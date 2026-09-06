package cli

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/growsimplee/sapien/internal/daemon"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

func init() { Register(newDaemonCmd) }

// newDaemonCmd is `sapien daemon status|stop`: small helpers so a user can
// inspect or stop the daemon `sapien serve` or `sapien mcp` started for
// this workspace, without needing to know where daemon.json lives.
func newDaemonCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Inspect or stop the daemon for this workspace",
	}
	cmd.AddCommand(newDaemonStatusCmd(app), newDaemonStopCmd(app))
	return cmd
}

func newDaemonStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether a daemon is running for this workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}

			info, err := daemon.Read(ws)
			if err != nil {
				if !errs.Is(err, errs.DaemonUnavailable) {
					return err
				}
				if app.Printer.IsJSON() {
					return app.Printer.JSON(map[string]any{"running": false})
				}
				app.Printer.Line("no daemon running for %s", ws.Dir)
				return nil
			}

			alive := daemon.Alive(cmd.Context(), info)
			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{
					"running": alive,
					"pid":     info.PID,
					"port":    info.Port,
					"version": info.Version,
					"started": info.Started,
				})
			}
			if !alive {
				app.Printer.Line("daemon.json for %s names pid %d, but it is not responding (stale)", ws.Dir, info.PID)
				return nil
			}
			app.Printer.Line("sapien daemon running: pid %d, port %d, version %s, started %s",
				info.PID, info.Port, info.Version, info.Started.Format(time.RFC3339))
			return nil
		},
	}
}

func newDaemonStopCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon running for this workspace, if any",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}

			info, err := daemon.Read(ws)
			if err != nil {
				if !errs.Is(err, errs.DaemonUnavailable) {
					return err
				}
				if app.Printer.IsJSON() {
					return app.Printer.JSON(map[string]any{"stopped": false})
				}
				app.Printer.Line("no daemon running for %s", ws.Dir)
				return nil
			}

			if daemon.Alive(cmd.Context(), info) {
				if err := stopDaemon(info); err != nil {
					return err
				}
			}
			if err := daemon.Remove(ws); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{"stopped": true, "pid": info.PID})
			}
			app.Printer.Line("stopped daemon (pid %d)", info.PID)
			return nil
		},
	}
}

// findOrStartDaemon returns the live, version-matching daemon for ws,
// spawning one via spawnDaemon if none is running yet. `sapien mcp` (PLAN
// §4: "always daemon-backed... starts the daemon if none is running") is
// the only caller; every one-shot command instead goes through
// engine_wiring.go's NewEngine, which never starts one.
func findOrStartDaemon(ctx context.Context, ws *domain.Workspace, version string) (*daemon.Info, error) {
	info, err := daemon.Find(ctx, ws, version)
	if err == nil && info == nil {
		// No daemon.json, yet a live process holds the workspace lock: an
		// orphan from a replacement that lost track of it. Stop it before
		// starting ours so two daemons never watch the same workspace.
		if holder, ok := daemon.Holder(ws); ok {
			slog.Default().Info("stopping orphaned daemon holding the workspace lock", "pid", holder, "workspace", ws.Dir)
			_ = stopDaemon(&daemon.Info{PID: holder})
		}
	}
	switch {
	case err != nil && errs.CodeOf(err) == errs.Conflict:
		// A live daemon from a different build, typically left behind by
		// `make build` while an agent session kept it busy. Unlike the
		// one-shot CLI (engine_wiring.go), which reports the mismatch and
		// hints at `sapien serve --restart`, `sapien mcp` is spawned by an
		// agent host with nobody at a terminal to act on a hint, and the
		// host's entry already points at the rebuilt binary -- so the only
		// useful behaviour is to replace the old daemon with this build's.
		if err := replaceStaleDaemon(ctx, ws, version); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case info != nil:
		return info, nil
	}
	return spawnDaemon(ctx, ws, version)
}

// replaceStaleDaemon stops the daemon recorded in ws's daemon.json (SIGTERM,
// waiting up to 5s via stopDaemon) and removes the file, so the caller can
// spawn this build's daemon in its place. A missing daemon.json is not an
// error: the previous daemon exited on its own in the meantime.
func replaceStaleDaemon(ctx context.Context, ws *domain.Workspace, version string) error {
	_ = ctx
	info, err := daemon.Read(ws)
	if err != nil {
		if errs.Is(err, errs.DaemonUnavailable) {
			return nil
		}
		return err
	}
	slog.Default().Info("replacing daemon from another build",
		"workspace", ws.Dir, "daemon_version", info.Version, "version", version, "pid", info.PID)
	if err := stopDaemon(info); err != nil {
		return err
	}
	// daemon.json can name one process while another, older one still
	// holds the workspace lock (it was never stopped when it was replaced);
	// stop that one too, or it keeps indexing with its own grammar.
	if holder, ok := daemon.Holder(ws); ok && holder != info.PID {
		slog.Default().Info("stopping orphaned daemon holding the workspace lock", "pid", holder, "workspace", ws.Dir)
		_ = stopDaemon(&daemon.Info{PID: holder})
	}
	return daemon.Remove(ws)
}

// spawnDaemon starts "<self> serve --json --workspace <ws.Dir>" as a
// detached child (a new session via Setsid, so it outlives this process
// and isn't killed by signals delivered to this process's terminal), reads
// the handshake JSON serve.go's RunE prints to stdout on startup (a
// daemon.Info), and returns it. version is unused directly (the spawned
// process reports its own Version, which is always this build's) but kept
// in the signature for symmetry with findOrStartDaemon/daemon.Find.
func spawnDaemon(ctx context.Context, ws *domain.Workspace, version string) (*daemon.Info, error) {
	_ = version

	exePath, err := os.Executable()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "resolving the running executable's path")
	}

	child := exec.Command(exePath, "serve", "--json", "--workspace", ws.Dir)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stdout, err := child.StdoutPipe()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "opening daemon stdout pipe")
	}
	// A spawned daemon has no terminal; keep its log (warnings such as a
	// failed reindex) where `sapien daemon status` can point to it.
	if logFile, lerr := os.OpenFile(daemonLogPath(ws), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); lerr == nil {
		child.Stderr = logFile
		defer logFile.Close()
	}

	if err := child.Start(); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "starting %s serve", exePath)
	}
	// Reap the child's exit whenever it eventually happens, without
	// blocking on it now: it is meant to keep running as the workspace's
	// daemon long after this "sapien mcp" invocation has exited.
	go func() { _ = child.Wait() }()

	type handshake struct {
		info *daemon.Info
		err  error
	}
	resCh := make(chan handshake, 1)
	go func() {
		var info daemon.Info
		if err := json.NewDecoder(stdout).Decode(&info); err != nil {
			resCh <- handshake{err: errs.Wrap(errs.Internal, err, "reading daemon handshake")}
			return
		}
		resCh <- handshake{info: &info}
	}()

	select {
	case h := <-resCh:
		if h.err != nil {
			return nil, h.err
		}
		return h.info, nil
	case <-time.After(10 * time.Second):
		return nil, errs.New(errs.Internal, "timed out waiting for the spawned daemon's handshake")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// stopDaemon sends SIGTERM to info's process and waits up to 5s for it to
// exit, polling every 100ms via a signal-0 existence probe. It does not
// touch daemon.json; callers remove that themselves once ready to (`daemon
// stop` above, and `serve --restart` in serve.go).
func stopDaemon(info *daemon.Info) error {
	if info == nil || info.PID <= 0 {
		return nil
	}
	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // already gone
		}
		return errs.Wrap(errs.Internal, err, "signaling daemon pid %d", info.PID)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(info.PID, 0) != nil {
			return nil // exited
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil // gave up waiting; the caller proceeds regardless
}

// daemonLogPath is where a spawned daemon's stderr goes:
// <ws>/.sapien/daemon.log.
func daemonLogPath(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.WorkspaceStateDir, "daemon.log")
}
