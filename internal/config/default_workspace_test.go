package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultWorkspace_AbsentFileOrKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("SAPIEN_CONFIG", path)

	got, err := DefaultWorkspace()
	require.NoError(t, err)
	assert.Equal(t, "", got)

	require.NoError(t, os.WriteFile(path, []byte("semantic:\n  enabled: false\n"), 0o600))
	got, err = DefaultWorkspace()
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestSetDefaultWorkspace_CreatesFilePreservesKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	t.Setenv("SAPIEN_CONFIG", path)

	ws := t.TempDir()
	require.NoError(t, SetDefaultWorkspace(ws))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, err := DefaultWorkspace()
	require.NoError(t, err)
	assert.Equal(t, ws, got)

	// Other keys survive a rewrite, and Load still parses the file.
	require.NoError(t, os.WriteFile(path, []byte("daemon:\n  idle_timeout: 5m\ndefault_workspace: /old\nmcp:\n  default:\n    execute_mutation: true\n"), 0o600))
	ws2 := t.TempDir()
	require.NoError(t, SetDefaultWorkspace(ws2))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "idle_timeout: 5m")
	assert.Contains(t, string(data), "execute_mutation: true")
	assert.NotContains(t, string(data), "/old")
	got, err = DefaultWorkspace()
	require.NoError(t, err)
	assert.Equal(t, ws2, got)
	cfg, err := Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "5m", cfg.Daemon.IdleTimeout)
}

func TestDefaultWorkspace_ExpandsHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("SAPIEN_CONFIG", path)
	require.NoError(t, os.WriteFile(path, []byte("default_workspace: ~/ws\n"), 0o600))
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	got, err := DefaultWorkspace()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "ws"), got)
}

func TestDefaultWorkspace_InvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("SAPIEN_CONFIG", path)
	require.NoError(t, os.WriteFile(path, []byte("default_workspace: [unclosed\n"), 0o600))
	_, err := DefaultWorkspace()
	assert.Error(t, err)
}
