package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- describe: human-mode rendering of every optional section
// (params/request body/responses/security/deprecated/source), plus --fields
// and --examples, using the real logistics fixtures so security and
// deprecated are actually populated (the enginetest.Fake's seeded
// operations never set either). ---

func TestDescribe_Human_ParamsAndSecurity(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "describe", "order-service.listOrders")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	assert.Contains(t, stdout, "GET /v1/orders")
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "customerId")
	assert.Contains(t, stdout, "query")
	assert.Contains(t, stdout, "Filter by customer ID.")
	assert.Contains(t, stdout, "responses:")
	assert.Contains(t, stdout, "200 (application/json)")
	assert.Contains(t, stdout, "security: bearerAuth")
	assert.Contains(t, stdout, "source:")
	assert.NotContains(t, stdout, "DEPRECATED")
}

func TestDescribe_Human_RequestBodyAndDeprecated(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "describe", "allocation-service.allocateV1")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	assert.Contains(t, stdout, "POST /v1/allocate")
	assert.Contains(t, stdout, "request body (application/json):")
	assert.Contains(t, stdout, "orderId")
	assert.Contains(t, stdout, "DEPRECATED")
}

func TestDescribe_Human_Examples(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "describe", "allocation-service.allocate", "--examples")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, `"request"`)
	assert.Contains(t, stdout, "ord_0001")
	assert.Contains(t, stdout, `"response_409"`)
}

func TestDescribe_Human_Examples_None(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "describe", "allocation-service.allocateV1", "--examples")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "{}") // no examples anywhere on this deprecated operation
}

func TestDescribe_Human_Fields(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "describe", "order-service.createOrder", "--fields")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "fields:")
	assert.Contains(t, stdout, "PATH")
	assert.Contains(t, stdout, "request.body.customerId")
}

// TestDescribe_Fake_NoSecurityNoDeprecated exercises the false branches of
// the security/deprecated rendering (the seeded Fake operations set
// neither), complementing the real-fixture tests above.
func TestDescribe_Fake_NoSecurityNoDeprecated(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "describe", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "security:")
	assert.NotContains(t, stdout, "DEPRECATED")
	assert.Contains(t, stdout, "Create an order")
}

func TestDescribe_Fake_ByMethodPath(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "describe", "GET /v1/orders/{orderId}")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "order-service.getOrder")
}
