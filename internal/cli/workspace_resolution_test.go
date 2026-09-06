package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test process runs with cwd = this package's directory, which has no
// sapien.workspace.yaml above it, so "nothing discoverable" is the natural
// state here; SAPIEN_CONFIG is pointed at a temp file so a developer's real
// default_workspace never leaks into these assertions.

func TestWorkspace_EnvVarStandsInForFlag(t *testing.T) {
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	t.Setenv("SAPIEN_WORKSPACE", wsDir)
	stdout, stderr, code := run(t, "env", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, `"local"`)
}

func TestWorkspace_DefaultWorkspaceFallback(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("SAPIEN_CONFIG", cfgPath)
	t.Setenv("SAPIEN_WORKSPACE", "")
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	require.NoError(t, os.WriteFile(cfgPath, []byte("default_workspace: "+wsDir+"\n"), 0o600))

	stdout, stderr, code := run(t, "env", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, `"local"`)
}

func TestWorkspace_NotFoundHintNamesEveryOption(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("SAPIEN_CONFIG", cfgPath)
	t.Setenv("SAPIEN_WORKSPACE", "")

	_, stderr, code := run(t, "env", "list", "--json")
	require.NotEqual(t, 0, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_WORKSPACE_NOT_FOUND", got["code"])
	hint, _ := got["hint"].(string)
	assert.Contains(t, hint, "--workspace")
	assert.Contains(t, hint, "SAPIEN_WORKSPACE")
	assert.Contains(t, hint, "default_workspace")
	assert.Contains(t, hint, cfgPath)
}

func TestInit_PrintsGitAndMCPTips(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := run(t, "init", dir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "git init")
	assert.Contains(t, stdout, "sapien mcp config --client claude-code --write")

	// A workspace created inside a git repo gets no git tip.
	dir2 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir2, ".git"), 0o755))
	stdout, stderr, code = run(t, "init", dir2)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "git init")
}

func TestMCPConfigWrite_RecordsDefaultWorkspaceOnce(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("SAPIEN_CONFIG", cfgPath)
	t.Setenv("SAPIEN_WORKSPACE", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// cursor --write always installs (it creates ~/.cursor/mcp.json), so it
	// is the simplest install path to exercise the recording with.
	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "cursor", "--write")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "recorded "+wsDir+" as default_workspace")
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "default_workspace: "+wsDir)

	// From an unrelated directory, with no flag, the CLI now finds it.
	stdout, stderr, code = run(t, "env", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, `"local"`)

	// A second workspace does not steal the default.
	ws2 := t.TempDir()
	_, stderr, code = run(t, "init", ws2)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	stdout, stderr, code = run(t, "--workspace", ws2, "mcp", "config", "--client", "cursor", "--write")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "recorded ")
	data, err = os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "default_workspace: "+wsDir)
}

// `workspace list` merges the registry with the workspace you are standing
// in. Reaching that same workspace by another path -- lowercase `desktop`
// on a case-insensitive filesystem, or a symlink as here -- used to add it
// a second time, so the listing showed a workspace that does not exist and
// put the "current" marker on the wrong row.
func TestWorkspaceList_SameWorkspaceViaAnotherPathIsListedOnce(t *testing.T) {
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(wsDir, link))

	// --workspace stands in for "the cwd resolved to this spelling".
	stdout, stderr, code := run(t, "--workspace", link, "workspace", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var rows []struct {
		Dir     string `json:"dir"`
		Current bool   `json:"current"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &rows))

	require.Len(t, rows, 1, "one workspace reached two ways is one row (got %+v)", rows)
	assert.True(t, rows[0].Current, "the row must be marked current whichever path named it")
}
