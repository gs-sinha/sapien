package cli

import (
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
)

// spawnRestartedDaemon returns the closure serve.go hands to
// server.Options.RestartHook (PLAN §34f item 3): called by POST
// /v1/daemon/restart and POST /v1/update/apply once their response has
// been flushed, it spawns a detached `<self> serve --restart --json
// --workspace <ws.Dir>` successor on the same port and idle-timeout this
// daemon was started with, mirroring spawnDaemon's Setsid + daemon.log
// handling.
//
// Unlike spawnDaemon (which findOrStartDaemon uses to start a daemon on a
// one-shot command's behalf and then waits for), this deliberately does
// not read the child's handshake off its stdout: the successor's own
// `--restart` flag makes its startup stop *this* process as a side effect
// (serve.go's refuseIfDaemonAlive), so by the time it would write one,
// the goroutine reading for it here may already be gone. The child's
// stdout/stderr go to daemon.log instead, same as a normally spawned
// daemon's stderr.
func spawnRestartedDaemon(ws *domain.Workspace, port int, idleTimeout time.Duration) func() {
	return func() {
		exePath, err := os.Executable()
		if err != nil {
			slog.Default().Error("daemon restart: could not resolve the running executable's path", "error", err)
			return
		}

		child := exec.Command(exePath, "serve", "--restart", "--json",
			"--workspace", ws.Dir,
			"--port", strconv.Itoa(port),
			"--idle-timeout", idleTimeout.String(),
		)
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

		if logFile, lerr := os.OpenFile(daemonLogPath(ws), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); lerr == nil {
			child.Stdout = logFile
			child.Stderr = logFile
			defer logFile.Close()
		}

		if err := child.Start(); err != nil {
			slog.Default().Error("daemon restart: failed to spawn successor", "error", err)
			return
		}
		// Reap it whenever it eventually exits, without blocking on it now
		// -- it is meant to keep running long after this call returns (and,
		// per the doc above, this process is likely to be gone before it
		// does).
		go func() { _ = child.Wait() }()
	}
}
