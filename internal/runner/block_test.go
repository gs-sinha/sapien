package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/expr"
)

// createEachBody builds the body of an order-service.createOrder step that
// reads its customerId from `iter.item`, for foreach block tests.
func createEachStep() domain.Step {
	return domain.Step{
		ID:   "create",
		Call: "order-service.createOrder",
		Body: map[string]any{
			"customerId": "${iter.item}",
			"type":       "QCOM",
			"pickup":     map[string]any{"lat": 12.9716, "lng": 77.5946},
			"drop":       map[string]any{"lat": 12.9352, "lng": 77.6146},
		},
		Extract: map[string]string{"orderId": "body.orderId"},
		Assert:  []domain.Assertion{{Status: intp(201)}},
	}
}

// TestRun_Foreach_OverExtractedList exercises a foreach block whose list
// comes from an earlier step's extracted output, with iter.item used in a
// nested step's body template, and confirms steps.<block>.count/iterations
// are readable afterward (PLAN §34f.8).
func TestRun_Foreach_OverExtractedList(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "foreach-extracted",
		Steps: []domain.Step{
			{
				ID:      "customers",
				Call:    "order-service.createOrder",
				Body:    map[string]any{"customerId": "seed", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}},
				Extract: map[string]string{"ids": "['c1', 'c2', 'c3']"},
			},
			{
				ID:      "create_each",
				Foreach: "steps.customers.out.ids",
				Max:     10,
				Steps:   []domain.Step{createEachStep()},
			},
			{
				ID:   "verify",
				Call: "allocation-service.allocate",
				Body: map[string]any{"orderId": "o1"},
				Assert: []domain.Assertion{
					{Expr: "steps.create_each.count == 3"},
					{Expr: "steps.create_each.iterations.size() == 3"},
					{Expr: "steps.create_each.iterations[0].create.status == 201"},
				},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 6) // customers, create_each block, 3 nested creates, verify

	block := findStepResult(t, run, "create_each", nil)
	assert.Equal(t, domain.StepPassed, block.Status)
	assert.Equal(t, "foreach", block.Kind)
	assert.Equal(t, 3, block.Count)

	for i := 0; i < 3; i++ {
		nested := findStepResult(t, run, "create", intp(i))
		assert.Equal(t, "create_each", nested.Parent)
		require.NotNil(t, nested.Iteration)
		assert.Equal(t, i, *nested.Iteration)
		assert.Equal(t, domain.StepPassed, nested.Status)
	}

	verify := findStepResult(t, run, "verify", nil)
	assert.Equal(t, domain.StepPassed, verify.Status, "%+v", verify.Assertions)
	for _, a := range verify.Assertions {
		assert.True(t, a.Passed, "assertion %q: %s %s", a.Expr, a.Message, a.Error)
	}

	assert.Equal(t, domain.RunPassed, run.Status)
	// steps_total counts leaf executions: customers, 3 nested creates, verify = 5;
	// the block itself is not counted as an additional step (see internal/runs.Summarize).
	assert.Equal(t, 5, run.Summary.StepsTotal, "%+v", run.Steps)
}

// findStepResult finds the StepResult named id (optionally at a specific
// iteration) in run.Steps, failing the test if there is no match.
func findStepResult(t *testing.T, run *domain.Run, id string, iteration *int) domain.StepResult {
	t.Helper()
	for _, st := range run.Steps {
		if st.StepID != id {
			continue
		}
		if iteration == nil {
			return st
		}
		if st.Iteration != nil && *st.Iteration == *iteration {
			return st
		}
	}
	t.Fatalf("no step %q (iteration %v) found in %+v", id, iteration, run.Steps)
	return domain.StepResult{}
}

// TestRun_Foreach_EmptyList confirms a block over an empty list runs zero
// iterations, passes, and reports count == 0.
func TestRun_Foreach_EmptyList(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "foreach-empty",
		Steps: []domain.Step{
			{ID: "each", Foreach: "[]", Steps: []domain.Step{createEachStep()}},
			{
				ID:     "verify",
				Call:   "allocation-service.allocate",
				Body:   map[string]any{"orderId": "o1"},
				Assert: []domain.Assertion{{Expr: "steps.each.count == 0"}, {Expr: "steps.each.iterations.size() == 0"}},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 2)

	block := findStepResult(t, run, "each", nil)
	assert.Equal(t, domain.StepPassed, block.Status)
	assert.Equal(t, 0, block.Count)

	verify := findStepResult(t, run, "verify", nil)
	assert.Equal(t, domain.StepPassed, verify.Status, "%+v", verify.Assertions)
	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_Foreach_ListOverMax_Fails confirms a foreach list longer than max
// fails the block before iterating at all -- it never truncates silently.
func TestRun_Foreach_ListOverMax_Fails(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "foreach-over-max",
		Steps: []domain.Step{
			{ID: "each", Foreach: "['a', 'b', 'c']", Max: 2, Steps: []domain.Step{createEachStep()}},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 1, "no iterations should have run: %+v", run.Steps)

	block := run.Steps[0]
	assert.Equal(t, domain.StepFailed, block.Status)
	assert.Equal(t, 0, block.Count)
	require.NotNil(t, block.Error)
	assert.Contains(t, block.Error.Message, "max")

	assert.Equal(t, domain.RunFailed, run.Status)
}

// TestRun_Repeat_Pagination_ReadsPreviousCursor exercises a repeat block
// whose nested step's body reads the previous iteration's own extracted
// value via steps.<nested-id> (guarded by has() on the first iteration),
// confirming "latest execution" semantics (PLAN §34f.8).
func TestRun_Repeat_Pagination_ReadsPreviousCursor(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "repeat-pagination",
		Steps: []domain.Step{
			{
				ID: "page",
				Repeat: &domain.Repeat{
					Until: "steps.fetch.out.n >= 3",
					Max:   10,
				},
				Steps: []domain.Step{
					{
						ID:   "fetch",
						Call: "order-service.createOrder",
						Body: map[string]any{
							"customerId": "${has(steps.fetch) ? string(steps.fetch.out.n + 1) : '1'}",
							"type":       "QCOM",
							"pickup":     map[string]any{"lat": 1, "lng": 2},
							"drop":       map[string]any{"lat": 1, "lng": 2},
						},
						Extract: map[string]string{"n": "int(request.body.customerId)"},
					},
				},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	block := findStepResult(t, run, "page", nil)
	assert.Equal(t, domain.StepPassed, block.Status, "%+v", block)
	assert.Equal(t, "repeat", block.Kind)
	assert.Equal(t, 3, block.Count)

	last := findStepResult(t, run, "fetch", intp(2))
	assert.EqualValues(t, 3, last.Out["n"])

	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_Repeat_WhileFalseAtStart confirms `while` checked before the
// first iteration can stop a repeat block at zero iterations.
func TestRun_Repeat_WhileFalseAtStart(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "repeat-while-false",
		Steps: []domain.Step{
			{ID: "each", Repeat: &domain.Repeat{While: "false", Max: 5}, Steps: []domain.Step{createEachStep()}},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 1)

	block := run.Steps[0]
	assert.Equal(t, domain.StepPassed, block.Status)
	assert.Equal(t, 0, block.Count)
	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_Block_BreakWhen confirms break_when ends the loop after the
// iteration in which it turns true, even though more items remain.
func TestRun_Block_BreakWhen(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "break-when",
		Steps: []domain.Step{
			{
				ID:        "each",
				Foreach:   "['a', 'b', 'c', 'd']",
				BreakWhen: "iter.index >= 1", // stop after the 2nd iteration (index 1)
				Steps:     []domain.Step{createEachStep()},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	block := findStepResult(t, run, "each", nil)
	assert.Equal(t, domain.StepPassed, block.Status)
	assert.Equal(t, 2, block.Count)
	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_Block_OnError_Continue confirms on_error: continue keeps
// iterating past a failed nested step, with the block's own final status
// still failed.
func TestRun_Block_OnError_Continue(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "on-error-continue",
		Steps: []domain.Step{
			{
				ID:      "each",
				Foreach: "['a', 'bad', 'c']",
				OnError: "continue",
				Steps: []domain.Step{
					{
						ID:     "create",
						Call:   "order-service.createOrder",
						Body:   map[string]any{"customerId": "${iter.item}", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}},
						Assert: []domain.Assertion{{Expr: "iter.item != 'bad'"}},
					},
				},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	block := findStepResult(t, run, "each", nil)
	assert.Equal(t, domain.StepFailed, block.Status)
	assert.Equal(t, 3, block.Count, "on_error: continue should run every iteration despite the failure")

	assert.Equal(t, domain.StepPassed, findStepResult(t, run, "create", intp(0)).Status)
	assert.Equal(t, domain.StepFailed, findStepResult(t, run, "create", intp(1)).Status)
	assert.Equal(t, domain.StepPassed, findStepResult(t, run, "create", intp(2)).Status)

	assert.Equal(t, domain.RunFailed, run.Status)
}

// TestRun_Block_OnError_StopIsDefault confirms the default on_error (stop)
// ends the block at the first failed iteration.
func TestRun_Block_OnError_StopIsDefault(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "on-error-stop",
		Steps: []domain.Step{
			{
				ID:      "each",
				Foreach: "['a', 'bad', 'c']",
				Steps: []domain.Step{
					{
						ID:     "create",
						Call:   "order-service.createOrder",
						Body:   map[string]any{"customerId": "${iter.item}", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}},
						Assert: []domain.Assertion{{Expr: "iter.item != 'bad'"}},
					},
				},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	block := findStepResult(t, run, "each", nil)
	assert.Equal(t, domain.StepFailed, block.Status)
	assert.Equal(t, 2, block.Count, "on_error: stop (default) should end the block at the failing iteration")

	for _, st := range run.Steps {
		if st.StepID == "create" && st.Iteration != nil && *st.Iteration == 2 {
			t.Fatalf("iteration 2 should never have run: %+v", st)
		}
	}

	assert.Equal(t, domain.RunFailed, run.Status)
}

// TestRun_Block_CancellationMidLoop confirms a run.Cancel()/context
// cancellation stops a loop promptly, part-way through.
func TestRun_Block_CancellationMidLoop(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "cancel-mid-loop",
		Steps: []domain.Step{
			{
				ID:     "each",
				Repeat: &domain.Repeat{While: "true", Max: 1000, Interval: "5ms"},
				// A repeat block has no iter.item (only foreach does); use a
				// constant body rather than createEachStep(), which reads
				// iter.item and would fail every iteration here.
				Steps: []domain.Step{{
					ID:     "create",
					Call:   "order-service.createOrder",
					Body:   map[string]any{"customerId": "c1", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}},
					Assert: []domain.Assertion{{Status: intp(201)}},
				}},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Millisecond)
	defer cancel()

	run, err := r.Run(ctx, f, nil, Options{Env: e})
	require.NoError(t, err)

	block := findStepResult(t, run, "each", nil)
	assert.Equal(t, domain.StepCancelled, block.Status, "%+v", block)
	assert.Less(t, block.Count, 1000, "the loop must not have run to completion")

	assert.Equal(t, domain.RunCancelled, run.Status)
}

// TestRun_Block_TeardownStillRuns confirms teardown still runs, and can
// still pass, after a loop block failed.
func TestRun_Block_TeardownStillRuns(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "block-teardown",
		Steps: []domain.Step{
			{
				ID:      "each",
				Foreach: "['bad']",
				Steps: []domain.Step{
					{
						ID:     "create",
						Call:   "order-service.createOrder",
						Body:   map[string]any{"customerId": "${iter.item}", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}},
						Assert: []domain.Assertion{{Expr: "status == 999"}},
					},
				},
			},
		},
		Teardown: []domain.Step{
			{ID: "cleanup", Call: "order-service.createOrder", Body: map[string]any{"customerId": "c", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}}, Assert: []domain.Assertion{{Status: intp(201)}}},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	block := findStepResult(t, run, "each", nil)
	assert.Equal(t, domain.StepFailed, block.Status)

	cleanup := findStepResult(t, run, "cleanup", nil)
	assert.Equal(t, "teardown", cleanup.Phase)
	assert.Equal(t, domain.StepPassed, cleanup.Status, "teardown must still run after a block failure")

	assert.Equal(t, domain.RunFailed, run.Status)
}

// TestRun_Block_WhenFalse_SkipsWholeBlock confirms `when: false` on a block
// skips it entirely: zero iterations, no nested executions, left out of
// steps entirely.
func TestRun_Block_WhenFalse_SkipsWholeBlock(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "block-when-false",
		Steps: []domain.Step{
			// A real, passing step alongside the skipped block: DeriveStatus
			// treats a run where nothing meaningful ran (only skipped steps,
			// none passed) as errored, same as a lone when:false call step
			// would be (PLAN §34f.7) -- not specific to blocks.
			createOrderStep(),
			{ID: "each", When: "false", Foreach: "['a', 'b']", Steps: []domain.Step{createEachStep()}},
		},
		Inputs: map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 2, "no nested executions should have run: %+v", run.Steps)

	block := findStepResult(t, run, "each", nil)
	assert.Equal(t, domain.StepSkipped, block.Status)
	assert.Equal(t, "when", block.SkipReason)
	assert.Equal(t, 0, block.Count)

	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_Block_NestedStepWhen confirms `when` on a NESTED step (not the
// block) can skip individual iterations while others still run.
func TestRun_Block_NestedStepWhen(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "nested-when",
		Steps: []domain.Step{
			{
				ID:      "each",
				Foreach: "['a', 'b', 'c']",
				Steps: []domain.Step{
					{
						ID:   "create",
						When: "iter.index != 1", // skip the middle iteration
						Call: "order-service.createOrder",
						Body: map[string]any{"customerId": "${iter.item}", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}},
					},
				},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	block := findStepResult(t, run, "each", nil)
	assert.Equal(t, domain.StepPassed, block.Status, "a skipped nested step never fails a run")
	assert.Equal(t, 3, block.Count)

	assert.Equal(t, domain.StepSkipped, findStepResult(t, run, "create", intp(1)).Status)
	assert.Equal(t, "when", findStepResult(t, run, "create", intp(1)).SkipReason)
	assert.Equal(t, domain.StepPassed, findStepResult(t, run, "create", intp(0)).Status)
	assert.Equal(t, domain.StepPassed, findStepResult(t, run, "create", intp(2)).Status)

	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_Block_MonotonicIndex confirms every StepResult across setup,
// main (including nested executions), and teardown gets a distinct,
// globally monotonic Index (PLAN §34f.8: "idx for nested results: keep a
// monotonically increasing execution index so ordering is stable") -- and
// that a block's own Index is lower than any of its nested executions',
// which is what makes `ORDER BY idx ASC` (loadSteps) show a block before
// its children on reload, even though the block's own StepResult is only
// appended to run.Steps once the whole loop finishes (so live, in-process
// run.Steps is in a different physical order: nested results first, then
// the block's own aggregated result).
func TestRun_Block_MonotonicIndex(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "monotonic-index",
		Setup:   []domain.Step{createOrderStep()},
		Steps: []domain.Step{
			{ID: "each", Foreach: "['a', 'b']", Steps: []domain.Step{createEachStep()}},
		},
		Teardown: []domain.Step{
			{ID: "cleanup", Call: "order-service.createOrder", Body: map[string]any{"customerId": "c", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}}},
		},
	}
	f.Inputs = map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	require.Len(t, run.Steps, 5) // setup create, block, 2 nested, teardown cleanup
	seen := map[int]string{}
	for _, st := range run.Steps {
		if other, dup := seen[st.Index]; dup {
			t.Fatalf("Index %d used by both %q and %q: %+v", st.Index, other, st.StepID, run.Steps)
		}
		seen[st.Index] = st.StepID
	}

	block := findStepResult(t, run, "each", nil)
	nested0 := findStepResult(t, run, "create", intp(0))
	nested1 := findStepResult(t, run, "create", intp(1))
	assert.Less(t, block.Index, nested0.Index, "a block's own Index must precede its nested executions'")
	assert.Less(t, block.Index, nested1.Index)
}

// ---- iterations memory budget (PLAN §34f.8) --------------------------------

func TestApproxStepValueSize_CountsBodyAndRequestOnly(t *testing.T) {
	sv := expr.StepValue{
		Status:  200,
		Headers: map[string]string{"content-type": "application/json"},
		Body:    map[string]any{"x": strings.Repeat("a", 1000)},
		Request: map[string]any{"body": strings.Repeat("b", 500)},
		Out:     map[string]any{"y": 1},
	}
	// Both Body and Request are marshaled; the exact byte count depends on
	// JSON quoting, but it must be in the right ballpark and far larger
	// than a StepValue with no body/request at all.
	assert.Greater(t, approxStepValueSize(sv), 1400)

	empty := expr.StepValue{Status: 200, Out: map[string]any{"y": 1}}
	assert.Less(t, approxStepValueSize(empty), 10)
}

func TestTrimStepValueBody_DropsBodyAndRequestKeepsRest(t *testing.T) {
	sv := expr.StepValue{
		Status: 201, LatencyMs: 12.5,
		Headers: map[string]string{"content-type": "application/json"},
		Body:    map[string]any{"orderId": "ord_1"},
		Request: map[string]any{"method": "POST"},
		Out:     map[string]any{"orderId": "ord_1"},
	}
	trimmed := trimStepValueBody(sv)
	assert.Nil(t, trimmed.Body)
	assert.Nil(t, trimmed.Request)
	assert.Equal(t, 201, trimmed.Status)
	assert.Equal(t, 12.5, trimmed.LatencyMs)
	assert.Equal(t, sv.Headers, trimmed.Headers)
	assert.Equal(t, sv.Out, trimmed.Out)
}

// TestRun_Foreach_IterationsBudget_TrimsBodiesPastLimit runs enough
// iterations with a large enough per-iteration response body to cross
// blockIterationsBudgetBytes partway through, and confirms:
// steps.<block>.iterations keeps full bodies for the early iterations
// (under budget) and only status/out (no body) for the later ones -- all
// while every iteration's own StepResult (the persisted one, read back via
// run.Steps, not the CEL-facing iterations value) keeps its real body
// regardless, since the budget is specific to that in-memory value.
func TestRun_Foreach_IterationsBudget_TrimsBodiesPastLimit(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	// ~300KB echoed back per response (order-service.createOrder echoes
	// customerId); 16 iterations is enough to cross the 4MB budget partway
	// through (14 * 300KB > 4MB) while still finishing quickly.
	big := strings.Repeat("x", 300*1024)
	const n = 16

	f := &domain.Flow{
		Version: 1,
		ID:      "iterations-budget",
		Steps: []domain.Step{
			{
				ID:      "each",
				Foreach: "[" + strings.Repeat("0,", n-1) + "0]", // n items; the value itself is unused
				Steps: []domain.Step{{
					ID:   "create",
					Call: "order-service.createOrder",
					Body: map[string]any{
						"customerId": big,
						"type":       "QCOM",
						"pickup":     map[string]any{"lat": 1, "lng": 2},
						"drop":       map[string]any{"lat": 1, "lng": 2},
					},
					Extract: map[string]string{"orderId": "body.orderId"},
					Assert:  []domain.Assertion{{Status: intp(201)}},
				}},
			},
			{
				ID:   "verify",
				Call: "allocation-service.allocate",
				Body: map[string]any{"orderId": "o1"},
				Assert: []domain.Assertion{
					// The first iteration is well under budget: its body
					// (the echoed customerId) must still be present.
					{Expr: "steps.each.iterations[0].create.body.customerId.size() > 100000"},
					// The last iteration is past budget: body is trimmed to
					// null (stepValueToNative always sets the "body" key,
					// so has() alone can't tell "trimmed" from "present but
					// null" -- checking the value itself can), but
					// status/out must still be there.
					{Expr: "steps.each.iterations[15].create.body == null"},
					{Expr: "steps.each.iterations[15].create.status == 201"},
					{Expr: "steps.each.iterations[15].create.out.orderId != ''"},
				},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps)

	verify := findStepResult(t, run, "verify", nil)
	for _, a := range verify.Assertions {
		assert.True(t, a.Passed, "assertion %q: %s %s", a.Expr, a.Message, a.Error)
	}

	// The PERSISTED StepResult for the last iteration keeps its real body
	// regardless -- the budget only trims the CEL-facing iterations value.
	last := findStepResult(t, run, "create", intp(n-1))
	require.NotNil(t, last.Response)
	assert.NotEmpty(t, last.Response.Body)
}
