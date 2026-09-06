package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- context: --op, --flow, and --budget together, both JSON and human ---

func TestContext_WithOpFlowBudget_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "context", "create and fetch an order",
		"--op", "order-service.createOrder", "--op", "order-service.getOrder",
		"--flow", "create-order-flow", "--budget", "500", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "create and fetch an order", got["intent"])
	ops, ok := got["operations"].([]any)
	require.True(t, ok)
	assert.Len(t, ops, 2)
	flows, ok := got["flows"].([]any)
	require.True(t, ok)
	require.Len(t, flows, 1)
	flow := flows[0].(map[string]any)
	assert.Equal(t, "create-order-flow", flow["id"])
}

func TestContext_WithFlow_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "context", "create an order",
		"--flow", "create-order-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "intent: create an order")
	assert.Contains(t, stdout, "Operations")
	assert.Contains(t, stdout, "Docs")
	assert.Contains(t, stdout, "Memories")
	assert.Contains(t, stdout, "Flows")
	assert.Contains(t, stdout, "create-order-flow: create: order-service.createOrder, fetch: order-service.getOrder")
	assert.Contains(t, stdout, "estimated tokens:")
}

func TestContext_UnknownFlow_OmitsFlowsSection(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "context", "an intent", "--flow", "no-such-flow", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Nil(t, got["flows"])
}
