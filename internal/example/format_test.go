package example_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/example"
)

func TestMarshalParse_RoundTrip(t *testing.T) {
	created := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	ex := &domain.SavedExample{
		Version:     1,
		ID:          "create-qcom-order",
		Operation:   "order-service.createOrder",
		Description: "QCOM order in Bengaluru",
		Input:       map[string]any{},
		Body: map[string]any{
			"customerId": "c1",
			"type":       "QCOM",
		},
		Headers: map[string]string{"X-Trace": "1"},
		Expect:  &domain.ExampleExpect{Status: 201, Body: map[string]any{"orderId": "ord_0001"}},
		Verified: &domain.ExampleVerified{
			Env:    "stage",
			RunID:  "run_01J",
			StepID: "call",
			At:     created,
			Source: &domain.MemorySource{Kind: "agent", Client: "claude-code"},
		},
		Tags:    []string{"qcom", "happy-path"},
		Created: created,
		Updated: created,
	}

	data, err := example.Marshal(ex)
	require.NoError(t, err)
	assert.Contains(t, string(data), "operation: order-service.createOrder")
	// Scope/Service/Path are derived, never written to the file.
	assert.NotContains(t, string(data), "scope:")
	assert.NotContains(t, string(data), "service:")
	assert.NotContains(t, string(data), "path:")

	path := "/ws/examples/create-qcom-order.example.yaml"
	got, err := example.Parse(data, path)
	require.NoError(t, err)

	assert.Equal(t, ex.Version, got.Version)
	assert.Equal(t, ex.ID, got.ID)
	assert.Equal(t, ex.Operation, got.Operation)
	assert.Equal(t, ex.Description, got.Description)
	assert.Equal(t, "order-service", got.Service) // derived from Operation
	assert.Equal(t, path, got.Path)
	assert.Equal(t, ex.Tags, got.Tags)
	require.NotNil(t, got.Expect)
	assert.Equal(t, 201, got.Expect.Status)
	require.NotNil(t, got.Verified)
	assert.Equal(t, "stage", got.Verified.Env)
	assert.Equal(t, "run_01J", got.Verified.RunID)
	require.NotNil(t, got.Verified.Source)
	assert.Equal(t, "agent", got.Verified.Source.Kind)
	assert.True(t, ex.Created.Equal(got.Created))

	// Scope is never set by Parse; it depends on which directory the file
	// was found under, which Parse does not know.
	assert.Equal(t, domain.ExampleScope(""), got.Scope)
}

func TestParse_DefaultsIDFromPath(t *testing.T) {
	data := []byte("operation: order-service.createOrder\n")
	got, err := example.Parse(data, "/ws/examples/my-example.example.yaml")
	require.NoError(t, err)
	assert.Equal(t, "my-example", got.ID)
	assert.Equal(t, "order-service", got.Service)
}

func TestParse_ExplicitIDWins(t *testing.T) {
	data := []byte("id: explicit-id\noperation: order-service.createOrder\n")
	got, err := example.Parse(data, "/ws/examples/my-example.example.yaml")
	require.NoError(t, err)
	assert.Equal(t, "explicit-id", got.ID)
}

func TestParse_InvalidYAML(t *testing.T) {
	_, err := example.Parse([]byte("{ not: valid: yaml"), "/ws/examples/bad.example.yaml")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestParse_NoOperation_ServiceEmpty(t *testing.T) {
	got, err := example.Parse([]byte("id: no-op\n"), "/ws/examples/no-op.example.yaml")
	require.NoError(t, err)
	assert.Equal(t, "", got.Service)
}

func TestMarshal_WritesIntegralFloatsAsIntegers(t *testing.T) {
	ex := &domain.SavedExample{
		Version: 1, ID: "ts", Operation: "svc.op",
		Input: map[string]any{"limit": float64(50)},
		Body: map[string]any{
			"metadata": map[string]any{"shipmentReadyTime": float64(1788589814724)},
			"price":    499.5,
			"items":    []any{map[string]any{"quantity": float64(1)}},
		},
		Expect: &domain.ExampleExpect{Status: 200, Body: map[string]any{"count": float64(3)}},
	}
	data, err := example.Marshal(ex)
	require.NoError(t, err)
	out := string(data)
	assert.Contains(t, out, "shipmentReadyTime: 1788589814724")
	assert.NotContains(t, out, "e+12")
	assert.Contains(t, out, "price: 499.5")
	assert.Contains(t, out, "quantity: 1\n")
	assert.Contains(t, out, "limit: 50")
	assert.Contains(t, out, "count: 3")

	back, err := example.Parse(data, "ts.example.yaml")
	require.NoError(t, err)
	assert.EqualValues(t, 1788589814724, back.Body.(map[string]any)["metadata"].(map[string]any)["shipmentReadyTime"])
	// The caller's value is left untouched.
	assert.Equal(t, float64(1788589814724), ex.Body.(map[string]any)["metadata"].(map[string]any)["shipmentReadyTime"])
}
