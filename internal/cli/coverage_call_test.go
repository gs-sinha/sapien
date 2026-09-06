package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- call: parseParams / parseHeaders / parseBody error and success paths ---

func TestCall_InvalidParam(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "-p", "not-a-pair")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "E_INVALID")
}

func TestCall_InvalidHeader(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "-H", "not-a-header", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

func TestCall_InvalidBodyJSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "--body", "{not json", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

func TestCall_BodyFromFile(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	bodyFile := filepath.Join(dir, "body.json")
	require.NoError(t, os.WriteFile(bodyFile, []byte(`{"customerId":"cust_9"}`), 0o644))

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "--body", "@"+bodyFile, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "passed", got["status"])
}

func TestCall_BodyFromMissingFile(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "--body", "@"+filepath.Join(dir, "nope.json"), "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

func TestCall_Headers_Success(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"-H", "X-Trace-Id: abc123", "-p", "customerId=cust_1", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "passed")
}

// --- call --save-as: conflict path (Flows().Create fails after the call
// itself already succeeded) ---

func TestCall_SaveAs_Conflict(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"--save-as", "create-order-flow", "--json") // this flow id already exists in the seed
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_CONFLICT", got["code"])
}

func TestCall_SaveAs_WithBody(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "rider-service.assignRider",
		"--body", `{"orderId":"order_9"}`, "--save-as", "my-assign-flow", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "passed", got["status"])

	fl, err := fake.Flows().Get(context.Background(), "my-assign-flow")
	require.NoError(t, err)
	require.Len(t, fl.Steps, 1)
	assert.Equal(t, "rider-service.assignRider", fl.Steps[0].Call)
}
