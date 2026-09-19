package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// blockFlowForResume is a flow with a foreach block ("each", nested step
// "create") followed by a plain step ("after"), used by the resume/block
// interaction tests below (PLAN §34f.8).
func blockFlowForResume() *domain.Flow {
	return &domain.Flow{
		Version: 1,
		ID:      "resume-block",
		Steps: []domain.Step{
			{
				ID:      "each",
				Foreach: "['a', 'b']",
				Steps:   []domain.Step{createEachStep()},
			},
			{
				ID:     "after",
				Call:   "allocation-service.allocate",
				Body:   map[string]any{"orderId": "o1"},
				Assert: []domain.Assertion{{Status: intp(201)}},
			},
		},
	}
}

// ---- buildResumePlan: resume only ever lands at a block's boundary ------

func TestBuildResumePlan_FromStepNamesNestedStep_Invalid(t *testing.T) {
	f := blockFlowForResume()
	_, err := buildResumePlan(f, &Resume{FromStep: "create"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `from_step "create" is a step inside block "each"`)
}

func TestBuildResumePlan_UntilStepNamesNestedStep_Invalid(t *testing.T) {
	f := blockFlowForResume()
	_, err := buildResumePlan(f, &Resume{UntilStep: "create"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `until_step "create" is a step inside block "each"`)
}

func TestBuildResumePlan_FromStepNamesBlock_Works(t *testing.T) {
	f := blockFlowForResume()
	plan, err := buildResumePlan(f, &Resume{FromStep: "each"})
	require.NoError(t, err)
	assert.Equal(t, 0, plan.fromIdx, "each is f.Steps[0]")
}

func TestBuildResumePlan_UntilStepNamesBlock_Works(t *testing.T) {
	f := blockFlowForResume()
	plan, err := buildResumePlan(f, &Resume{UntilStep: "each"})
	require.NoError(t, err)
	assert.Equal(t, 0, plan.untilIdx)
}

// TestDefaultResumeIndex_LandsAtBlockBoundary confirms the DEFAULT resume
// point (From set, FromStep not) lands at a failed block's own position,
// never at one of its nested executions -- even though nested results
// also have Phase "" and appear earlier in From.Steps than the block's own
// aggregated result (PLAN §34f.8: the block's own row is appended to
// run.Steps only after every iteration finishes).
func TestDefaultResumeIndex_LandsAtBlockBoundary(t *testing.T) {
	f := blockFlowForResume()
	iter := func(n int) *int { return &n }
	earlier := &domain.Run{
		ID: "run_1", FlowID: "resume-block",
		Steps: []domain.StepResult{
			{StepID: "create", Iteration: iter(0), Parent: "each", Status: domain.StepPassed},
			{StepID: "create", Iteration: iter(1), Parent: "each", Status: domain.StepFailed},
			{StepID: "each", Kind: "foreach", Count: 2, Status: domain.StepFailed},
		},
	}
	idx := defaultResumeIndex(f, earlier)
	assert.Equal(t, 0, idx, "must resolve to the block's own position (each, index 0), not skip past it")
}

// ---- reuseBlock: resuming a block reuses every nested execution ---------

// TestRun_ResumeBlock_ReusesEveryIterationAndRebuildsScope runs a flow with
// a foreach block once, then resumes from the step AFTER it (so the block
// itself falls in the reused window): every nested execution the earlier
// run recorded must be replayed as Reused StepResults (not re-executed --
// re-executing would double the mock's side effects, which this test does
// not seed for a second pass), and steps.<block>.count/iterations must be
// rebuilt so the resumed run's own steps can still read them.
func TestRun_ResumeBlock_ReusesEveryIterationAndRebuildsScope(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "resume-block-reuse",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Steps: []domain.Step{
			createOrderStep(),
			{ID: "each", Foreach: "['a', 'b', 'c']", Steps: []domain.Step{createEachStep()}},
			{
				ID:   "after",
				Call: "allocation-service.allocate",
				Body: map[string]any{"orderId": "${steps.create.out.orderId}"},
				Assert: []domain.Assertion{
					{Expr: "steps.each.count == 3"},
					{Status: intp(201)},
				},
			},
		},
	}

	first, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.RunPassed, first.Status, "%+v", first.Steps)

	second, err := r.Run(context.Background(), f, nil, Options{
		Env:    e,
		Resume: &Resume{From: first, FromStep: "after"},
	})
	require.NoError(t, err)
	require.Equal(t, domain.RunPassed, second.Status, "%+v", second.Steps)

	block := findStepResult(t, second, "each", nil)
	assert.True(t, block.Reused, "the block's own row must be reused, not re-executed")
	assert.Equal(t, 3, block.Count)

	for i := 0; i < 3; i++ {
		nested := findStepResult(t, second, "create", intp(i))
		assert.True(t, nested.Reused, "every nested execution must be reused")
		assert.Equal(t, "each", nested.Parent)
	}

	after := findStepResult(t, second, "after", nil)
	assert.False(t, after.Reused, "after falls at/after the resume point, so it re-executes")
	assert.Equal(t, domain.StepPassed, after.Status, "%+v", after.Assertions)
	for _, a := range after.Assertions {
		assert.True(t, a.Passed, "assertion %q: %s %s", a.Expr, a.Message, a.Error)
	}
}
