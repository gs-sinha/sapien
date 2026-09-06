package local

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/diagnose"
	"github.com/gs-sinha/sapien/internal/engine"
)

const softFlowV1 = `version: 1
id: soft-flip
inputs:
  expected: { type: string, default: "no" }
steps:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: STANDARD, pickup: { lat: 12.9, lng: 77.5 }, drop: { lat: 12.9, lng: 77.6 } }
    extract: { orderId: body.orderId }
  - id: check
    call: order-service.getOrder
    input: { orderId: "${steps.create.out.orderId}" }
    assert:
      - status == 200
      - expr: inputs.expected == "yes"
        soft: true
        message: gap still open
`

// The second run of a flow reports the soft assertions that flipped since
// the previous run, so a closed gap announces itself.
func TestSoftChanges_ReportsFlipSincePreviousRun(t *testing.T) {
	m := setupEngineWithMock(t)
	ctx := context.Background()

	first, err := m.l.Runner().RunFlowSource(ctx, softFlowV1, engine.RunOptions{Environment: "test"})
	require.NoError(t, err)
	assert.Equal(t, "passed", string(first.Status), "a soft mismatch never fails the run")
	assert.Equal(t, 1, first.Summary.AssertionsWarned)
	assert.Empty(t, diagnose.SoftChanges(ctx, m.l, first), "no earlier run to compare with")
	lines := diagnose.SoftLines(first, nil)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "soft mismatch: check: inputs.expected == \"yes\" (gap still open)")

	// The gap closes: the same assertion text now holds (here driven by an
	// input, in life by the API changing underneath the flow).
	second, err := m.l.Runner().RunFlowSource(ctx, softFlowV1, engine.RunOptions{Environment: "test", Inputs: map[string]any{"expected": "yes"}})
	require.NoError(t, err)
	changes := diagnose.SoftChanges(ctx, m.l, second)
	require.Len(t, changes, 1)
	assert.True(t, changes[0].NowPassing)
	assert.Equal(t, "check", changes[0].StepID)
	assert.Equal(t, first.ID, changes[0].SinceRun)
	lines = diagnose.SoftLines(second, changes)
	require.NotEmpty(t, lines)
	assert.Contains(t, lines[0], "now passing since run "+first.ID)
}
