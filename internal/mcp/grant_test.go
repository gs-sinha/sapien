package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllowMutations_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sapien", "mcp.yaml")

	changed, err := AllowMutations(path)
	require.NoError(t, err)
	assert.True(t, changed)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Default.ExecuteMutation)
	assert.False(t, cfg.Default.AllowProduction, "production stays behind its own grant")
	assert.Empty(t, cfg.Default.Environments)

	changed, err = AllowMutations(path)
	require.NoError(t, err)
	assert.False(t, changed, "a second grant leaves the file alone")
}

func TestAllowMutations_EditsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`# kept
default:
  write_flows: false   # a research-only setup
  execute_mutation: false
clients:
  codex:
    environments: [local]
`), 0o644))

	changed, err := AllowMutations(path)
	require.NoError(t, err)
	assert.True(t, changed)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "# kept")
	assert.Contains(t, string(data), "# a research-only setup")

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Default.ExecuteMutation)
	assert.False(t, cfg.Default.WriteFlows, "other default fields survive")
	assert.True(t, cfg.For("codex").ExecuteMutation, "a client without its own setting inherits the grant")
	assert.Equal(t, []string{"local"}, cfg.For("codex").Environments)
}

func TestAllowMutations_AddsMissingDefault(t *testing.T) {
	for name, body := range map[string]string{
		"no default key": "clients:\n  codex:\n    write_flows: false\n",
		"empty default":  "default:\nclients:\n  codex:\n    write_flows: false\n",
		"nested mcp key": "daemon:\n  idle_timeout: 5m\nmcp:\n  clients:\n    codex:\n      write_flows: false\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.yaml")
			require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

			changed, err := AllowMutations(path)
			require.NoError(t, err)
			assert.True(t, changed)

			cfg, err := LoadConfig(path)
			require.NoError(t, err)
			assert.True(t, cfg.Default.ExecuteMutation)
			assert.False(t, cfg.For("codex").WriteFlows)
		})
	}
}

func TestAllowMutations_RefusesNonMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	require.NoError(t, os.WriteFile(path, []byte("default: yes please\n"), 0o644))

	_, err := AllowMutations(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a mapping")
}
