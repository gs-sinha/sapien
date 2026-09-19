package workspaces

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// newWorkspaceDir writes a minimal workspace file and returns its directory.
func newWorkspaceDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	body := "version: 1\nname: " + name + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, domain.WorkspaceFileName), []byte(body), 0o644))
	return dir
}

// newRegisteredWorkspaceDir is newWorkspaceDir plus registration in the
// (test-scoped) user config: what a workspace the picker offers looks like,
// and what Engine requires before it opens one on a request's say-so.
func newRegisteredWorkspaceDir(t *testing.T, name string) string {
	t.Helper()
	dir := newWorkspaceDir(t, name)
	require.NoError(t, config.AddWorkspace(dir))
	return dir
}

// fakeOpener stands in for local.Open so the manager can be exercised
// without a database, and records what it was asked to open.
func fakeOpener(opened *[]string) func(*domain.Workspace, local.Options) (engine.Engine, error) {
	return func(ws *domain.Workspace, _ local.Options) (engine.Engine, error) {
		*opened = append(*opened, ws.Dir)
		return enginetest.New(ws), nil
	}
}

func newManager(t *testing.T, primaryDir string, opened *[]string) (*Manager, *domain.Workspace) {
	t.Helper()
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	primary, err := workspace.Load(filepath.Join(primaryDir, domain.WorkspaceFileName))
	require.NoError(t, err)

	m := New(primary, enginetest.New(primary), Options{PID: os.Getpid(), Open: fakeOpener(opened)})
	t.Cleanup(func() { _ = m.Close() })
	return m, primary
}

// The primary is served without opening anything: `sapien serve` already
// opened it and owns its lock.
func TestManager_PrimaryIsAdopted(t *testing.T) {
	var opened []string
	m, primary := newManager(t, newWorkspaceDir(t, "primary"), &opened)

	eng, err := m.Engine("")
	require.NoError(t, err)
	assert.Equal(t, primary.Dir, eng.Workspace().Dir)

	// By directory, too.
	eng, err = m.Engine(primary.Dir)
	require.NoError(t, err)
	assert.Equal(t, primary.Dir, eng.Workspace().Dir)

	assert.Empty(t, opened, "the primary must never be reopened")
}

// A second workspace is opened lazily, exactly once, and its lock is taken.
func TestManager_OpensSecondWorkspaceOnceAndLocksIt(t *testing.T) {
	var opened []string
	m, _ := newManager(t, newWorkspaceDir(t, "primary"), &opened)
	otherDir := newRegisteredWorkspaceDir(t, "other")

	eng, err := m.Engine(otherDir)
	require.NoError(t, err)
	assert.Equal(t, "other", eng.Workspace().Name)

	again, err := m.Engine(otherDir)
	require.NoError(t, err)
	assert.Same(t, eng, again, "a second request must reuse the open engine")
	assert.Equal(t, []string{otherDir}, opened)

	other, err := workspace.Load(filepath.Join(otherDir, domain.WorkspaceFileName))
	require.NoError(t, err)
	assert.FileExists(t, daemon.LockPath(other), "opening a workspace claims its lock")
}

// A workspace another live process is serving is refused rather than opened
// twice -- the 2026-09-06 two-daemon failure, made structural.
func TestManager_RefusesWorkspaceLockedElsewhere(t *testing.T) {
	var opened []string
	m, _ := newManager(t, newWorkspaceDir(t, "primary"), &opened)
	otherDir := newRegisteredWorkspaceDir(t, "other")

	other, err := workspace.Load(filepath.Join(otherDir, domain.WorkspaceFileName))
	require.NoError(t, err)

	// A lock is only honoured while its holder is alive (daemon.Holder), so
	// this needs a real live process that is not this one: a stale pid would
	// legitimately be taken over and prove nothing.
	holder := exec.Command("sleep", "60")
	require.NoError(t, holder.Start())
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	})
	held, err := daemon.Acquire(other, holder.Process.Pid)
	require.NoError(t, err)
	defer func() { _ = held.Release() }()

	_, err = m.Engine(otherDir)
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Empty(t, opened)
}

func TestManager_EngineRejectsMissingWorkspace(t *testing.T) {
	var opened []string
	m, _ := newManager(t, newWorkspaceDir(t, "primary"), &opened)

	_, err := m.Engine(filepath.Join(t.TempDir(), "not-a-workspace"))
	require.Error(t, err)
	assert.Equal(t, errs.WorkspaceNotFound, errs.CodeOf(err))
}

// List reports open workspaces and registered-but-unopened ones alike, so
// the picker can offer a workspace this daemon has never touched.
func TestManager_ListsOpenAndRegistered(t *testing.T) {
	var opened []string
	m, primary := newManager(t, newWorkspaceDir(t, "primary"), &opened)
	otherDir := newRegisteredWorkspaceDir(t, "other")

	_, err := m.Engine(otherDir)
	require.NoError(t, err)

	byDir := map[string]Info{}
	for _, info := range m.List() {
		byDir[info.Dir] = info
	}

	require.Contains(t, byDir, primary.Dir)
	assert.True(t, byDir[primary.Dir].Primary)
	assert.True(t, byDir[primary.Dir].Open)

	require.Contains(t, byDir, otherDir)
	assert.Equal(t, "other", byDir[otherDir].Name)
	assert.False(t, byDir[otherDir].Primary)
	assert.True(t, byDir[otherDir].Open)
}

// A registered workspace that no longer loads is listed with its error
// rather than dropped: vanishing silently from the picker gives the user
// nothing to act on.
func TestManager_ListsUnloadableWorkspaceWithError(t *testing.T) {
	var opened []string
	m, _ := newManager(t, newWorkspaceDir(t, "primary"), &opened)

	goneDir := newRegisteredWorkspaceDir(t, "gone")
	_, err := m.Engine(goneDir)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(goneDir, domain.WorkspaceFileName)))

	var found *Info
	for _, info := range m.List() {
		if info.Dir == goneDir {
			i := info
			found = &i
		}
	}
	require.NotNil(t, found)
	assert.NotEmpty(t, found.Error)
	assert.Equal(t, filepath.Base(goneDir), found.Name)
}

// Close releases the locks of workspaces the manager opened and leaves the
// primary's engine to `serve`, which owns it.
func TestManager_CloseReleasesOpenedLocksOnly(t *testing.T) {
	var opened []string
	primaryDir := newWorkspaceDir(t, "primary")
	m, primary := newManager(t, primaryDir, &opened)
	otherDir := newRegisteredWorkspaceDir(t, "other")

	_, err := m.Engine(otherDir)
	require.NoError(t, err)
	other, err := workspace.Load(filepath.Join(otherDir, domain.WorkspaceFileName))
	require.NoError(t, err)

	require.NoError(t, m.Close())

	assert.NoFileExists(t, daemon.LockPath(other), "an opened workspace's lock is released")
	assert.NoFileExists(t, daemon.LockPath(primary), "serve owns the primary's lock, not the manager")
}

// A directory reached by a second spelling of its path is the same
// workspace. On a case-insensitive filesystem the daemon's primary can be
// "~/desktop/ws" while the registry holds "~/Desktop/ws"; keying by the
// string opened one directory twice -- two engines, two database handles,
// two watchers -- and showed a phantom extra workspace in every picker.
func TestManager_SameDirectoryUnderAnotherSpellingIsOneWorkspace(t *testing.T) {
	var opened []string
	primaryDir := newWorkspaceDir(t, "primary")
	m, primary := newManager(t, primaryDir, &opened)

	// A symlink reaches the same directory by a path filepath.Clean cannot
	// collapse -- the same shape as path case on a case-insensitive
	// filesystem, which is how this actually happened.
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(primary.Dir, link))

	eng, err := m.Engine(link)
	require.NoError(t, err)
	assert.Equal(t, primary.Dir, eng.Workspace().Dir)

	assert.Empty(t, opened, "the primary must not be reopened under another spelling")

	// And it is listed once, as the primary.
	var count int
	for _, info := range m.List() {
		if workspace.SameDir(info.Dir, primary.Dir) {
			count++
			assert.True(t, info.Primary)
		}
	}
	assert.Equal(t, 1, count, "one directory must appear once in the listing")
}

// The same guarantee for a workspace the manager opened itself, not just
// the adopted primary.
func TestManager_SecondSpellingReusesAnOpenedWorkspace(t *testing.T) {
	var opened []string
	m, _ := newManager(t, newWorkspaceDir(t, "primary"), &opened)
	otherDir := newRegisteredWorkspaceDir(t, "other")

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(otherDir, link))

	first, err := m.Engine(otherDir)
	require.NoError(t, err)
	second, err := m.Engine(link)
	require.NoError(t, err)

	assert.Same(t, first, second)
	assert.Equal(t, []string{otherDir}, opened, "opened once, not once per path")
}

// A request header is not an invitation: a workspace that is neither the
// primary nor registered is refused, so a stale client cannot reopen a
// workspace the user forgot. Register is the explicit way in, and it
// records the directory in the user config.
func TestManager_UnregisteredIsRefusedUntilRegistered(t *testing.T) {
	var opened []string
	m, _ := newManager(t, newWorkspaceDir(t, "primary"), &opened)
	otherDir := newWorkspaceDir(t, "other")

	_, err := m.Engine(otherDir)
	require.Error(t, err)
	assert.Equal(t, errs.WorkspaceNotFound, errs.CodeOf(err))
	assert.Empty(t, opened)

	eng, err := m.Register(otherDir)
	require.NoError(t, err)
	assert.Equal(t, "other", eng.Workspace().Name)
	assert.Len(t, opened, 1)

	known, err := config.KnownWorkspaces()
	require.NoError(t, err)
	assert.Contains(t, known, otherDir)

	// Registered: the implicit path now works too.
	_, err = m.Engine(otherDir)
	require.NoError(t, err)
}

// CloseOne releases the workspace's lock and drops its engine; the primary
// is never closed this way; a still-registered workspace reopens on the
// next implicit request, a forgotten one does not.
func TestManager_CloseOne(t *testing.T) {
	var opened []string
	m, primary := newManager(t, newWorkspaceDir(t, "primary"), &opened)
	otherDir := newWorkspaceDir(t, "other")
	require.NoError(t, config.AddWorkspace(otherDir))

	_, err := m.Engine(otherDir)
	require.NoError(t, err)
	require.Len(t, opened, 1)
	lock := filepath.Join(otherDir, domain.WorkspaceStateDir, "daemon.lock")
	assert.FileExists(t, lock)

	require.NoError(t, m.CloseOne(otherDir))
	assert.NoFileExists(t, lock, "closing releases the lock")
	for _, info := range m.List() {
		if info.Dir == otherDir {
			assert.False(t, info.Open)
		}
	}

	err = m.CloseOne(primary.Dir)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	// Still registered: reopened on demand.
	_, err = m.Engine(otherDir)
	require.NoError(t, err)
	assert.Len(t, opened, 2)

	// Forgotten and closed: stays closed.
	require.NoError(t, config.RemoveWorkspace(otherDir))
	require.NoError(t, m.CloseOne(otherDir))
	_, err = m.Engine(otherDir)
	require.Error(t, err)
	assert.Equal(t, errs.WorkspaceNotFound, errs.CodeOf(err))
	assert.Len(t, opened, 2)
}

// TestManager_Engines proves Engines() reports exactly the currently-open
// set -- the primary alone at first, then also a second workspace once
// something has opened it, and no longer once it is closed again -- which
// is what GET /v1/daemon's active_runs (PLAN §34f item 3) sums across.
func TestManager_Engines(t *testing.T) {
	var opened []string
	m, primary := newManager(t, newWorkspaceDir(t, "primary"), &opened)

	engines := m.Engines()
	require.Len(t, engines, 1)
	assert.Equal(t, primary.Dir, engines[0].Workspace().Dir)

	otherDir := newRegisteredWorkspaceDir(t, "other")
	_, err := m.Engine(otherDir)
	require.NoError(t, err)

	engines = m.Engines()
	require.Len(t, engines, 2)
	dirs := []string{engines[0].Workspace().Dir, engines[1].Workspace().Dir}
	assert.ElementsMatch(t, []string{primary.Dir, otherDir}, dirs)

	require.NoError(t, m.CloseOne(otherDir))
	engines = m.Engines()
	require.Len(t, engines, 1)
	assert.Equal(t, primary.Dir, engines[0].Workspace().Dir)
}
