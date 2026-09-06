package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPermissions(t *testing.T) {
	p := DefaultPermissions()
	assert.True(t, p.ReadContracts)
	assert.True(t, p.ReadMemories)
	assert.True(t, p.WriteMemories)
	assert.True(t, p.ReadFlows)
	assert.True(t, p.WriteFlows)
	assert.True(t, p.WriteServices)
	assert.True(t, p.WriteExamples)
	assert.True(t, p.ReadRuns)
	assert.True(t, p.ExecuteRead)
	assert.False(t, p.ExecuteMutation)
	assert.False(t, p.AllowProduction)
	assert.Empty(t, p.Environments)
}

func TestConfigFor(t *testing.T) {
	cfg := Config{
		Default: DefaultPermissions(),
		Clients: map[string]Permissions{
			"claude-code": {ExecuteMutation: true},
		},
	}
	assert.Equal(t, Permissions{ExecuteMutation: true}, cfg.For("claude-code"))
	assert.Equal(t, DefaultPermissions(), cfg.For("unknown-client"))
}

func TestLoadConfig_MissingFilesIgnored(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.NoError(t, err)
	assert.Equal(t, DefaultPermissions(), cfg.Default)
	assert.Empty(t, cfg.Clients)
}

func TestLoadConfig_TopLevelDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
default:
  allow_production: false
clients:
  claude-code:
    execute_mutation: true
    environments: ["staging", "qa"]
  codex:
    write_flows: false
`), 0o644))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	claude := cfg.For("claude-code")
	assert.True(t, claude.ExecuteMutation)
	assert.Equal(t, []string{"staging", "qa"}, claude.Environments)
	// Fields not mentioned inherit the baseline default profile.
	assert.True(t, claude.ReadContracts)
	assert.True(t, claude.WriteFlows)

	codex := cfg.For("codex")
	assert.False(t, codex.WriteFlows)
	assert.True(t, codex.ReadContracts) // untouched field keeps the default

	assert.Equal(t, DefaultPermissions(), cfg.For("some-other-client"))
}

func TestLoadConfig_MCPKeyDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
some_unrelated_setting: true
mcp:
  default:
    allow_production: true
  clients:
    cowork:
      read_runs: false
`), 0o644))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Default.AllowProduction)
	assert.False(t, cfg.For("cowork").ReadRuns)
}

func TestLoadConfig_MergeOrder(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "mcp.yaml")
	home := filepath.Join(dir, "config.yaml")

	require.NoError(t, os.WriteFile(ws, []byte(`
clients:
  claude-code:
    execute_mutation: true
    allow_production: true
`), 0o644))
	// The home/global file is loaded second and acts as a safety ceiling:
	// it can restrict what a workspace file granted.
	require.NoError(t, os.WriteFile(home, []byte(`
mcp:
  clients:
    claude-code:
      allow_production: false
`), 0o644))

	cfg, err := LoadConfig(ws, home)
	require.NoError(t, err)
	p := cfg.For("claude-code")
	assert.True(t, p.ExecuteMutation)  // from the workspace file, untouched by home
	assert.False(t, p.AllowProduction) // home file overrides the workspace grant
}

func TestLoadConfig_WriteServicesOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
default:
  write_services: false
clients:
  claude-code:
    write_services: true
`), 0o644))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.False(t, cfg.Default.WriteServices)
	assert.True(t, cfg.For("claude-code").WriteServices)
	assert.False(t, cfg.For("codex").WriteServices, "inherits the default")
	assert.True(t, cfg.Default.allows(classWriteFlows), "unrelated classes untouched")
	assert.False(t, cfg.Default.allows(classWriteServices))
}

func TestLoadConfig_WriteExamplesOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
default:
  write_examples: false
clients:
  claude-code:
    write_examples: true
`), 0o644))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.False(t, cfg.Default.WriteExamples)
	assert.True(t, cfg.For("claude-code").WriteExamples)
	assert.False(t, cfg.For("codex").WriteExamples, "inherits the default")
	assert.True(t, cfg.Default.allows(classWriteFlows), "unrelated classes untouched")
	assert.False(t, cfg.Default.allows(classWriteExamples))
}
