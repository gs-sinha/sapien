package cli_test

import (
	"context"
	"encoding/json"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
)

// fakeSpawnDaemon returns a spawnDaemonFunc substitute (see
// cli.SetSpawnDaemonFunc) that never execs anything: it just records that
// it was called and returns info, exactly what `daemon restart`'s "then
// start a fresh one" half needs without the recursive-test-binary trap
// spawnDaemon's real exec.Command(os.Executable(), "serve", ...) would be
// under `go test` (os.Executable() there is the compiled test binary, not
// a built `sapien`).
func fakeSpawnDaemon(t *testing.T, info *daemon.Info) (func(context.Context, *domain.Workspace, string) (*daemon.Info, error), *int) {
	t.Helper()
	calls := 0
	return func(ctx context.Context, ws *domain.Workspace, version string) (*daemon.Info, error) {
		calls++
		return info, nil
	}, &calls
}

func TestDaemonRestart_NoDaemonRunning_JustStarts(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	want := &daemon.Info{PID: 424242, Port: 4242, Version: cli.Version}
	fn, calls := fakeSpawnDaemon(t, want)
	defer cli.SetSpawnDaemonFunc(fn)()

	stdout, stderr, code := run(t, "--workspace", wsDir, "daemon", "restart", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, 1, *calls)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, true, got["restarted"])
	assert.Equal(t, float64(want.PID), got["pid"])
	assert.Equal(t, float64(want.Port), got["port"])
	assert.Empty(t, got["stopped"], "nothing was running, so nothing should be reported stopped")
}

func TestDaemonRestart_StopsLiveDaemonThenStarts(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	oldPID := spawnKillableProcess(t)
	startFakeDaemon(t, ws, cli.Version, oldPID)

	want := &daemon.Info{PID: 424243, Port: 4243, Version: cli.Version}
	fn, calls := fakeSpawnDaemon(t, want)
	defer cli.SetSpawnDaemonFunc(fn)()

	stdout, stderr, code := run(t, "--workspace", wsDir, "daemon", "restart", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, 1, *calls)
	assert.Error(t, syscall.Kill(oldPID, 0), "the old daemon must actually be signalled")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, true, got["restarted"])
	assert.Equal(t, float64(want.PID), got["pid"])
	stopped, ok := got["stopped"].([]any)
	require.True(t, ok)
	require.Len(t, stopped, 1)
	assert.Equal(t, float64(oldPID), stopped[0])

	// The old daemon.json record must be gone before the (faked) spawn --
	// restartDaemon removes it as soon as the stop succeeds, rather than
	// leaving the stale record around for whatever starts next to trip
	// over. The real spawned child would write its own; this fake does
	// not, so the file's absence here is exactly what proves the removal
	// happened rather than being masked by a fresh write.
	assert.NoFileExists(t, daemon.Path(ws))
}

// TestRestartDaemon_Direct exercises cli.RestartDaemon itself (the shared
// helper `daemon restart` and `sapien upgrade` both call), the same way
// TestMCP_ReplaceStaleDaemon_* exercises cli.ReplaceStaleDaemon directly.
func TestRestartDaemon_Direct(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	oldPID := spawnKillableProcess(t)
	startFakeDaemon(t, ws, cli.Version, oldPID)

	want := &daemon.Info{PID: 424244, Port: 4244, Version: cli.Version}
	fn, calls := fakeSpawnDaemon(t, want)
	defer cli.SetSpawnDaemonFunc(fn)()

	stopped, info, err := cli.RestartDaemon(context.Background(), ws)
	require.NoError(t, err)
	assert.Equal(t, 1, *calls)
	assert.Equal(t, []int{oldPID}, stopped)
	assert.Same(t, want, info)
	assert.Error(t, syscall.Kill(oldPID, 0))
}

func TestRestartDaemon_Direct_NothingRunning(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	want := &daemon.Info{PID: 424245, Port: 4245, Version: cli.Version}
	fn, calls := fakeSpawnDaemon(t, want)
	defer cli.SetSpawnDaemonFunc(fn)()

	stopped, info, err := cli.RestartDaemon(context.Background(), ws)
	require.NoError(t, err)
	assert.Equal(t, 1, *calls)
	assert.Empty(t, stopped)
	assert.Same(t, want, info)
}
