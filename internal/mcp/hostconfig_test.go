package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mcpServersDoc struct {
	MCPServers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcpServers"`
}

func TestHostConfig_ClaudeCode_DefaultsToUserScope(t *testing.T) {
	out, err := HostConfig("claude-code", "sapien", nil, "/ws", "")
	require.NoError(t, err)
	assert.Equal(t, `claude mcp add --scope user sapien -- sapien mcp --workspace /ws`, out)
}

func TestHostConfig_ClaudeCode_Scopes(t *testing.T) {
	for _, scope := range ClaudeScopes {
		out, err := HostConfig("claude-code", "sapien", nil, "/ws", scope)
		require.NoError(t, err)
		assert.Equal(t, "claude mcp add --scope "+scope+" sapien -- sapien mcp --workspace /ws", out)
	}
	_, err := HostConfig("claude-code", "sapien", nil, "/ws", "global")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown claude-code scope")
}

func TestNormalizeClaudeScope(t *testing.T) {
	got, err := NormalizeClaudeScope("")
	require.NoError(t, err)
	assert.Equal(t, DefaultClaudeScope, got)
	got, err = NormalizeClaudeScope("project")
	require.NoError(t, err)
	assert.Equal(t, "project", got)
	_, err = NormalizeClaudeScope("nope")
	assert.Error(t, err)
}

func TestHostConfig_Codex(t *testing.T) {
	out, err := HostConfig("codex", "sapien", nil, "/ws", "")
	require.NoError(t, err)
	assert.Contains(t, out, "[mcp_servers.sapien]")
	assert.Contains(t, out, `command = "sapien"`)
	assert.Contains(t, out, `args = ["mcp", "--workspace", "/ws"]`)
}

func TestHostConfig_JSONClients(t *testing.T) {
	for _, client := range []string{"cursor", "cowork", "generic"} {
		t.Run(client, func(t *testing.T) {
			out, err := HostConfig(client, "sapien", nil, "/ws", "")
			require.NoError(t, err)
			var doc mcpServersDoc
			require.NoError(t, json.Unmarshal([]byte(out), &doc))
			sapien, ok := doc.MCPServers["sapien"]
			require.True(t, ok)
			assert.Equal(t, "sapien", sapien.Command)
			assert.Equal(t, []string{"mcp", "--workspace", "/ws"}, sapien.Args)
		})
	}
}

func TestHostConfig_Generic_ExtraArgsComeFirst(t *testing.T) {
	out, err := HostConfig("generic", "sapien", []string{"--flag"}, "/ws", "")
	require.NoError(t, err)
	var doc mcpServersDoc
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	assert.Equal(t, []string{"--flag", "mcp", "--workspace", "/ws"}, doc.MCPServers["sapien"].Args)
}

func TestHostConfig_UnknownClient(t *testing.T) {
	_, err := HostConfig("bogus", "sapien", nil, "/ws", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cursor")
}

func TestHostConfig_Codex_EscapesTOMLString(t *testing.T) {
	out, err := HostConfig("codex", `sap"ien\bin`, nil, "/ws", "")
	require.NoError(t, err)
	assert.Contains(t, out, `command = "sap\"ien\\bin"`)
}

func TestServerArgs(t *testing.T) {
	assert.Equal(t, []string{"mcp", "--workspace", "/ws"}, ServerArgs(nil, "/ws"))
	assert.Equal(t, []string{"-v", "mcp", "--workspace", "/ws"}, ServerArgs([]string{"-v"}, "/ws"))
}

func TestMergeMCPServersFile_CreatesFileAndDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cursor", "mcp.json")
	require.NoError(t, MergeMCPServersFile(path, "/bin/sapien", ServerArgs(nil, "/ws")))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc mcpServersDoc
	require.NoError(t, json.Unmarshal(data, &doc))
	assert.Equal(t, "/bin/sapien", doc.MCPServers["sapien"].Command)
	assert.Equal(t, []string{"mcp", "--workspace", "/ws"}, doc.MCPServers["sapien"].Args)
	assert.Equal(t, byte('\n'), data[len(data)-1], "file ends with a newline")
}

func TestMergeMCPServersFile_PreservesOtherEntriesAndReplacesSapien(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
  "mcpServers": {
    "other": {"command": "other-bin", "args": ["x"]},
    "sapien": {"command": "/old/sapien", "args": ["mcp", "--workspace", "/old"]}
  },
  "theme": "dark"
}`), 0o644))

	require.NoError(t, MergeMCPServersFile(path, "/new/sapien", ServerArgs(nil, "/new")))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "dark", raw["theme"], "unrelated top-level keys survive")

	var doc mcpServersDoc
	require.NoError(t, json.Unmarshal(data, &doc))
	assert.Equal(t, "other-bin", doc.MCPServers["other"].Command)
	assert.Equal(t, "/new/sapien", doc.MCPServers["sapien"].Command)
	assert.Equal(t, []string{"mcp", "--workspace", "/new"}, doc.MCPServers["sapien"].Args)
}

func TestMergeMCPServersFile_EmptyFileIsFine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	require.NoError(t, os.WriteFile(path, []byte("  \n"), 0o644))
	require.NoError(t, MergeMCPServersFile(path, "sapien", ServerArgs(nil, "/ws")))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"sapien"`)
}

func TestMergeMCPServersFile_InvalidJSONIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	err := MergeMCPServersFile(path, "sapien", ServerArgs(nil, "/ws"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing")
}

func TestClaudeDesktopConfigPath(t *testing.T) {
	assert.Equal(t, filepath.Join("/Users/x", "Library", "Application Support", "Claude", "claude_desktop_config.json"),
		ClaudeDesktopConfigPath("darwin", "/Users/x", ""))
	assert.Equal(t, filepath.Join("/home/x", ".config", "Claude", "claude_desktop_config.json"),
		ClaudeDesktopConfigPath("linux", "/home/x", ""))
	assert.Equal(t, filepath.Join(`C:\Users\x\AppData\Roaming`, "Claude", "claude_desktop_config.json"),
		ClaudeDesktopConfigPath("windows", `C:\Users\x`, `C:\Users\x\AppData\Roaming`))
	assert.Equal(t, filepath.Join(`C:\Users\x`, "AppData", "Roaming", "Claude", "claude_desktop_config.json"),
		ClaudeDesktopConfigPath("windows", `C:\Users\x`, ""))
}
