package cli_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// --- call --example: merging input/body/headers ---

func TestCall_Example_MergesInputBodyHeaders(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "base-example", Operation: "rider-service.assignRider",
		Input:   map[string]any{"riderId": "rider_1"},
		Body:    map[string]any{"orderId": "order_1"},
		Headers: map[string]string{"X-From-Example": "abc123"},
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "call", "rider-service.assignRider", "--example", "base-example", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "passed", got["status"])

	call := lastCall(fake, "Runner.RunFlowSource")
	args, ok := call.Args.(map[string]any)
	require.True(t, ok)
	yamlSrc, ok := args["yaml"].(string)
	require.True(t, ok)
	assert.Contains(t, yamlSrc, "call: rider-service.assignRider")
	assert.Contains(t, yamlSrc, "riderId: rider_1")
	assert.Contains(t, yamlSrc, "orderId: order_1")
	assert.Contains(t, yamlSrc, "X-From-Example: abc123")
}

func TestCall_Example_ExplicitFlagsOverride(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "override-example", Operation: "rider-service.assignRider",
		Input:   map[string]any{"riderId": "rider_1"},
		Body:    map[string]any{"orderId": "order_1"},
		Headers: map[string]string{"X-Trace": "fromexample"},
	})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "call", "rider-service.assignRider", "--example", "override-example",
		"-p", "riderId=rider_2", "--body", `{"orderId":"order_9"}`, "-H", "X-Trace: fromflag", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	call := lastCall(fake, "Runner.RunFlowSource")
	args := call.Args.(map[string]any)
	yamlSrc := args["yaml"].(string)
	assert.Contains(t, yamlSrc, "riderId: rider_2")
	assert.Contains(t, yamlSrc, "orderId: order_9")
	assert.Contains(t, yamlSrc, "X-Trace: fromflag")
	assert.NotContains(t, yamlSrc, "rider_1")
	assert.NotContains(t, yamlSrc, "fromexample")
}

func TestCall_Example_InputsPassedAsRunOptions(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "templated-example", Operation: "order-service.createOrder",
		Body: map[string]any{"customerId": "${inputs.customerId}"},
	})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "--example", "templated-example",
		"-i", "customerId=cust_42", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	call := lastCall(fake, "Runner.RunFlowSource")
	args := call.Args.(map[string]any)
	opts, ok := args["opts"].(engine.RunOptions)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"customerId": "cust_42"}, opts.Inputs)

	yamlSrc := args["yaml"].(string)
	assert.Contains(t, yamlSrc, "${inputs.customerId}")
}

func TestCall_Example_OperationMismatch(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	_, err := fake.Examples().Create(ctx, domain.SavedExample{ID: "mismatch-example", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "call", "rider-service.getRider", "--example", "mismatch-example", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

func TestCall_Example_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "--example", "nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_EXAMPLE_NOT_FOUND", got["code"])
}

func TestCall_Input_WithoutExample_Errors(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "-i", "x=1", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

func TestCall_Example_CombinedWithSaveAs(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "save-as-example", Operation: "order-service.createOrder",
		Body: map[string]any{"customerId": "cust_1"},
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"--example", "save-as-example", "--save-as", "from-example-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "from-example-flow")

	fl, err := fake.Flows().Get(ctx, "from-example-flow")
	require.NoError(t, err)
	require.Len(t, fl.Steps, 1)
	assert.Equal(t, "order-service.createOrder", fl.Steps[0].Call)
	assert.Equal(t, map[string]any{"customerId": "cust_1"}, fl.Steps[0].Body)
}

// --- call --save-example ---

func TestCall_SaveExample_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"-p", "customerId=cust_1", "--save-example", "saved-from-call", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "passed", got["status"])
	example, ok := got["example"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "saved-from-call", example["id"])
	verified, ok := example["verified"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, got["environment"], verified["env"])

	created, err := fake.Examples().Get(context.Background(), "saved-from-call")
	require.NoError(t, err)
	require.NotNil(t, created.Verified)
	assert.Equal(t, got["id"], created.Verified.RunID)

	call := lastCall(fake, "Examples.FromRun")
	req, ok := call.Args.(engine.ExampleFromRun)
	require.True(t, ok)
	assert.Equal(t, "saved-from-call", req.ID)
	require.NotNil(t, req.Source)
	assert.Equal(t, "user", req.Source.Kind)
	assert.Equal(t, got["id"], req.RunID)
}

func TestCall_SaveExample_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"-p", "customerId=cust_1", "--save-example", "saved-human")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "saved example saved-human")
	assert.Contains(t, stdout, "-> ")
	assert.Contains(t, stdout, "use it: `sapien call --example saved-human`, or in a flow step: `example: saved-human`")
}

func TestCall_SaveExample_ScopeDescriptionTags(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"--save-example", "tagged-example", "--scope", "service", "--description", "a described example",
		"--tag", "qcom", "--tag", "smoke")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "saved example tagged-example")

	call := lastCall(fake, "Examples.FromRun")
	req := call.Args.(engine.ExampleFromRun)
	assert.Equal(t, domain.ExampleScope("service"), req.Scope)
	assert.Equal(t, "a described example", req.Description)
	assert.ElementsMatch(t, []string{"qcom", "smoke"}, req.Tags)
}

func TestCall_SaveExample_NoHintsInJSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"--save-example", "json-no-hints", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "use it:")
}

// --save-as and --save-example are independent side effects of the same
// run and may be combined (PLAN §34b build item 2).
func TestCall_SaveExample_CombinedWithSaveAs(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"-p", "customerId=cust_1", "--save-as", "combined-flow", "--save-example", "combined-example")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "saved combined-flow")
	assert.Contains(t, stdout, "saved example combined-example")

	_, err := fake.Flows().Get(ctx, "combined-flow")
	require.NoError(t, err)
	_, err = fake.Examples().Get(ctx, "combined-example")
	require.NoError(t, err)
}
