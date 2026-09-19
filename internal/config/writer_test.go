package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
)

func TestSetUpdatesCheck_CreatesFileWhenMissing(t *testing.T) {
	path := isolateUserConfig(t, "")
	os.Remove(path) // isolateUserConfig with "" never writes it in the first place, but be explicit

	require.NoError(t, config.SetUpdatesCheck(false))

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.False(t, cfg.Updates.Enabled())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "check: false")

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

func TestSetUpdatesCheck_PreservesOtherKeysAndComments(t *testing.T) {
	path := isolateUserConfig(t, `
# a hand-written comment that must survive
semantic:
  enabled: true
  kind: ollama

mcp:
  allow_mutations: true

friction:
  repo: acme/internal-tools
`)

	require.NoError(t, config.SetUpdatesCheck(true))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	out := string(data)

	assert.Contains(t, out, "a hand-written comment that must survive")
	assert.Contains(t, out, "kind: ollama")
	assert.Contains(t, out, "allow_mutations: true")
	assert.Contains(t, out, "repo: acme/internal-tools")
	assert.Contains(t, out, "check: true")

	// The typed loader (which doesn't know mcp: at all -- see the package
	// doc) still round-trips the fields it does know.
	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.True(t, cfg.Semantic.Enabled)
	assert.Equal(t, "ollama", cfg.Semantic.Kind)
	assert.Equal(t, "acme/internal-tools", cfg.Friction.Repo)
	assert.True(t, cfg.Updates.Enabled())
}

func TestSetUpdatesCheck_OverwritesPreviousValue(t *testing.T) {
	isolateUserConfig(t, `
updates:
  check: true
`)

	require.NoError(t, config.SetUpdatesCheck(false))

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.False(t, cfg.Updates.Enabled())
}

func TestSetUpdatesCheck_OverwritesNonMappingUpdatesKey(t *testing.T) {
	// A hand-edited or older config might have `updates:` as something
	// other than a mapping; the write must still succeed and take over.
	isolateUserConfig(t, "updates: not-a-mapping\n")

	require.NoError(t, config.SetUpdatesCheck(false))

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.False(t, cfg.Updates.Enabled())
}

func TestSetUpdatesCheck_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested", "config.yaml")
	t.Setenv("SAPIEN_CONFIG", nested)

	require.NoError(t, config.SetUpdatesCheck(true))
	assert.FileExists(t, nested)
}
