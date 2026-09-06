package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withUserConfig points $SAPIEN_CONFIG at a fresh file for one test.
func withUserConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("SAPIEN_CONFIG", path)
	return path
}

func TestWorkspaceRegistry_AddListRemove(t *testing.T) {
	withUserConfig(t)
	a, b := t.TempDir(), t.TempDir()

	require.NoError(t, AddWorkspace(a))
	require.NoError(t, AddWorkspace(b))
	// Adding twice is a no-op, so callers can register on every open.
	require.NoError(t, AddWorkspace(a))

	got, err := KnownWorkspaces()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{a, b}, got)

	require.NoError(t, RemoveWorkspace(a))
	got, err = KnownWorkspaces()
	require.NoError(t, err)
	assert.Equal(t, []string{b}, got)

	// Removing one that was never registered is not an error.
	require.NoError(t, RemoveWorkspace(filepath.Join(t.TempDir(), "nope")))
}

// The default workspace counts as registered even when nothing added it:
// `sapien mcp config --write` sets it directly, and a picker that omitted it
// would hide the one workspace the user certainly has.
func TestKnownWorkspaces_IncludesDefault(t *testing.T) {
	withUserConfig(t)
	def := t.TempDir()
	require.NoError(t, SetDefaultWorkspace(def))

	got, err := KnownWorkspaces()
	require.NoError(t, err)
	assert.Equal(t, []string{def}, got)
}

// The workspace list shares one file with default_workspace, semantic, git
// and daemon settings, so writing it must not drop any of them.
func TestWorkspaceRegistry_PreservesOtherKeys(t *testing.T) {
	path := withUserConfig(t)
	require.NoError(t, os.WriteFile(path, []byte("semantic:\n  enabled: true\n  model: text-embedding-3-small\ndaemon:\n  idle_timeout: 5m\n"), 0o600))

	dir := t.TempDir()
	require.NoError(t, AddWorkspace(dir))
	require.NoError(t, SetDefaultWorkspace(dir))

	cfg, err := Load(nil)
	require.NoError(t, err)
	assert.True(t, cfg.Semantic.Enabled)
	assert.Equal(t, "text-embedding-3-small", cfg.Semantic.Model)
	assert.Equal(t, "5m", cfg.Daemon.IdleTimeout)

	got, err := KnownWorkspaces()
	require.NoError(t, err)
	assert.Equal(t, []string{dir}, got)
}

func TestKnownWorkspaces_NoFile(t *testing.T) {
	withUserConfig(t)
	got, err := KnownWorkspaces()
	require.NoError(t, err)
	assert.Empty(t, got)
}

// One directory registered under two paths that survive filepath.Clean as
// different strings -- a symlink here, path case on a case-insensitive
// filesystem in the wild -- is one workspace. Registering it twice is what
// put a phantom third entry in the user's picker.
func TestWorkspaceRegistry_SameDirectoryRegistersOnce(t *testing.T) {
	withUserConfig(t)
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))

	require.NoError(t, AddWorkspace(dir))
	require.NoError(t, AddWorkspace(link))

	got, err := KnownWorkspaces()
	require.NoError(t, err)
	require.Len(t, got, 1, "one directory, one entry (got %v)", got)
}

// Forgetting works through either path to the same directory.
func TestWorkspaceRegistry_ForgetMatchesAnyPath(t *testing.T) {
	withUserConfig(t)
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))
	require.NoError(t, AddWorkspace(dir))

	require.NoError(t, RemoveWorkspace(link))

	got, err := KnownWorkspaces()
	require.NoError(t, err)
	assert.Empty(t, got)
}

// A registered directory that no longer exists cannot be compared by
// inode, so it stays listed on its own rather than merging with another.
func TestWorkspaceRegistry_MissingDirectoryStaysListed(t *testing.T) {
	withUserConfig(t)
	live := t.TempDir()
	gone := filepath.Join(t.TempDir(), "deleted")

	require.NoError(t, AddWorkspace(live))
	require.NoError(t, AddWorkspace(gone))

	got, err := KnownWorkspaces()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{live, gone}, got)
}
