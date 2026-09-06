package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/cli"
	"github.com/growsimplee/sapien/internal/daemon"
	"github.com/growsimplee/sapien/internal/errs"
)

// A live holder of the workspace lock that daemon.json no longer names (an
// orphan from an earlier replacement) blocks a plain serve and is stopped
// by serve --restart.
func TestServe_WorkspaceLock_OrphanBlocksThenRestartStopsIt(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	orphan := spawnKillableProcess(t)
	require.NoError(t, os.WriteFile(daemon.LockPath(ws), []byte(strconv.Itoa(orphan)+"\n"), 0o600))

	_, err := cli.AcquireWorkspaceLock(ws, false)
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))

	lk, err := cli.AcquireWorkspaceLock(ws, true)
	require.NoError(t, err)
	defer lk.Release()
	assert.Error(t, syscall.Kill(orphan, 0), "the orphan was stopped")
	pid, ok := daemon.Holder(ws)
	assert.True(t, ok)
	assert.Equal(t, os.Getpid(), pid)
}

// Replacing a daemon from another build also stops a different process
// that holds the workspace lock, so two daemons never index one workspace.
func TestMCP_ReplaceStaleDaemon_StopsLockHolderToo(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	named := spawnKillableProcess(t)
	orphan := spawnKillableProcess(t)
	startFakeDaemon(t, ws, "not-"+cli.Version, named)
	require.NoError(t, os.WriteFile(daemon.LockPath(ws), []byte(strconv.Itoa(orphan)+"\n"), 0o600))

	require.NoError(t, cli.ReplaceStaleDaemon(context.Background(), ws, cli.Version))
	assert.Error(t, syscall.Kill(named, 0), "daemon.json's process stopped")
	assert.Error(t, syscall.Kill(orphan, 0), "lock holder stopped")
	assert.NoFileExists(t, filepath.Join(wsDir, ".sapien", "daemon.json"))
}
