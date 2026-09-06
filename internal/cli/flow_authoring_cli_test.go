package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authoringFlow = `version: 1
id: auth-demo
steps:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: STANDARD }
    assert: [status == 201]
  - id: get
    call: order-service.getOrder
    input: { orderId: "${steps.create.out.orderId}" }
    assert: [status == 999]
`

func writeTemp(t *testing.T, name, content string) string {
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestFlowCreate_PrintsSummaryNotDocument(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	file := writeTemp(t, "auth-demo.flow.yaml", authoringFlow)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "saved flow auth-demo")
	assert.Contains(t, stdout, "2 steps")
	assert.NotContains(t, stdout, "steps:")

	stdout, stderr, code = run(t, "--workspace", dir, "flow", "create", file, "--json")
	// The fake conflicts on a second create of the same id; either outcome must be a summary shape.
	if code == 0 {
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &got))
		assert.Equal(t, "auth-demo", got["id"])
		assert.EqualValues(t, 2, got["steps"])
	} else {
		assert.Contains(t, stderr, "E_CONFLICT")
	}
}

func TestFlowPatch_MergeStepAndRemoveStep(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	file := writeTemp(t, "auth-demo.flow.yaml", authoringFlow)
	_, stderr, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "patch", "auth-demo", "--merge-step", "get={assert: [status == 200]}")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "patched flow auth-demo")
	upd := lastCall(fake, "Flows.Update").Args.(map[string]string)
	yamlSent := upd["yaml"]
	assert.Contains(t, yamlSent, "status == 200")
	assert.NotContains(t, yamlSent, "status == 999")
	assert.Contains(t, yamlSent, "customerId: c1", "untouched steps survive verbatim")

	_, stderr, code = run(t, "--workspace", dir, "flow", "patch", "auth-demo", "--remove-step", "get", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	upd = lastCall(fake, "Flows.Update").Args.(map[string]string)
	yamlSent = upd["yaml"]
	assert.NotContains(t, yamlSent, "id: get")

	_, stderr, code = run(t, "--workspace", dir, "flow", "patch", "auth-demo")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "nothing to patch")

	_, stderr, code = run(t, "--workspace", dir, "flow", "patch", "auth-demo", "--remove-step", "nope")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "nope")
}

func TestFlowPatch_OpsFile(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	file := writeTemp(t, "auth-demo.flow.yaml", authoringFlow)
	_, _, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code)
	ops := writeTemp(t, "ops.json", `[{"kind":"set_inputs","inputs":{"customerId":{"type":"string","default":"c9"}}},{"kind":"merge_step","id":"create","fields":{"body":{"customerId":"${inputs.customerId}","type":"STANDARD"}}}]`)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "patch", "auth-demo", "--ops", "@"+ops)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "patched flow auth-demo")
	yamlSent := lastCall(fake, "Flows.Update").Args.(map[string]string)["yaml"]
	assert.Contains(t, yamlSent, "customerId:")
	assert.Contains(t, yamlSent, "inputs:")
}

func TestFlowUpdate_FromFile(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	file := writeTemp(t, "auth-demo.flow.yaml", authoringFlow)
	_, _, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code)

	edited := writeTemp(t, "edited.flow.yaml", authoringFlow+"tags: [edited]\n")
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "update", "auth-demo", "--file", edited)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "updated flow auth-demo")
	yamlSent := lastCall(fake, "Flows.Update").Args.(map[string]string)["yaml"]
	assert.Contains(t, yamlSent, "tags: [edited]")

	_, stderr, code = run(t, "--workspace", dir, "flow", "update", "auth-demo")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "--file is required")
}
