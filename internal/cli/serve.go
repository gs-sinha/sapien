package cli

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/mcp"
	"github.com/gs-sinha/sapien/internal/server"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

func init() { Register(newServeCmd) }

// newServeCmd is `sapien serve` (PLAN §4, §21, §22): the daemon. It opens
// the workspace's engine in-process with the file watcher on, serves
// internal/server's HTTP API plus the MCP streamable-HTTP handler mounted
// at /mcp (guarded by the same bearer token, checked locally since
// server.Server exposes only Handler(), not a way to add routes beside its
// own), writes daemon.json so other invocations can find it, and shuts
// down gracefully on SIGINT/SIGTERM or after IdleTimeout with nothing in
// flight.
func newServeCmd(app *App) *cobra.Command {
	var port int
	var idleTimeout time.Duration
	var restart bool
	var foreground bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Sapien daemon: HTTP API plus the MCP endpoint, for this workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}

			if err := refuseIfDaemonAlive(cmd.Context(), ws, restart); err != nil {
				return err
			}
			lock, err := acquireWorkspaceLock(ws, restart)
			if err != nil {
				return err
			}
			defer func() { _ = lock.Release() }()

			eng, err := local.Open(ws, local.Options{Watch: true})
			if err != nil {
				return err
			}
			var closed sync.Once
			closeEngine := func() { closed.Do(func() { _ = eng.Close() }) }
			defer closeEngine()

			token := daemon.NewToken()

			idleCtx, idleCancel := context.WithCancel(context.Background())
			defer idleCancel()

			// One daemon, many workspaces (internal/workspaces): this
			// workspace is the primary -- the one a request that names none
			// gets, and the one whose lock and engine `serve` owns -- and
			// the manager opens any other workspace a request selects,
			// lazily, taking that workspace's own lock as it does.
			wsMgr := workspaces.New(ws, eng, workspaces.Options{
				Local: local.Options{Watch: true},
				PID:   os.Getpid(),
			})
			defer func() { _ = wsMgr.Close() }()

			srv := server.New(server.Options{
				Engine:     eng,
				Workspaces: wsMgr,
				Token:      token,
				Version:    Version,
			})

			mcpCfg, err := loadMCPConfig(ws)
			if err != nil {
				return err
			}
			mcpHandler := mcp.HTTPHandler(mcp.Options{Engine: eng, Workspaces: wsMgr, Config: mcpCfg, ConfigPaths: mcpConfigPaths(ws), Version: Version})

			mux := http.NewServeMux()
			mux.Handle("/mcp", bearerGuard(token, mcpHandler))
			mux.Handle("/", srv.Handler())

			guard := newIdleGuard(idleTimeout, idleCancel)
			wrapped := guard.wrap(mux)

			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				return errs.Wrap(errs.Internal, err, "binding 127.0.0.1:%d", port)
			}
			httpServer := &http.Server{Handler: wrapped}
			guard.start()

			serveErrCh := make(chan error, 1)
			go func() {
				if serveErr := httpServer.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
					serveErrCh <- serveErr
				}
			}()

			_, actualPortStr, _ := net.SplitHostPort(ln.Addr().String())
			actualPort, _ := strconv.Atoi(actualPortStr)

			info := &daemon.Info{
				PID:       os.Getpid(),
				Port:      actualPort,
				Token:     token,
				Version:   Version,
				Started:   time.Now(),
				Workspace: ws.Dir,
			}
			if err := daemon.Write(ws, info); err != nil {
				_ = ln.Close()
				return err
			}

			// Handshake (PLAN §4/§21): a human line always, or -- under
			// --json -- the full daemon.Info a caller like
			// daemonctl.go's spawnDaemon parses off this process's stdout.
			if app.Printer.IsJSON() {
				if err := app.Printer.JSON(info); err != nil {
					return err
				}
			} else {
				app.Printer.Line("sapien daemon listening on 127.0.0.1:%d (pid %d)", actualPort, info.PID)
			}

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sigCh)

			var runErr error
			select {
			case <-sigCh:
			case <-idleCtx.Done():
			case serveErr := <-serveErrCh:
				runErr = errs.Wrap(errs.Internal, serveErr, "serving")
			}

			guard.stop()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
			_ = daemon.Remove(ws)
			closeEngine()

			return runErr
		},
	}

	cmd.Flags().IntVar(&port, "port", 0, "TCP port to bind on 127.0.0.1 (0 picks a free port)")
	cmd.Flags().DurationVar(&idleTimeout, "idle-timeout", 30*time.Minute, "shut down after this long with no requests and no open connections (0 disables)")
	cmd.Flags().BoolVar(&restart, "restart", false, "stop a live daemon already running for this workspace first, instead of refusing to start")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "accepted for forward compatibility; sapien serve always runs in the foreground of its own process (see docs/BUILD-LOG.md)")
	_ = foreground

	return cmd
}

// refuseIfDaemonAlive implements PLAN §4's "one daemon per workspace"
// rule: a daemon.json naming a still-live process is a conflict unless
// restart is set, in which case that process is signaled and given up to
// 5s to exit (stopDaemon) before daemon.json is removed so this process
// can take over.
func refuseIfDaemonAlive(ctx context.Context, ws *domain.Workspace, restart bool) error {
	info, err := daemon.Read(ws)
	if err != nil {
		if errs.Is(err, errs.DaemonUnavailable) {
			return nil
		}
		return err
	}
	if !daemon.Alive(ctx, info) {
		return nil
	}
	if !restart {
		return errs.New(errs.Conflict, "a daemon is already running for workspace %q (pid %d, port %d)", ws.Dir, info.PID, info.Port).
			WithHint("run `sapien serve --restart`")
	}
	_ = stopDaemon(info)
	return daemon.Remove(ws)
}

// acquireWorkspaceLock takes the daemon's exclusive claim on ws
// (daemon.Acquire). A live holder that daemon.json no longer names, an
// orphan from an earlier replacement, is a conflict unless restart is set,
// in which case it is stopped and the lock taken over. This is what keeps
// two daemons from ever indexing the same workspace at once.
func acquireWorkspaceLock(ws *domain.Workspace, restart bool) (*daemon.Lock, error) {
	lock, err := daemon.Acquire(ws, os.Getpid())
	if err == nil {
		return lock, nil
	}
	if errs.CodeOf(err) != errs.Conflict || !restart {
		return nil, err
	}
	if holder, ok := daemon.Holder(ws); ok {
		_ = stopDaemon(&daemon.Info{PID: holder})
	}
	return daemon.Acquire(ws, os.Getpid())
}

// bearerGuard requires "Authorization: Bearer <token>" on every request to
// next. It mirrors internal/server's own (unexported) authMiddleware so
// the MCP handler mounted at /mcp is guarded exactly like the rest of the
// daemon's HTTP API, without internal/server needing to expose anything
// beyond Handler().
func bearerGuard(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		authz := r.Header.Get("Authorization")
		presented := strings.TrimPrefix(authz, prefix)
		if !strings.HasPrefix(authz, prefix) || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(errs.New(errs.PermissionDenied, "missing or invalid bearer token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// idleGuard fires onIdle once after timeout elapses with no in-flight
// request on the handler it wraps (PLAN §4: "idle-exits after 30 min with
// no clients"). A WebSocket or MCP streamable-HTTP connection counts as
// in-flight for as long as its ServeHTTP call hasn't returned, so a
// long-lived connection keeps the daemon busy -- the same accounting
// internal/server's own (unexported) idleTracker uses for its WebSocket
// endpoint, reimplemented here because serve.go serves its own mux rather
// than server.Server.Handler() directly (Task B: "If ListenAndServe
// cannot accept a pre-built mux, serve the mux with your own http.Server").
type idleGuard struct {
	mu      sync.Mutex
	timeout time.Duration
	onIdle  func()
	active  int
	timer   *time.Timer
	stopped bool
}

func newIdleGuard(timeout time.Duration, onIdle func()) *idleGuard {
	return &idleGuard{timeout: timeout, onIdle: onIdle}
}

func (g *idleGuard) enabled() bool { return g.timeout > 0 && g.onIdle != nil }

// start arms the timer. Call once, when the server begins serving.
func (g *idleGuard) start() {
	if !g.enabled() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	g.timer = time.AfterFunc(g.timeout, g.fire)
}

func (g *idleGuard) fire() {
	g.mu.Lock()
	busy := g.stopped || g.active > 0
	g.mu.Unlock()
	if !busy {
		g.onIdle()
	}
}

func (g *idleGuard) enter() {
	if !g.enabled() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active++
	if g.timer != nil {
		g.timer.Stop()
	}
}

func (g *idleGuard) leave() {
	if !g.enabled() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if g.active <= 0 && !g.stopped {
		g.timer = time.AfterFunc(g.timeout, g.fire)
	}
}

// stop disables the guard permanently. Call once, during shutdown.
func (g *idleGuard) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	if g.timer != nil {
		g.timer.Stop()
	}
}

// wrap returns next instrumented with enter/leave around every request.
func (g *idleGuard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.enter()
		defer g.leave()
		next.ServeHTTP(w, r)
	})
}
