package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- env show / use: human-mode branches (existing tests only exercise
// --json) ---

func TestEnvShow_Human(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "show", "local")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "name: local")
	assert.Contains(t, stdout, "production: false")
	assert.Contains(t, stdout, "path:")
}

func TestEnvUse_Human(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "use", "local")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "default environment set to local")
}

// --- secret set --stdin: value read from process stdin (its first line),
// success and empty-value paths ---

func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
}

func TestSecretSet_Stdin(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	withStdin(t, "sekrit-from-stdin\n")

	stdout, stderr, code := run(t, "--workspace", dir, "secret", "set", "STDIN_KEY", "--stdin")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "STDIN_KEY")
	assert.NotContains(t, stdout, "sekrit-from-stdin")

	names, err := fake.Envs().ListSecrets(t.Context())
	require.NoError(t, err)
	assert.Contains(t, names, "STDIN_KEY")
}

func TestSecretSet_StdinEmpty(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	withStdin(t, "\n")

	stdout, stderr, code := run(t, "--workspace", dir, "secret", "set", "EMPTY_KEY", "--stdin", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "E_INVALID")
	assert.Contains(t, stderr, "empty")
}

// --- secret rm: --json success, and not-found ---

func TestSecretRm_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "secret", "set", "K", "--value", "v")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", dir, "secret", "rm", "K", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "K", got["removed"])
}

func TestSecretRm_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "secret", "rm", "NOPE", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_SECRET_MISSING", got["code"])
}

// --- memory rm: not-found ---

func TestMemoryRm_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "memory", "rm", "mem_nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_MEMORY_NOT_FOUND", got["code"])
}

// --- memory reindex: --json ---

func TestMemoryReindex_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "reindex", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]bool
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got["reindexed"])
}

// --- memory promote: not-found ---

func TestMemoryPromote_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "memory", "promote", "mem_nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_MEMORY_NOT_FOUND", got["code"])
}

// --- service remove: --json success ---

func TestServiceRemove_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	_, err := fake.Services().Get(context.Background(), "order-service")
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "service", "remove", "order-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "order-service", got["removed"])
}

// --- run purge: --json ---

func TestRunPurge_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "run", "purge", "--keep", "100", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]int
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, 0, got["removed"])
}
