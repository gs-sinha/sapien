package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newReloadSession is newTestSession's ConfigPaths-aware counterpart: it
// builds a server whose permissions come from the given files (Options.
// ConfigPaths), not a static Config, so a test can rewrite those files
// mid-session and observe the effect on the very next tool call.
func newReloadSession(t *testing.T, paths []string, clientImplName string) *sdkmcp.ClientSession {
	t.Helper()
	eng := newFixtureEngine()
	srv := NewServer(Options{Engine: eng, ConfigPaths: paths, Version: "test", Logger: silentLogger})

	c1, c2 := sdkmcp.NewInMemoryTransports()
	ctx := context.Background()

	if _, err := srv.Connect(ctx, c1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: clientImplName, Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, c2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestPermissionsHotReload_NoRestartNeeded is the scenario from the real
// session that motivated this feature: a client starts with
// execute_mutation denied, calls execute_api with a POST and is denied,
// then someone edits <workspace>/.sapien/mcp.yaml to grant it -- and the
// *same, still-connected* session must see the grant on its very next
// call, with no server restart and no reconnect.
func TestPermissionsHotReload_NoRestartNeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
default:
  execute_mutation: false
`), 0o644))

	cs := newReloadSession(t, []string{path}, "claude-code")

	callPost := func() *sdkmcp.CallToolResult {
		return callTool(t, cs, "execute_api", map[string]any{
			"id":  "rider-service.createRider", // POST
			"env": "staging",
		})
	}

	res := callPost()
	require.True(t, res.IsError, "expected denied before the file grants execute_mutation")
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
	assert.Contains(t, firstText(res), "execute_mutation")

	// Rewrite the file granting it. mtime resolution on some filesystems
	// is coarse (1s on HFS+); bump ModTime explicitly afterward so the
	// stamp genuinely differs even on a fast test run.
	require.NoError(t, os.WriteFile(path, []byte(`
default:
  execute_mutation: true
`), 0o644))
	require.NoError(t, os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)))

	res = callPost()
	assert.False(t, res.IsError, "expected allowed on the next call, no reconnect: %s", firstText(res))
}

// TestPermissionsHotReload_UnchangedFileServesCache proves configSource
// isn't reparsing the file on every single call: it stats the file (size
// + mtime) each time but only reruns LoadConfig when that stamp changes.
// A grant added after the file's stamp was captured, with the stamp then
// forced back to its original value, must NOT be observed.
func TestPermissionsHotReload_UnchangedFileServesCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
default:
  read_flows: false
`), 0o644))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	origSize, origMTime := fi.Size(), fi.ModTime()

	cs := newReloadSession(t, []string{path}, "claude-code")

	res := callTool(t, cs, "list_flows", map[string]any{})
	require.True(t, res.IsError, "expected denied: read_flows is false")

	// Overwrite with content of a different size (so a naive "did the
	// file change" check that only compares size would still catch it),
	// then force the stamp back to the original size+mtime.
	require.NoError(t, os.WriteFile(path, []byte(`
default:
  read_flows: true
`), 0o644))
	// Pad/trim isn't needed: os.Chtimes controls mtime, and we additionally
	// truncate back to the original size so both halves of the stamp
	// (size + mtime) match the first load.
	require.NoError(t, os.Truncate(path, origSize))
	require.NoError(t, os.Chtimes(path, origMTime, origMTime))

	res = callTool(t, cs, "list_flows", map[string]any{})
	assert.True(t, res.IsError, "the forced-identical stamp must keep serving the cached (denying) config")
}

// TestPermissionsHotReload_FileAppearing proves a file that didn't exist
// at the first load, and then appears, counts as a change: configSource
// must not remember "missing" forever.
func TestPermissionsHotReload_FileAppearing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml") // does not exist yet

	cs := newReloadSession(t, []string{path}, "claude-code")

	// No file yet: the baseline default profile grants read_flows.
	res := callTool(t, cs, "list_flows", map[string]any{})
	require.False(t, res.IsError, "text: %s", firstText(res))

	require.NoError(t, os.WriteFile(path, []byte(`
default:
  read_flows: false
`), 0o644))

	res = callTool(t, cs, "list_flows", map[string]any{})
	assert.True(t, res.IsError, "a newly appeared file must be picked up on the next call")
}

// TestPermissionsHotReload_ConfigPathsEmptyUsesStaticConfig confirms
// Options.Config keeps working exactly as before when ConfigPaths is not
// set -- the static fallback every other test in this package (and the
// rest of the suite) relies on.
func TestPermissionsHotReload_ConfigPathsEmptyUsesStaticConfig(t *testing.T) {
	cfg := Config{Default: Permissions{ReadFlows: true, ExecuteMutation: true}}
	cs := newTestSession(t, cfg, "claude-code")

	res := callTool(t, cs, "execute_api", map[string]any{
		"id":  "rider-service.createRider",
		"env": "staging",
	})
	assert.False(t, res.IsError, "text: %s", firstText(res))
}

// TestPermissionsHotReload_MalformedEditKeepsLastGood proves a transient
// or broken edit (e.g. a half-written file, or a real YAML typo) doesn't
// deny every call: configSource keeps serving the last good Config until
// the file parses again.
func TestPermissionsHotReload_MalformedEditKeepsLastGood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
default:
  read_flows: true
`), 0o644))

	cs := newReloadSession(t, []string{path}, "claude-code")
	res := callTool(t, cs, "list_flows", map[string]any{})
	require.False(t, res.IsError, "text: %s", firstText(res))

	// Not valid YAML.
	require.NoError(t, os.WriteFile(path, []byte("default: [this is not a mapping"), 0o644))

	res = callTool(t, cs, "list_flows", map[string]any{})
	assert.False(t, res.IsError, "a broken edit must keep serving the last good config, not deny everything")
}
