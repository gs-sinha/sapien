package local

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/flowpatch"
)

// TestFlows_Patch_InvalidResultLeavesFileUntouched exercises
// flowpatch.Apply followed by the real local engine's Update -- the exact
// path patch_flow (MCP) and `sapien flow patch` (CLI) both take. Apply
// itself doesn't check the result against the operation catalog (it only
// edits YAML), so an op that leaves the flow calling an operation that
// doesn't exist must be caught by Update's own validate-before-write, the
// same as any other invalid flow, and the saved file must come back
// byte-for-byte unchanged (HALF2, discussion #4: "a patch that produces an
// invalid flow must leave the file untouched").
func TestFlows_Patch_InvalidResultLeavesFileUntouched(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	f, err := l.Flows().Create(ctx, validFlowYAML, "")
	require.NoError(t, err)
	before, err := os.ReadFile(f.Path)
	require.NoError(t, err)

	patchResult, err := flowpatch.Apply(string(before), []flowpatch.Op{{
		Kind:   flowpatch.KindMergeStep,
		ID:     "create",
		Fields: map[string]any{"call": "order-service.doesNotExist"},
	}})
	require.NoError(t, err, "Apply itself succeeds -- it edits YAML, it doesn't consult the operation catalog")

	_, err = l.Flows().Update(ctx, "order-allocation", patchResult.YAML)
	require.Error(t, err, "the patched flow calls an operation that doesn't exist; Update must reject it")

	after, err := os.ReadFile(f.Path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the saved file must be byte-identical to before the rejected patch")
}

// TestFlows_Patch_ValidResultPreservesHandFormatting confirms a valid
// patch, applied through the real engine (not just flowpatch's own unit
// tests), keeps a hand-formatted flow's 2-space indent, leading comment,
// and key order on disk.
func TestFlows_Patch_ValidResultPreservesHandFormatting(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	const src = `version: 1
id: fmt-flow
steps:
  # creates the order
  - id: create
    call: order-service.createOrder
    body:
      customerId: cust_123
      type: QCOM
      pickup: { lat: 12.9716, lng: 77.5946 }
      drop: { lat: 12.9352, lng: 77.6146 }
    assert:
      - status == 201
`
	f, err := l.Flows().Create(ctx, src, "")
	require.NoError(t, err)

	patchResult, err := flowpatch.Apply(src, []flowpatch.Op{{
		Kind:   flowpatch.KindMergeStep,
		ID:     "create",
		Fields: map[string]any{"until": "status == 201"},
	}})
	require.NoError(t, err)

	_, err = l.Flows().Update(ctx, "fmt-flow", patchResult.YAML)
	require.NoError(t, err)

	onDisk, err := os.ReadFile(f.Path)
	require.NoError(t, err)
	out := string(onDisk)
	assert.Contains(t, out, "  # creates the order", "the step's comment must survive on disk")
	assert.Contains(t, out, "  - id: create", "the source's 2-space indent must survive on disk")
	assert.NotContains(t, out, "    - id: create", "must not have reflowed to yaml.v3's 4-space default")
	assert.Contains(t, out, "until: status == 201")
}
