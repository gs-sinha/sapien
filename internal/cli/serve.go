package cli

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/mcp"
	"github.com/gs-sinha/sapien/internal/server"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// DefaultPort is the port `sapien serve` binds unless --port says
// otherwise. It is fixed rather than 0-and-random so that the browser UI
// has a stable origin: the daemon idle-exits after 30 minutes and is
// replaced outright whenever the binary is upgraded
// (daemonctl.go's replaceStaleDaemon), and on a random port every one of
// those events strands every open tab at an address nothing is listening
// on any more, with no way for the page to discover where the daemon
// went. With a stable origin, the tab a user left open comes back on
// reload once `sapien ui` has minted a fresh session cookie -- which is
// shared across tabs at that origin, so one relaunch revives the whole
// set.
//
// 7717 is in the IANA dynamic range and is not a registered service.
const DefaultPort = 7717

// listenLoopback binds port on 127.0.0.1. When the port is already taken
// and mayFallBack is set -- meaning the caller did not ask for this
// particular port, it is just DefaultPort -- it retries on an ephemeral
// port rather than refusing to start: something else on the machine
// holding 7717 should cost the stable origin, not the daemon. An
// explicit --port is honoured exactly, so a caller that pinned a port is
// told when it could not have it instead of silently getting another.
func listenLoopback(port int, mayFallBack bool) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		return ln, nil
	}
	if !mayFallBack || port == 0 || !errors.Is(err, syscall.EADDRINUSE) {
		return nil, errs.Wrap(errs.Internal, err, "binding 127.0.0.1:%d", port)
	}
	slog.Default().Info("default port is in use; falling back to an ephemeral port",
		"port", port, "hint", "open tabs will not survive a daemon restart on an ephemeral port")
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "binding 127.0.0.1:0 after %d was in use", port)
	}
	return ln, nil
}

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

			cfg, err := config.Load(ws)
			if err != nil {
				return err
			}
			applyMemoryLimit(cfg.Daemon.MemoryLimitBytes())

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

			defer srv.Close()

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

			// Hand the heap back to the OS once the daemon stops working.
			// Tied to idleCtx, which is cancelled either by the idle-exit
			// guard above or by this command returning.
			startScavenger(idleCtx)

			ln, err := listenLoopback(port, !cmd.Flags().Changed("port"))
			if err != nil {
				return err
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
			_ = srv.Close()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
			_ = daemon.Remove(ws)
			closeEngine()

			return runErr
		},
	}

	cmd.Flags().IntVar(&port, "port", DefaultPort, "TCP port to bind on 127.0.0.1 (0 picks a free port)")
	cmd.Flags().DurationVar(&idleTimeout, "idle-timeout", 30*time.Minute, "shut down after this long with no requests and no open connections (0 disables)")
	cmd.Flags().BoolVar(&restart, "restart", false, "stop a live daemon already running for this workspace first, instead of refusing to start")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "accepted for forward compatibility; sapien serve always runs in the foreground of its own process (see docs/BUILD-LOG.md)")
	_ = foreground

	return cmd
}

// applyMemoryLimit gives the daemon a soft heap ceiling (config
// daemon.memory_limit, 2 GiB by default; see config.defaultDaemonMemoryLimit
// for where that number comes from).
//
// Only the daemon gets one. A one-shot CLI invocation exits in a second and
// would only pay the GC cost of a ceiling it can never approach; the daemon
// is the process that lives for hours and re-indexes on every file change,
// and the one that was observed reaching an 18 GB footprint. The limit is
// soft -- Go collects harder as the heap approaches it and never fails an
// allocation because of it -- so the worst case is a slower re-index, not a
// broken one.
//
// A GOMEMLIMIT already set in the environment wins: the runtime has applied
// it before main even runs, and an operator who set it meant it.
func applyMemoryLimit(limit int64) {
	if limit <= 0 {
		return
	}
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	debug.SetMemoryLimit(limit)
}

// Idle scavenging (see startScavenger).
const (
	// scavengeInterval is how often the daemon checks whether it has gone
	// quiet. Long enough that the check itself is free, short enough that a
	// burst of indexing does not leave its pages behind for the rest of the
	// afternoon.
	scavengeInterval = 5 * time.Minute
	// scavengeQuietBytes is how little the daemon must have allocated since
	// the previous check to count as quiet. A daemon doing nothing still
	// allocates a trickle -- timers, the odd health check -- so the
	// threshold is a few megabytes rather than zero, and well under what
	// even one service resync costs.
	scavengeQuietBytes = 8 << 20
)

// startScavenger returns the freed heap to the operating system once the
// daemon stops working, until ctx is done.
//
// Go's own scavenger is not enough here, and the reason is worth writing
// down. After a burst of re-indexing the heap is mostly garbage, but the
// collector is demand-driven: with nothing allocating, nothing reaches the
// next GC target, so no collection runs, the garbage is never identified as
// free, and the background scavenger has nothing to hand back. (The runtime
// does force a collection every two minutes, but only that, and its
// scavenger then returns the pages on its own slow pacing.) The daemon
// otherwise sits on its high-water mark -- 1.2 GB in the reproduction for
// this change, and far worse before it -- which on macOS shows up in
// phys_footprint, and in the swap file, long after the work is over, and is
// what made this look like a leak in the first place. debug.FreeOSMemory is
// exactly the missing step: collect, then release, at once.
//
// It only runs when the process really has gone quiet, measured by how
// little it has allocated since the last check, so a busy daemon is never
// interrupted by a forced collection whose pages it is about to want back.
func startScavenger(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(scavengeInterval)
		defer ticker.Stop()

		var prev runtime.MemStats
		runtime.ReadMemStats(&prev)
		lastTotal := prev.TotalAlloc

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				allocated := m.TotalAlloc - lastTotal
				lastTotal = m.TotalAlloc
				if allocated > scavengeQuietBytes {
					continue
				}
				debug.FreeOSMemory()
			}
		}
	}()
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
	if err := stopDaemon(info); err != nil {
		return err
	}
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
		if err := stopDaemon(&daemon.Info{PID: holder}); err != nil {
			return nil, err
		}
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
