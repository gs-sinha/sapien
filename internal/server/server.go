// Package server implements PLAN §22 (local engine HTTP API): a chi-based
// HTTP server over any engine.Engine (Local or, in tests, a fake). It is the
// only thing engine.Remote talks to.
package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/terminal"
	"github.com/gs-sinha/sapien/internal/ui"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// Options configures a Server.
type Options struct {
	Engine engine.Engine
	// Workspaces, when set, lets one server serve many workspaces: each
	// request selects one with the X-Sapien-Workspace header (see
	// workspacectx.go) and Engine is the primary/fallback. When nil the
	// server serves Engine alone, exactly as it always did.
	Workspaces *workspaces.Manager
	// Token is the bearer token every request but /v1/health must present.
	Token string
	// Version is reported by /v1/health, used by daemon clients to detect a
	// version mismatch (PLAN §4).
	Version string
	Logger  *slog.Logger
	// IdleTimeout, when positive, calls OnIdle after this long with no HTTP
	// requests and no open WebSocket connections.
	IdleTimeout time.Duration
	OnIdle      func()
}

// Server is the local engine HTTP API.
type Server struct {
	engine     engine.Engine
	workspaces *workspaces.Manager
	token      string
	version    string
	logger     *slog.Logger

	idle *idleTracker

	// recent buffers the last few hundred engine events (PLAN §34c) for
	// GET /v1/events/recent; recentCancel unsubscribes it, for Close.
	recent       *recentEvents
	recentCancel func()

	// terminal spawns and tracks the agent pane's PTY sessions (PLAN §34c
	// Phase 7b, handlers_terminal.go): one per open /v1/terminal WebSocket.
	terminal *terminal.Manager

	handler http.Handler
	openAPI []byte
}

// New builds a Server. Call Handler or ListenAndServe to serve it.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		engine:     opts.Engine,
		workspaces: opts.Workspaces,

		token:    opts.Token,
		version:  opts.Version,
		logger:   logger,
		idle:     newIdleTracker(opts.IdleTimeout, opts.OnIdle),
		recent:   newRecentEvents(),
		terminal: terminal.NewManager(),
	}
	s.openAPI = buildOpenAPI(routeTable)
	s.recentCancel = subscribeRecentEvents(context.Background(), s.engine.Events(), s.recent)
	s.handler = s.newRouter()
	return s
}

// Handler returns the http.Handler serving the API, for use with
// httptest.Server or a custom listener.
func (s *Server) Handler() http.Handler { return s.handler }

// Close stops the event subscription and terminates managed terminal sessions.
func (s *Server) Close() error {
	if s.recentCancel != nil {
		s.recentCancel()
	}
	s.terminal.CloseAll()
	return nil
}

// ListenAndServe binds addr (e.g. "127.0.0.1:0" to pick a free port) and
// returns promptly with the actual bound address (with the real port
// substituted): it starts listening, launches serving in a background
// goroutine, and returns -- it does not block until ctx is done. A second
// background goroutine watches ctx and shuts the server down gracefully
// (5s deadline) once it is cancelled. A bind failure (e.g. an
// already-in-use port) is returned immediately; failures during serving or
// shutdown, which happen after this function has already returned, are
// logged rather than returned.
func (s *Server) ListenAndServe(ctx context.Context, addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}

	actualAddr := ln.Addr().String()

	httpServer := &http.Server{Handler: s.handler}

	s.idle.start()

	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("serve failed", "addr", actualAddr, "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		s.idle.stop()
		_ = s.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("shutdown failed", "addr", actualAddr, "error", err)
		}
	}()

	return actualAddr, nil
}

// newRouter builds the chi router with every route from the route table,
// wrapped in logging, host/origin, idle-tracking, and (except for /v1/health)
// bearer-token middleware.
func (s *Server) newRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(s.hostOriginMiddleware)
	r.Use(s.loggingMiddleware)
	r.Use(s.idleMiddleware)

	r.NotFound(s.handleNotFound)
	r.MethodNotAllowed(s.handleMethodNotAllowed)

	for _, rt := range routeTable {
		var h http.Handler = rt.Handler(s)
		// Liveness must not wait behind workspace loading/indexing. It
		// describes the daemon, not a selected workspace.
		if rt.Pattern != "/v1/health" {
			h = s.workspaceMiddleware(h)
		}
		if rt.RequiresAuth {
			h = s.authMiddleware(h)
		}
		r.Method(rt.Method, rt.Pattern, h)
	}

	// The UI (PLAN §34c). GET /ui/session exchanges the daemon's bearer
	// token (a query parameter, from `sapien ui`'s printed URL) for an
	// HttpOnly sapien_session cookie and redirects to /ui/; it is
	// deliberately outside routeTable/authMiddleware (handlers_ui.go).
	// The built SPA under /ui/ and /ui/* is served unauthenticated: it is
	// static assets, not data, and every API call it makes goes through
	// routeTable above, which authMiddleware guards exactly as it does
	// for any other client, accepting this cookie as well as the header.
	r.Get("/ui/session", s.handleUISession)
	uiHandler := ui.Handler()
	r.Get("/ui", uiHandler.ServeHTTP)
	r.Get("/ui/*", uiHandler.ServeHTTP)

	// Runtime introspection: /debug/pprof/* and /debug/memstats
	// (handlers_debug.go). Outside routeTable because it is Go's surface,
	// not Sapien's API, so it must not appear in /v1/openapi.json -- but
	// wrapped in authMiddleware here and covered by hostOriginMiddleware
	// above, so it is guarded exactly like every /v1 route.
	debugRoutes := s.authMiddleware(s.debugHandler())
	r.Handle("/debug/*", debugRoutes)
	r.Handle("/debug", debugRoutes)

	return r
}

// idleTracker calls onIdle once after timeout elapses with no HTTP requests
// and no open WebSocket connections. It is safe for concurrent use.
type idleTracker struct {
	mu       sync.Mutex
	timeout  time.Duration
	onIdle   func()
	timer    *time.Timer
	activeWS int
	stopped  bool
}

func newIdleTracker(timeout time.Duration, onIdle func()) *idleTracker {
	return &idleTracker{timeout: timeout, onIdle: onIdle}
}

func (t *idleTracker) enabled() bool { return t.timeout > 0 && t.onIdle != nil }

// start arms the timer. Call once, when the server begins serving.
func (t *idleTracker) start() {
	if !t.enabled() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.timer = time.AfterFunc(t.timeout, t.fire)
}

func (t *idleTracker) fire() {
	t.mu.Lock()
	busy := t.stopped || t.activeWS > 0
	t.mu.Unlock()
	if busy {
		return
	}
	t.onIdle()
}

// touch resets the idle countdown. It is a no-op while any WebSocket is open.
func (t *idleTracker) touch() {
	if !t.enabled() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.activeWS > 0 {
		return
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(t.timeout, t.fire)
}

// wsOpened marks one WebSocket connection as open, pausing the idle timer.
func (t *idleTracker) wsOpened() {
	if !t.enabled() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activeWS++
	if t.timer != nil {
		t.timer.Stop()
	}
}

// wsClosed marks one WebSocket connection as closed, resuming the idle timer
// once no WebSocket connections remain open.
func (t *idleTracker) wsClosed() {
	if !t.enabled() {
		return
	}
	t.mu.Lock()
	t.activeWS--
	last := t.activeWS <= 0
	t.mu.Unlock()
	if last {
		t.touch()
	}
}

func (t *idleTracker) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	if t.timer != nil {
		t.timer.Stop()
	}
}
