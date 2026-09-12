package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
)

// deadPort returns a port nothing is listening on, so daemon.Alive's health
// probe fails against a process that is nonetheless running -- the wedged
// daemon this file is about.
func deadPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

// writeWedgedDaemon points ws's daemon.json at pid on a port nothing
// answers: a live process that fails its health check, exactly as a daemon
// swapped out, part-way through a long index or SIGSTOPped does.
func writeWedgedDaemon(t *testing.T, ws *domain.Workspace, pid int) *daemon.Info {
	t.Helper()
	info := &daemon.Info{
		PID: pid, Port: deadPort(t), Token: "wedged-daemon-token",
		Version: cli.Version, Started: time.Now(), Workspace: ws.Dir,
	}
	require.NoError(t, daemon.Write(ws, info))
	require.False(t, daemon.Alive(context.Background(), info), "the fixture must fail its health check")
	require.True(t, daemon.Running(info), "the fixture's process must still be running")
	return info
}

// zombiePID starts a child that exits immediately and deliberately leaves it
// unreaped until the test ends. A zombie answers the signal-0 existence
// probe forever and shrugs off every signal, so it stands in for the one
// process stopDaemon cannot get rid of -- the case where printing "stopped"
// would be a lie.
func zombiePID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() })
	return cmd.Process.Pid
}

// spawnDeafProcess starts a child that ignores SIGTERM outright and waits
// until it says so, so the test never races the trap being installed. It
// stands in for the wedged daemon of the bug report -- there, a daemon
// SIGSTOPped mid-index, whose own SIGTERM handler (serve.go installs one)
// meant the signal merely stayed pending instead of killing it. Either way
// SIGTERM has no effect and only SIGKILL ends it.
func spawnDeafProcess(t *testing.T) int {
	t.Helper()
	// The inner sleeps are one second each so that killing the shell leaves
	// nothing behind for longer than that.
	cmd := exec.Command("sh", "-c", `trap "" TERM; echo ready; while :; do sleep 1; done`)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	// Reap it as soon as it exits: an unreaped zombie still answers the
	// signal-0 probe, so stopDaemon would never see it go.
	go func() { _ = cmd.Wait() }()

	ready := make(chan struct{})
	go func() {
		_, _ = io.ReadFull(stdout, make([]byte, len("ready\n")))
		close(ready)
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the SIGTERM-proof child never reported readiness")
	}
	return cmd.Process.Pid
}

// A daemon that fails its health check is not gone. `daemon stop` used to
// gate the signal on daemon.Alive, so it never signalled a wedged one, yet
// removed daemon.json anyway -- leaving a live process holding the
// workspace that nothing could ever name again.
func TestDaemonStop_KillsAWedgedDaemonThatFailsItsHealthCheck(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	pid := spawnKillableProcess(t)
	writeWedgedDaemon(t, ws, pid)

	stdout, stderr, code := run(t, "--workspace", wsDir, "daemon", "stop", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, true, got["stopped"])
	assert.Equal(t, float64(pid), got["pid"])
	assert.Error(t, syscall.Kill(pid, 0), "the wedged daemon's process should be gone")
	assert.NoFileExists(t, daemon.Path(ws))
}

// daemon.json is the only record of a process that survived both signals,
// so a failed stop must keep it -- removing it is exactly what turns a
// survivor into an orphan -- and must not claim to have stopped anything.
func TestDaemonStop_KeepsDaemonJSONWhenTheProcessSurvives(t *testing.T) {
	defer cli.SetStopWindows(100*time.Millisecond, 100*time.Millisecond, 10*time.Millisecond)()

	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)
	writeWedgedDaemon(t, ws, zombiePID(t))

	stdout, stderr, code := run(t, "--workspace", wsDir, "daemon", "stop")
	assert.Equal(t, 2, code, "stdout: %s", stdout)
	assert.NotContains(t, stdout, "stopped daemon")
	assert.Contains(t, stderr, "did not exit after SIGTERM and SIGKILL")
	assert.FileExists(t, daemon.Path(ws), "the surviving daemon must stay discoverable")
}

// An orphan that lost its daemon.json still holds the workspace lock, and
// being rid of it is why someone runs `daemon stop`. findOrStartDaemon and
// replaceStaleDaemon already stop that process; so does this.
func TestDaemonStop_StopsAnOrphanHoldingTheWorkspaceLock(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	orphan := spawnKillableProcess(t)
	require.NoError(t, os.WriteFile(daemon.LockPath(ws), []byte(strconv.Itoa(orphan)+"\n"), 0o600))

	stdout, stderr, code := run(t, "--workspace", wsDir, "daemon", "stop")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "stopped daemon (pid "+strconv.Itoa(orphan)+")")
	assert.Error(t, syscall.Kill(orphan, 0), "the lock holder should be gone")
}

// A daemon.json naming a process that already exited is just litter: it is
// cleared, and nothing is reported as stopped, because nothing was.
func TestDaemonStop_ClearsAStaleDaemonJSONWithoutClaimingAStop(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	exited := spawnKillableProcess(t)
	require.NoError(t, syscall.Kill(exited, syscall.SIGKILL))
	require.Eventually(t, func() bool { return syscall.Kill(exited, 0) != nil }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, daemon.Write(ws, &daemon.Info{PID: exited, Port: deadPort(t), Version: cli.Version}))

	stdout, stderr, code := run(t, "--workspace", wsDir, "daemon", "stop", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, false, got["stopped"])
	assert.NoFileExists(t, daemon.Path(ws))
}

// A wedged daemon may never act on SIGTERM at all, so stopDaemon escalates
// to SIGKILL instead of giving up -- and logs that it did, because losing a
// daemon's graceful shutdown should never be silent.
func TestStopDaemon_EscalatesToSIGKILLWhenSIGTERMIsIgnored(t *testing.T) {
	defer cli.SetStopWindows(150*time.Millisecond, 5*time.Second, 10*time.Millisecond)()

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	pid := spawnDeafProcess(t)

	require.NoError(t, cli.StopDaemon(&daemon.Info{PID: pid}))
	assert.Error(t, syscall.Kill(pid, 0), "SIGKILL should have got rid of the deaf process")
	assert.Contains(t, logs.String(), "escalating to SIGKILL")
}
