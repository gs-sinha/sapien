package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

// --- secret ---

func TestSecretSet_Value(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "secret", "set", "API_KEY", "--value", "sekrit", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "API_KEY", got["set"])
	assert.NotContains(t, stdout, "sekrit")

	names, err := fake.Envs().ListSecrets(context.Background())
	require.NoError(t, err)
	assert.Contains(t, names, "API_KEY")
}

func TestSecretList(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "secret", "set", "API_KEY", "--value", "sekrit")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", dir, "secret", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "API_KEY")
	assert.NotContains(t, stdout, "sekrit")
}

func TestSecretRm(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "secret", "set", "API_KEY", "--value", "sekrit")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", dir, "secret", "rm", "API_KEY")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "API_KEY")
}

func TestSecretSet_NoValue(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "secret", "set", "API_KEY", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
	assert.NotEmpty(t, got["hint"])
}

// --- memory ---

func TestMemoryAdd(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "riders need a fresh token every hour",
		"--op", "rider-service.getRider", "--type", "gotcha", "--scope", "workspace", "--tag", "riders")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "mem_")

	mems, err := fake.Memories().List(context.Background(), domain.MemoryQuery{})
	require.NoError(t, err)
	found := false
	for _, m := range mems {
		if strings.Contains(m.Text, "fresh token") {
			found = true
		}
	}
	assert.True(t, found, "expected the new memory in the fake's store")
}

func TestMemoryList(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "ID")
	assert.Contains(t, stdout, "TYPE")

	stdout, stderr, code = run(t, "--workspace", dir, "memory", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var mems []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &mems))
	assert.NotEmpty(t, mems)
}

func TestMemorySearch(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "search", "customerId")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SCORE")
}

func TestMemoryShow(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	mems, err := fake.Memories().List(context.Background(), domain.MemoryQuery{})
	require.NoError(t, err)
	require.NotEmpty(t, mems)
	id := mems[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "show", id)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "id: "+id)
}

func TestMemoryRm(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	mems, err := fake.Memories().List(context.Background(), domain.MemoryQuery{})
	require.NoError(t, err)
	require.NotEmpty(t, mems)
	id := mems[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "rm", id)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, id)

	_, err = fake.Memories().Get(context.Background(), id)
	assert.Error(t, err)
}

func TestMemoryPromote(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	mems, err := fake.Memories().List(context.Background(), domain.MemoryQuery{})
	require.NoError(t, err)
	require.NotEmpty(t, mems)
	id := mems[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "promote", id, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "openapi", got["kind"])
}

func TestMemoryReindex(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "reindex")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "reindexed")
}

func TestMemory_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "memory", "show", "mem_nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_MEMORY_NOT_FOUND", got["code"])
}

// --- context ---

func TestContext_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "context", "create an order and fetch it")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "Operations")
	assert.Contains(t, stdout, "Memories")
}

func TestContext_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "context", "create an order",
		"--op", "order-service.createOrder", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "create an order", got["intent"])
}
