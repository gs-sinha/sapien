package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/mcp"
)

// --- mcp config --allow-mutations ---

func TestMCPConfig_AllowMutations_Alone(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(wsDir, ".sapien", "mcp.yaml")
	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--allow-mutations")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "allowed agents to make POST, PUT, PATCH and DELETE calls")
	assert.Contains(t, stdout, path)
	assert.NotContains(t, stdout, "mcpServers", "no host entry without --client")

	cfg, err := mcp.LoadConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Default.ExecuteMutation)
	assert.False(t, cfg.Default.AllowProduction)

	stdout, _, code = run(t, "--workspace", wsDir, "mcp", "config", "--allow-mutations")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "agents may already make")
}

func TestMCPConfig_AllowMutations_WithClient(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "generic", "--write", "--allow-mutations")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, `"mcpServers"`)
	assert.Contains(t, stdout, "allowed agents to make")

	cfg, err := mcp.LoadConfig(filepath.Join(wsDir, ".sapien", "mcp.yaml"))
	require.NoError(t, err)
	assert.True(t, cfg.Default.ExecuteMutation)
}

func TestMCPConfig_AllowMutations_JSONKeepsStdoutClean(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "claude-code", "--allow-mutations", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, "claude-code", got["client"])
	assert.Contains(t, stderr, "allowed agents to make")
}

func TestMCPConfig_AllowMutations_WarnsWhenStillDenied(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	home := t.TempDir()
	t.Setenv("HOME", home)
	userCfg := filepath.Join(home, ".sapien", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(userCfg), 0o755))
	require.NoError(t, os.WriteFile(userCfg, []byte("mcp:\n  default:\n    execute_mutation: false\n  clients:\n    codex:\n      execute_mutation: false\n"), 0o644))

	_, stderr, code = run(t, "--workspace", wsDir, "mcp", "config", "--allow-mutations")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stderr, userCfg+" sets mcp.default.execute_mutation: false")
	assert.Contains(t, stderr, "clients.codex sets execute_mutation: false")
}

func TestMCPConfig_ClientRequiredWithoutAllowMutations(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	_, stderr, code = run(t, "--workspace", wsDir, "mcp", "config")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, `required flag "client" not set`)
}
