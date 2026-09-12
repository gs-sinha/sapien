package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
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

			// A missing daemon.json is not the end of the story: an orphan
			// that lost its discovery record still holds the workspace
			// lock, and being rid of it is exactly why someone runs `daemon
			// stop`. Carry on with a nil info and let the lock decide.
			info, err := daemon.Read(ws)
			if err != nil && !errs.Is(err, errs.DaemonUnavailable) {
				return err
			}

			// Gate on the *process*, not on daemon.Alive: a wedged daemon
			// (swapped out, part-way through a long index, SIGSTOPped)
			// fails the health probe while it is still running, still
			// watching the workspace and still holding several hundred MB.
			// Skipping the signal for those and removing daemon.json anyway
			// is how `daemon stop` used to manufacture the orphans this
			// command exists to clear: it printed "stopped daemon (pid N)"
			// about a process that was very much alive and could never be
			// named again.
			var pids []int
			if daemon.Running(info) {
				pids = append(pids, info.PID)
			}
			// daemon.json is only half the story: the workspace lock always
			// names the live holder, which can be an older daemon that no
			// daemon.json refers to any more. findOrStartDaemon and
			// replaceStaleDaemon already stop that one, and so must this.
			if holder, ok := daemon.Holder(ws); ok && (info == nil || holder != info.PID) {
				pids = append(pids, holder)
			}

			if len(pids) == 0 {
				// Nothing is running: at most a daemon.json left behind by
				// a daemon that already exited, which is safe to clear.
				if info != nil {
					if err := daemon.Remove(ws); err != nil {
						return err
					}
				}
				if app.Printer.IsJSON() {
					return app.Printer.JSON(map[string]any{"stopped": false})
				}
				app.Printer.Line("no daemon running for %s", ws.Dir)
				return nil
			}

			for _, pid := range pids {
				if err := stopDaemon(&daemon.Info{PID: pid}); err != nil {
					// Leave daemon.json where it is: it is the only record
					// of a process that demonstrably survived SIGTERM and
					// SIGKILL, and removing it is exactly what turns a
					// survivor into an orphan.
					return errs.As(err).WithHint(fmt.Sprintf(
						"daemon.json was left in place so pid %d can still be found; check `ps -p %d`", pid, pid))
				}
			}
			if err := daemon.Remove(ws); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				// "pid" stays for the callers that read it (it is the
				// daemon.json one whenever there was one); "pids" is the
				// honest full answer, which can also name a lock holder.
				return app.Printer.JSON(map[string]any{"stopped": true, "pid": pids[0], "pids": pids})
			}
			for _, pid := range pids {
				app.Printer.Line("stopped daemon (pid %d)", pid)
			}
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
	info, err := findDaemon(ctx, ws, version)
	if err == nil && info == nil {
		// No daemon.json, yet a live process holds the workspace lock: an
		// orphan from a replacement that lost track of it. Stop it before
		// starting ours so two daemons never watch the same workspace.
		if holder, ok := daemon.Holder(ws); ok {
			slog.Default().Info("stopping orphaned daemon holding the workspace lock", "pid", holder, "workspace", ws.Dir)
			if err := stopDaemon(&daemon.Info{PID: holder}); err != nil {
				return nil, err
			}
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

// How hard a caller tries before giving up on a daemon whose process is
// alive but whose health probe did not answer. Vars, not constants, only so
// a test can shrink the wait.
var (
	// findDaemonAttempts counts the first probe too: 3 is one try and two
	// retries.
	findDaemonAttempts = 3
	// findDaemonRetryDelay is the pause between them.
	findDaemonRetryDelay = 1 * time.Second
)

// findDaemon is daemon.Find with a bounded retry for the one outcome worth
// waiting out: errs.DaemonUnavailable, which Find returns when the daemon's
// process exists but its health probe timed out. Such a daemon is busy, not
// broken -- mid-reindex, or being paged back in after the laptop woke -- and
// it answers a moment later. Every other outcome is returned from the first
// attempt exactly as Find gave it: errs.Conflict (a daemon from another
// build, which the caller replaces) and the (nil, nil) "no daemon here"
// (which sends the caller to engine.Local) must keep behaving as they do
// today.
//
// Three attempts a second apart is a deliberate ~9s worst case: each attempt
// spends up to the health probe's own 2s budget (daemon.Alive), plus two 1s
// pauses in between. Long enough to ride out the stalls measured on a real
// workspace, short enough that a one-shot CLI command still feels like a
// command rather than a hang. `sapien mcp` is why this exists at all: it
// used to exit on the first failed probe, and an agent host then marks the
// MCP server failed for the whole session even though the daemon was back a
// second later -- the error's own "retry shortly" had nobody to act on it.
//
// On exhaustion the DaemonUnavailable error is returned unchanged. There is
// deliberately no fall back to engine.Local: opening a second engine on a
// live daemon's database is precisely the hazard preserving discovery in
// Find was meant to remove.
func findDaemon(ctx context.Context, ws *domain.Workspace, version string) (*daemon.Info, error) {
	for attempt := 1; ; attempt++ {
		info, err := daemon.Find(ctx, ws, version)
		if err == nil || errs.CodeOf(err) != errs.DaemonUnavailable || attempt >= findDaemonAttempts {
			return info, err
		}
		slog.Default().Info("daemon is running but not responding; retrying",
			"workspace", ws.Dir, "attempt", attempt, "attempts", findDaemonAttempts, "delay", findDaemonRetryDelay)
		select {
		case <-time.After(findDaemonRetryDelay):
		case <-ctx.Done():
			// The caller gave up (Ctrl-C, or a cancelled MCP request):
			// stop waiting, but report the daemon's own error rather than
			// a bare context one -- it is the answer to what was asked.
			return nil, err
		}
	}
}

// replaceStaleDaemon stops the daemon recorded in ws's daemon.json (via
// stopDaemon: SIGTERM, then SIGKILL if it will not go) and removes the
// file, so the caller can spawn this build's daemon in its place. A missing daemon.json is not an
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
		if err := stopDaemon(&daemon.Info{PID: holder}); err != nil {
			return err
		}
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

// stopDaemon's two waits, plus the interval of the existence probe inside
// them. Package-level vars rather than constants only so a test can shrink
// them: proving the SIGKILL escalation otherwise means sitting out the full
// SIGTERM grace period.
var (
	// stopGraceWindow is how long a daemon gets to shut down on its own
	// after SIGTERM -- enough to finish a request in flight, remove its
	// daemon.json and release the workspace lock.
	stopGraceWindow = 5 * time.Second
	// stopKillWindow is how long the process gets after SIGKILL, which it
	// cannot catch or ignore; only one stuck in an uninterruptible kernel
	// wait outlives it.
	stopKillWindow = 2 * time.Second
	// stopPollInterval is how often the signal-0 existence probe runs
	// inside either window.
	stopPollInterval = 100 * time.Millisecond
)

// stopDaemon stops info's process and waits for it to genuinely exit:
// SIGTERM first, then -- if the process is still there after
// stopGraceWindow -- SIGKILL, then a further stopKillWindow. It returns an
// error only if the process survives both.
//
// The escalation is what makes the callers honest. The daemon this is aimed
// at is wedged (swapped out, part-way through a long index, SIGSTOPped), so
// it may never run its SIGTERM handler at all; with no escalation `serve
// --restart` and `daemon stop` reported failure and left the user with no
// way back short of running `kill -9` themselves, while the workspace stayed
// locked by a process nothing could remove. A SIGKILL is logged, never
// silent: losing a daemon's graceful shutdown is worth a line in the log.
//
// It does not touch daemon.json; callers remove that themselves once ready
// to (`daemon stop` above, and `serve --restart` in serve.go).
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
	if waitForExit(info.PID, stopGraceWindow) {
		return nil
	}

	slog.Default().Info("daemon did not exit on SIGTERM; escalating to SIGKILL",
		"pid", info.PID, "waited", stopGraceWindow)
	if err := syscall.Kill(info.PID, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // exited between the last probe and this signal
		}
		return errs.Wrap(errs.Internal, err, "killing daemon pid %d", info.PID)
	}
	if waitForExit(info.PID, stopKillWindow) {
		return nil
	}
	return errs.New(errs.Internal, "daemon pid %d did not exit after SIGTERM and SIGKILL", info.PID)
}

// waitForExit polls pid every stopPollInterval with a signal-0 existence
// probe (the same one daemon.Running uses) until it is gone or window has
// elapsed, and reports whether it exited.
func waitForExit(pid int, window time.Duration) bool {
	deadline := time.Now().Add(window)
	for {
		if syscall.Kill(pid, 0) != nil {
			return true // exited
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(stopPollInterval)
	}
}

// daemonLogPath is where a spawned daemon's stderr goes:
// <ws>/.sapien/daemon.log.
func daemonLogPath(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.WorkspaceStateDir, "daemon.log")
}
