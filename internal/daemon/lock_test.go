package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

func lockWS(t *testing.T) *domain.Workspace {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, domain.WorkspaceStateDir), 0o700))
	return &domain.Workspace{Dir: dir}
}

func liveProcess(t *testing.T) int {
	cmd := exec.Command("sleep", "300")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd.Process.Pid
}

func TestLock_AcquireReleaseHolder(t *testing.T) {
	ws := lockWS(t)
	_, ok := Holder(ws)
	assert.False(t, ok, "no lock yet")

	lk, err := Acquire(ws, os.Getpid())
	require.NoError(t, err)
	pid, ok := Holder(ws)
	assert.True(t, ok)
	assert.Equal(t, os.Getpid(), pid)

	require.NoError(t, lk.Release())
	_, ok = Holder(ws)
	assert.False(t, ok)
	assert.NoFileExists(t, LockPath(ws))
}

func TestLock_LiveHolderConflicts(t *testing.T) {
	ws := lockWS(t)
	other := liveProcess(t)
	require.NoError(t, os.WriteFile(LockPath(ws), []byte(strconv.Itoa(other)+"\n"), 0o600))

	_, err := Acquire(ws, os.Getpid())
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Equal(t, other, errs.As(err).Details["pid"])
	assert.Contains(t, errs.As(err).Hint, "serve --restart")
}

func TestLock_StaleHolderIsTakenOver(t *testing.T) {
	ws := lockWS(t)
	require.NoError(t, os.WriteFile(LockPath(ws), []byte("999999999\n"), 0o600))
	lk, err := Acquire(ws, os.Getpid())
	require.NoError(t, err)
	pid, ok := Holder(ws)
	assert.True(t, ok)
	assert.Equal(t, os.Getpid(), pid)
	require.NoError(t, lk.Release())
}

func TestLock_ReleaseLeavesAnotherHoldersFile(t *testing.T) {
	ws := lockWS(t)
	lk, err := Acquire(ws, os.Getpid())
	require.NoError(t, err)
	other := liveProcess(t)
	require.NoError(t, os.WriteFile(LockPath(ws), []byte(strconv.Itoa(other)+"\n"), 0o600))
	require.NoError(t, lk.Release())
	pid, ok := Holder(ws)
	assert.True(t, ok)
	assert.Equal(t, other, pid)
}
