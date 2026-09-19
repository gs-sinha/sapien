package mcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/domain"
)

// TestBuildRunView_ReusedAndWarnings covers a resumed run (engine.RunOptions
// .ResumeFrom): a reused step must carry Reused=true and its Warnings
// through to the StepView, and renderRunText must surface both so an agent
// reading the run's text alone (no structured content support) still sees
// which steps were replayed rather than executed.
func TestBuildRunView_ReusedAndWarnings(t *testing.T) {
	run := &domain.Run{
		ID: "run_2", FlowID: "qcom-allocation", Environment: "staging", Status: domain.RunPassed,
		Steps: []domain.StepResult{
			{
				StepID: "create", Index: 0, Operation: "order-service.createOrder", Status: domain.StepPassed,
				Reused: true, ReusedFromRun: "run_1", Warnings: []string{"step definition changed since the reused run"},
				Started: time.Now(), Finished: time.Now(),
			},
			{
				StepID: "allocate", Index: 1, Operation: "allocation-service.allocate", Status: domain.StepPassed,
				Started: time.Now(), Finished: time.Now(),
			},
		},
		Summary: domain.RunSummary{StepsTotal: 2, StepsPassed: 2},
	}

	rv := buildRunView(run, "", false, false)
	require := assert.New(t)
	require.True(rv.Steps[0].Reused)
	require.Equal([]string{"step definition changed since the reused run"}, rv.Steps[0].Warnings)
	require.False(rv.Steps[1].Reused)
	require.Empty(rv.Steps[1].Warnings)

	text := renderRunText(rv)
	assert.Contains(t, text, "create (order-service.createOrder): passed (reused)")
	assert.Contains(t, text, "warning: step definition changed since the reused run")
	assert.Contains(t, text, "allocate (allocation-service.allocate): passed")
	assert.NotContains(t, text, "allocate (allocation-service.allocate): passed (reused)")
}

// iterp is intp for this file's loop-block tests (PLAN §34f.8).
func iterp(n int) *int { return &n }

// TestCollapseIterations_AllPassed_OneCollapsedLine confirms every
// iteration of one nested step id that passed folds into a single
// synthetic entry, and the block's own StepView is untouched.
func TestCollapseIterations_AllPassed_OneCollapsedLine(t *testing.T) {
	rv := RunView{Steps: []StepView{
		{StepID: "each", Status: "passed", Kind: "foreach", Count: 3},
		{StepID: "create", Parent: "each", Iteration: iterp(0), Status: "passed"},
		{StepID: "create", Parent: "each", Iteration: iterp(1), Status: "passed"},
		{StepID: "create", Parent: "each", Iteration: iterp(2), Status: "passed"},
	}}

	out := collapseIterations(rv)
	require := assert.New(t)
	require.Len(out.Steps, 2, "%+v", out.Steps)
	require.Equal("each", out.Steps[0].StepID)
	require.Equal("create", out.Steps[1].StepID)
	require.Equal(3, out.Steps[1].Collapsed)
	require.Equal("passed", out.Steps[1].Status)
	require.Nil(out.Steps[1].Iteration)

	text := renderRunText(out)
	assert.Contains(t, text, "- create (parent each): x3 passed\n")
}

// TestCollapseIterations_OneFailed_KeepsFullDetailForThatIteration confirms
// a mix of outcomes keeps the failed iteration's own full StepView (PLAN
// §34f.8: "include full detail only for failed iterations") alongside one
// collapsed entry for the ones that passed.
func TestCollapseIterations_OneFailed_KeepsFullDetailForThatIteration(t *testing.T) {
	rv := RunView{Steps: []StepView{
		{StepID: "each", Status: "failed", Kind: "repeat", Count: 3},
		{StepID: "fetch", Parent: "each", Iteration: iterp(0), Status: "passed"},
		{
			StepID: "fetch", Parent: "each", Iteration: iterp(1), Status: "failed",
			Assertions: []domain.AssertionResult{{Expr: "status == 200", Passed: false}},
		},
		{StepID: "fetch", Parent: "each", Iteration: iterp(2), Status: "passed"},
	}}

	out := collapseIterations(rv)
	require := assert.New(t)
	require.Len(out.Steps, 3, "%+v", out.Steps)
	require.Equal("each", out.Steps[0].StepID)

	failed := out.Steps[1]
	require.Equal("fetch", failed.StepID)
	require.Equal("failed", failed.Status)
	require.NotNil(failed.Iteration)
	require.Equal(1, *failed.Iteration)
	require.Zero(failed.Collapsed, "a failed iteration is kept individually, not collapsed")
	require.Len(failed.Assertions, 1, "full detail (assertions) survives for the failed iteration")

	collapsed := out.Steps[2]
	require.Equal("fetch", collapsed.StepID)
	require.Equal(2, collapsed.Collapsed)
	require.Equal("passed", collapsed.Status)

	text := renderRunText(out)
	assert.Contains(t, text, "- fetch (): failed, iteration 1")
	assert.Contains(t, text, "- fetch (parent each): x2 passed\n")
}

// TestRunViewForDetail_CollapsesInSummaryAndFailedButNotFull confirms
// run_flow's summary/failed detail modes collapse a block's nested step
// id, and full does not (every iteration's own StepView survives).
func TestRunViewForDetail_CollapsesInSummaryAndFailedButNotFull(t *testing.T) {
	run := &domain.Run{
		ID: "run_1", Status: domain.RunPassed,
		Steps: []domain.StepResult{
			{StepID: "each", Status: domain.StepPassed, Kind: "foreach", Count: 2},
			{StepID: "create", Parent: "each", Iteration: iterp(0), Status: domain.StepPassed, Operation: "order-service.createOrder"},
			{StepID: "create", Parent: "each", Iteration: iterp(1), Status: domain.StepPassed, Operation: "order-service.createOrder"},
		},
		Summary: domain.RunSummary{StepsTotal: 2, StepsPassed: 2},
	}

	summary := runViewForDetail(run, "summary")
	assert.Len(t, summary.Steps, 2, "the block plus one collapsed entry for its nested step id")

	failed := runViewForDetail(run, "failed")
	assert.Len(t, failed.Steps, 2)

	full := runViewForDetail(run, "full")
	assert.Len(t, full.Steps, 3, "full keeps every iteration's own StepView")
}
