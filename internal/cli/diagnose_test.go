package cli_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
)

// This file exercises the CLI wiring for internal/diagnose: `sapien call`,
// `sapien flow run`, and `sapien run show` print a "Might explain it:"
// block (and, under --json, a "hints" array) when a run failed or errored
// with an HTTP response >= 400 that diagnose can say something about.
//
// It leans on data enginetest.Seed already provides rather than adding new
// fixtures to the shared Fake (internal/engine/enginetest is owned by
// another agent, and Fake's docs/memories maps aren't exported): the seeded
// order-service.createOrder operation, its "docs/orders.md#Overview"
// section ("Orders represent a customer purchase awaiting rider assignment.")
// and its "createOrder requires customerId ..." memory are enough to
// exercise a doc hit and a memory hit end-to-end.

// findSeededMemoryID locates the seeded memory whose Subject.Operation is
// op, so tests can assert on "sapien memory show <id>" without hard-coding
// the ULID Seed generated it with.
func findSeededMemoryID(t *testing.T, fake *enginetest.Fake, op string) string {
	t.Helper()
	mems, err := fake.Memories().List(context.Background(), domain.MemoryQuery{Operation: op})
	require.NoError(t, err)
	require.NotEmpty(t, mems, "expected a seeded memory for operation %q", op)
	return mems[0].ID
}

// failingCreateOrderRun is a one-step run for order-service.createOrder
// whose response is a 422 with a body carrying two tokens: "customerId"
// (found verbatim in the seeded memory's text) and "rider assignment"
// (found verbatim in the seeded doc's "Overview" section body, which ends
// "...awaiting rider assignment"). Neither token cross-matches the other
// fixture, so this deterministically yields exactly one doc hint and one
// memory hint, no contract hint (createOrder's contract only documents
// 201).
func failingCreateOrderRun() *domain.Run {
	now := time.Now().UTC()
	return &domain.Run{
		FlowID:     "create-order-flow",
		Status:     domain.RunFailed,
		Started:    now,
		Finished:   now.Add(5 * time.Millisecond),
		DurationMs: 5,
		Steps: []domain.StepResult{{
			StepID:    "call",
			Operation: "order-service.createOrder",
			Status:    domain.StepFailed,
			Response: &domain.ResponseRecord{
				Status: 422,
				Body:   map[string]any{"code": "customerId", "message": "rider assignment"},
			},
			Assertions: []domain.AssertionResult{{Expr: "status == 200", Passed: false}},
			Started:    now,
			Finished:   now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsFailed: 1, Assertions: 1, AssertionsFailed: 1},
	}
}

func assertHintLines(t *testing.T, stdout, memID string) {
	t.Helper()
	assert.Contains(t, stdout, "Might explain it:")
	assert.Contains(t, stdout, "order-service/docs/orders.md # Overview")
	assert.Contains(t, stdout, "(matched: rider assignment)")
	assert.Contains(t, stdout, `sapien docs show order-service docs/orders.md --section "Overview"`)
	assert.Contains(t, stdout, "(matched: customerId)")
	assert.Contains(t, stdout, "sapien memory show "+memID)
}

// --- sapien call ---

func TestCall_Human_Hints_OnFailure(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	memID := findSeededMemoryID(t, fake, "order-service.createOrder")
	fake.RunResult = failingCreateOrderRun()

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	assert.Equal(t, 1, code, "stderr: %s", stderr) // RunFailed -> exit 1
	assertHintLines(t, stdout, memID)
}

func TestCall_JSON_Hints_OnFailure(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	memID := findSeededMemoryID(t, fake, "order-service.createOrder")
	fake.RunResult = failingCreateOrderRun()

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "--json")
	assert.Equal(t, 1, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	// Existing JSON consumers read run fields at the top level (see
	// TestCall_JSON in phase2_test.go) -- that must still work.
	assert.Equal(t, "failed", got["status"])

	hints, ok := got["hints"].([]any)
	require.True(t, ok, "expected a top-level hints array, got %v", got["hints"])
	require.Len(t, hints, 2)

	var sawDoc, sawMemory bool
	for _, raw := range hints {
		h := raw.(map[string]any)
		switch h["kind"] {
		case "doc":
			sawDoc = true
			assert.Equal(t, "order-service/docs/orders.md # Overview", h["title"])
			assert.Equal(t, "rider assignment", h["matched_on"])
		case "memory":
			sawMemory = true
			ref := h["ref"].(map[string]any)
			assert.Equal(t, memID, ref["memory_id"])
			assert.Equal(t, "customerId", h["matched_on"])
		}
	}
	assert.True(t, sawDoc, "expected a doc hint")
	assert.True(t, sawMemory, "expected a memory hint")
}

func TestCall_Human_NoHints_WhenPassed(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "Might explain it:")
}

func TestCall_JSON_NoHintsKey_WhenPassed(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "passed", got["status"])
	_, present := got["hints"]
	assert.False(t, present, "hints should be omitted entirely for a run with none")
}

// --- sapien flow run ---

func TestFlowRun_Human_Hints_OnFailure(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	memID := findSeededMemoryID(t, fake, "order-service.createOrder")
	fake.RunResult = failingCreateOrderRun()

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow")
	assert.Equal(t, 1, code, "stderr: %s", stderr)
	assertHintLines(t, stdout, memID)
}

func TestFlowRun_JSON_Hints_OnFailure(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.RunResult = failingCreateOrderRun()

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--json")
	assert.Equal(t, 1, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "failed", got["status"]) // top-level field, unbroken by the wrapper
	hints, ok := got["hints"].([]any)
	require.True(t, ok)
	assert.Len(t, hints, 2)
}

// --- sapien run show ---

func TestRunShow_Human_Hints_OnFailure(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	memID := findSeededMemoryID(t, fake, "order-service.createOrder")
	fake.RunResult = failingCreateOrderRun()

	_, _, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 1, code)

	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{Status: domain.RunFailed})
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	runID := runs[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "run", "show", runID)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assertHintLines(t, stdout, memID)
}

func TestRunShow_JSON_Hints_OnFailure(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.RunResult = failingCreateOrderRun()

	_, _, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 1, code)

	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{Status: domain.RunFailed})
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	runID := runs[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "run", "show", runID, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "failed", got["status"])
	hints, ok := got["hints"].([]any)
	require.True(t, ok)
	assert.Len(t, hints, 2)
}

// --step still shows one step's own detail, untouched by hints (diagnose
// operates on the whole run view, not the isolated --step rendering).
func TestRunShow_Step_UnaffectedByHints(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.RunResult = failingCreateOrderRun()

	_, _, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 1, code)

	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{Status: domain.RunFailed})
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	runID := runs[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "run", "show", runID, "--step", "call")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "Might explain it:")
}
