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
