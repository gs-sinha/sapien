package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/mcp"
)

// --- service add: a relative path means "relative to where I am", not
// "relative to the workspace" (the engine's convention for stored paths).

func TestServiceAdd_RelativePath_ResolvedAgainstCwd(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "service", "add", "./my-service", "--name", "rel", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	src := lastCall(fake, "Services.Add").Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceLocal, src.Kind)
	assert.Equal(t, filepath.Join(cwd, "my-service"), src.Path)
}

func TestServiceAdd_AbsoluteAndHomePathsPassThrough(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	for _, p := range []string{"/abs/svc", "~/code/svc", "~"} {
		_, stderr, code := run(t, "--workspace", dir, "service", "add", p, "--name", "n-"+strings.Trim(p, "/~"), "--json")
		require.Equal(t, 0, code, "stderr: %s", stderr)
		src := lastCall(fake, "Services.Add").Args.(map[string]any)["source"].(domain.Source)
		assert.Equal(t, p, src.Path)
	}
}

// --- mcp config: --scope and the cursor client ---

func TestMCPConfig_Scope(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "claude-code", "--scope", "local")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "claude mcp add --scope local sapien --")

	_, stderr, code = run(t, "--workspace", wsDir, "mcp", "config", "--client", "claude-code", "--scope", "global")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "unknown claude-code scope")
}

func TestMCPConfigWrite_Cursor(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".cursor", "mcp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcpServers":{"other":{"command":"o"}}}`), 0o644))

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "cursor", "--write")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "installed sapien in "+cfgPath)

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var doc struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	assert.Equal(t, "o", doc.MCPServers["other"].Command, "existing entries survive")
	sapien := doc.MCPServers["sapien"]
	assert.True(t, filepath.IsAbs(sapien.Command), "command is the absolute path of the running binary: %q", sapien.Command)
	assert.Equal(t, []string{"mcp", "--workspace", wsDir}, sapien.Args)
}

func TestMCPConfigWrite_Cursor_CreatesFile(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	home := t.TempDir()
	t.Setenv("HOME", home)
	_, stderr, code = run(t, "--workspace", wsDir, "mcp", "config", "--client", "cursor", "--write")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	_, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json"))
	assert.NoError(t, err)
}

// fakeClaude installs a `claude` shell script on PATH that logs every
// invocation to logPath. Its first `mcp add` fails with Claude Code's
// "already exists" message; every later one succeeds, as does `mcp remove`.
func fakeClaude(t *testing.T, logPath string) {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/bin/sh
echo "$@" >> "$CLAUDE_FAKE_LOG"
if [ "$1" = "mcp" ] && [ "$2" = "add" ]; then
  if [ ! -f "$CLAUDE_FAKE_LOG.first-add-done" ]; then
    : > "$CLAUDE_FAKE_LOG.first-add-done"
    echo "MCP server sapien already exists in user config"
    exit 1
  fi
  echo "Added stdio MCP server sapien"
  exit 0
fi
if [ "$1" = "mcp" ] && [ "$2" = "remove" ]; then
  echo "Removed MCP server sapien"
  exit 0
fi
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755))
	t.Setenv("PATH", binDir)
	t.Setenv("CLAUDE_FAKE_LOG", logPath)
}

func TestMCPConfigWrite_ClaudeCode_ReplacesExistingEntry(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	logPath := filepath.Join(t.TempDir(), "claude.log")
	fakeClaude(t, logPath)

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "claude-code", "--write")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "installed sapien for claude-code (scope user, workspace "+wsDir)
	assert.Contains(t, stdout, "Added stdio MCP server sapien")

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 3, "add, remove, add: %q", lines)
	assert.True(t, strings.HasPrefix(lines[0], "mcp add --scope user sapien -- "), lines[0])
	assert.Equal(t, "mcp remove --scope user sapien", lines[1])
	assert.True(t, strings.HasPrefix(lines[2], "mcp add --scope user sapien -- "), lines[2])
	assert.True(t, strings.HasSuffix(lines[2], " mcp --workspace "+wsDir), lines[2])
}

func TestMCPConfigWrite_ClaudeCode_ProjectScope(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	logPath := filepath.Join(t.TempDir(), "claude.log")
	fakeClaude(t, logPath)
	// Pre-mark the first add as done so this run's single add succeeds.
	require.NoError(t, os.WriteFile(logPath+".first-add-done", nil, 0o644))

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "claude-code", "--write", "--scope", "project")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "scope project")

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "mcp add --scope project sapien -- "))
	assert.NotContains(t, string(data), "mcp remove")
}

func TestServiceSync_NamesMissingEnvironments(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	// The seeded services declare only "local"; remove the file init created
	// so that environment is declared but not defined.
	require.NoError(t, os.Remove(filepath.Join(dir, "environments", "local.yaml")))

	stdout, stderr, code := run(t, "--workspace", dir, "service", "sync", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "environments declared by order-service but not defined in this workspace: local")
	assert.Contains(t, stdout, "sapien env scaffold")
}

func TestServiceSync_NoHintWhenEnvironmentsExist(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "service", "sync", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "not defined in this workspace")
}

func TestMCPConfigWrite_Cowork_MergesIntoClaudeDesktopConfig(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", "")
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	cfgPath := mcp.ClaudeDesktopConfigPath(runtime.GOOS, home, "")
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcpServers":{"filesystem":{"command":"fs"}},"theme":"dark"}`), 0o644))

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "cowork", "--write")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "installed sapien in "+cfgPath)
	assert.Contains(t, stdout, "restart Claude Desktop")

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	assert.Equal(t, "dark", doc["theme"])
	servers := doc["mcpServers"].(map[string]any)
	assert.Contains(t, servers, "filesystem")
	sapien := servers["sapien"].(map[string]any)
	assert.True(t, filepath.IsAbs(sapien["command"].(string)))
	assert.Equal(t, []any{"mcp", "--workspace", wsDir}, sapien["args"])
}
