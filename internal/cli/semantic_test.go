package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOllamaCLIServer is a minimal Ollama-shaped /api/embed endpoint (see
// internal/engine/local's own fakeOllamaEmbeddingServer for the same
// shape), just enough for `sapien semantic enable/test`'s pre-save/test
// probe to succeed against it end to end.
func fakeOllamaCLIServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		embeddings := make([][]float32, len(req.Input))
		for i := range req.Input {
			embeddings[i] = []float32{0.1, 0.2, 0.3}
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(struct {
			Embeddings [][]float32 `json:"embeddings"`
		}{Embeddings: embeddings}))
	}))
}

func TestSemantic_StatusOffByDefault(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir, "--name", "logistics")
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "semantic", "status", "--json")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, false, out["enabled"])
	assert.Equal(t, "default", out["source"])
}

// TestSemantic_EnableTestDisable_Workspace exercises enable -> status ->
// test -> disable end to end, in-process (no daemon, per
// SAPIEN_NO_DAEMON), --workspace-scope so it writes <dir>/.sapien/
// config.yaml rather than the shared user config every test in this
// package uses.
func TestSemantic_EnableTestDisable_Workspace(t *testing.T) {
	t.Setenv("SAPIEN_NO_DAEMON", "1")
	srv := fakeOllamaCLIServer(t)
	defer srv.Close()

	dir := t.TempDir()
	_, _, code := run(t, "init", dir, "--name", "logistics")
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "semantic", "enable",
		"--kind", "ollama", "--model", "nomic-embed-text", "--base-url", srv.URL, "--workspace-scope", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var enabled map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &enabled))
	assert.Equal(t, true, enabled["enabled"])
	assert.Equal(t, "ollama", enabled["kind"])
	assert.Equal(t, "nomic-embed-text", enabled["model"])
	assert.Equal(t, "workspace", enabled["source"])

	// The workspace-level file must exist and carry it -- not the shared
	// user config this whole package points SAPIEN_CONFIG at.
	assert.FileExists(t, filepath.Join(dir, ".sapien", "config.yaml"))
	data, err := os.ReadFile(filepath.Join(dir, ".sapien", "config.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "nomic-embed-text")

	stdout, _, code = run(t, "--workspace", dir, "semantic", "status")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "ollama")
	assert.Contains(t, stdout, "nomic-embed-text")

	stdout, stderr, code = run(t, "--workspace", dir, "semantic", "test", "--json")
	assert.Equal(t, 0, code, "stderr: %s", stderr)
	var testOut map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &testOut))
	assert.Equal(t, true, testOut["ok"])

	stdout, stderr, code = run(t, "--workspace", dir, "semantic", "disable", "--workspace-scope", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var disabled map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &disabled))
	assert.Equal(t, false, disabled["enabled"])
	// Disabling must not forget the provider: re-enabling should not need
	// --kind/--model again if the UI just flips a switch. Confirmed via
	// status still naming it even though it's off.
	stdout, _, code = run(t, "--workspace", dir, "semantic", "status", "--json")
	assert.Equal(t, 0, code)
	var status map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &status))
	assert.Equal(t, "ollama", status["kind"])
	assert.Equal(t, "nomic-embed-text", status["model"])
}

// TestSemantic_EnableUnreachable_RefusedWithoutForce proves `enable`
// refuses to save a config that fails the connectivity probe unless
// --force is given, and that --force saves it anyway.
func TestSemantic_EnableUnreachable_RefusedWithoutForce(t *testing.T) {
	t.Setenv("SAPIEN_NO_DAEMON", "1")

	dir := t.TempDir()
	_, _, code := run(t, "init", dir, "--name", "logistics")
	require.Equal(t, 0, code)

	_, stderr, code := run(t, "--workspace", dir, "semantic", "enable",
		"--kind", "ollama", "--model", "m", "--base-url", "http://127.0.0.1:1", "--workspace-scope", "--json")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "E_INVALID")

	stdout, stderr, code := run(t, "--workspace", dir, "semantic", "enable",
		"--kind", "ollama", "--model", "m", "--base-url", "http://127.0.0.1:1", "--workspace-scope", "--force", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, true, out["enabled"])
}
