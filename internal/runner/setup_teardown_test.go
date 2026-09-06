package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// TestRun_SetupAndTeardown_HappyPath exercises the full setup -> steps ->
// teardown pipeline (PLAN §9): setup's output is usable by a main step,
// teardown's output is usable too, phases are recorded, and the summary
// only counts setup+main steps.
func TestRun_SetupAndTeardown_HappyPath(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "setup-teardown-happy",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Setup:   []domain.Step{createOrderStep()},
		Steps: []domain.Step{{
			ID:      "allocate",
			Call:    "allocation-service.allocate",
			Body:    map[string]any{"orderId": "${steps.create.out.orderId}"},
			Assert:  []domain.Assertion{{Status: intp(201)}},
			Extract: map[string]string{"allocationId": "body.allocationId", "riderId": "body.riderId"},
		}},
		Teardown: []domain.Step{{
			ID:     "release",
			Call:   "allocation-service.releaseAllocation",
			Input:  map[string]any{"allocationId": "${steps.allocate.out.allocationId}"},
			Assert: []domain.Assertion{{Status: intp(200)}},
		}},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.NotNil(t, run)

	assert.Equal(t, domain.RunPassed, run.Status)
	require.Len(t, run.Steps, 3)

	create, allocate, release := run.Steps[0], run.Steps[1], run.Steps[2]
	assert.Equal(t, "setup", create.Phase)
	assert.Equal(t, domain.StepPassed, create.Status)
	assert.NotEmpty(t, create.Out["orderId"])

	assert.Equal(t, "", allocate.Phase, "main steps leave Phase empty (domain: \"empty means steps\")")
	assert.Equal(t, domain.StepPassed, allocate.Status)
	assert.NotEmpty(t, allocate.Out["allocationId"])

	assert.Equal(t, "teardown", release.Phase)
	assert.Equal(t, domain.StepPassed, release.Status, "teardown can use the main step's extracted output")

	// Teardown is excluded from the summary (PLAN §9): only setup+main count.
	assert.Equal(t, 2, run.Summary.StepsTotal)
	assert.Equal(t, 2, run.Summary.StepsPassed)
}

// TestRun_TeardownRunsAfterMainStepFailure confirms teardown still runs,
// and can still pass, even though a main step failed -- and that its
// outcome does not change the run's overall (failed) status.
func TestRun_TeardownRunsAfterMainStepFailure(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "teardown-after-failure",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Setup:   []domain.Step{createOrderStep()},
		Steps: []domain.Step{{
			ID:     "allocate",
			Call:   "allocation-service.allocate",
			Body:   map[string]any{"orderId": "${steps.create.out.orderId}"},
			Assert: []domain.Assertion{{Expr: "status == 999"}}, // deliberately false
		}},
		Teardown: []domain.Step{{
			ID:     "cancel",
			Call:   "order-service.cancelOrder",
			Input:  map[string]any{"orderId": "${steps.create.out.orderId}"},
			Assert: []domain.Assertion{{Status: intp(200)}},
		}},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	require.Len(t, run.Steps, 3)
	assert.Equal(t, domain.StepFailed, run.Steps[1].Status)
	assert.Equal(t, "teardown", run.Steps[2].Phase)
	assert.Equal(t, domain.StepPassed, run.Steps[2].Status, "teardown still runs, and can still pass, after a main step failed")

	// The run's own status/summary reflect the main step's failure, not
	// teardown's (unaffected) success.
	assert.Equal(t, domain.RunFailed, run.Status)
	assert.Equal(t, 2, run.Summary.StepsTotal)
	assert.Equal(t, 1, run.Summary.StepsFailed)
}

// TestRun_TeardownContinuesPastOwnFailure confirms a failing teardown step
// does not stop later teardown steps from running.
func TestRun_TeardownContinuesPastOwnFailure(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "teardown-continues",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Setup:   []domain.Step{createOrderStep()},
		Steps: []domain.Step{{
			ID:     "allocate",
			Call:   "allocation-service.allocate",
			Body:   map[string]any{"orderId": "${steps.create.out.orderId}"},
			Assert: []domain.Assertion{{Status: intp(201)}},
		}},
		Teardown: []domain.Step{
			{
				ID:     "release-bogus",
				Call:   "allocation-service.releaseAllocation",
				Input:  map[string]any{"allocationId": "does-not-exist"},
				Assert: []domain.Assertion{{Status: intp(200)}}, // the mock 404s; this fails
			},
			{
				ID:     "cancel",
				Call:   "order-service.cancelOrder",
				Input:  map[string]any{"orderId": "${steps.create.out.orderId}"},
				Assert: []domain.Assertion{{Status: intp(200)}},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	require.Len(t, run.Steps, 4)
	assert.Equal(t, domain.StepPassed, run.Steps[1].Status, "the main step itself passed")
	releaseBogus, cancel := run.Steps[2], run.Steps[3]
	assert.Equal(t, "teardown", releaseBogus.Phase)
	assert.Equal(t, domain.StepFailed, releaseBogus.Status)
	assert.Equal(t, "teardown", cancel.Phase)
	assert.Equal(t, domain.StepPassed, cancel.Status, "a later teardown step still runs after an earlier one failed")

	// A failing teardown step never changes the run's overall status.
	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_TeardownRunsAfterCancellation confirms teardown still runs when
// the run itself was cancelled mid-flight.
func TestRun_TeardownRunsAfterCancellation(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "teardown-after-cancel",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Setup:   []domain.Step{createOrderStep()},
		Steps: []domain.Step{{
			ID:   "allocate",
			Call: "allocation-service.allocate",
			Body: map[string]any{"orderId": "${steps.create.out.orderId}"},
		}},
		Teardown: []domain.Step{{
			ID:     "cancel",
			Call:   "order-service.cancelOrder",
			Input:  map[string]any{"orderId": "${steps.create.out.orderId}"},
			Assert: []domain.Assertion{{Status: intp(200)}},
		}},
	}

	// A context already cancelled before the run starts: setup and steps
	// never get to run at all, but teardown must still run because it
	// uses the outer (here, uncancelled-by-Run) context, not the run's
	// own cancellable one.
	ctx, cancelCtx := context.WithCancel(context.Background())
	cancelCtx()

	run, err := r.Run(ctx, f, nil, Options{Env: e})
	require.NoError(t, err)

	assert.Equal(t, domain.RunCancelled, run.Status)
	require.Len(t, run.Steps, 3)
	assert.Equal(t, domain.StepSkipped, run.Steps[0].Status, "setup never ran: the context was already done")
	assert.Equal(t, domain.StepSkipped, run.Steps[1].Status)
	assert.Equal(t, "teardown", run.Steps[2].Phase)
	// Teardown was attempted on a context detached from the cancellation
	// (it is not skipped), but this teardown step depends on setup output
	// that never existed, so it errors and names the missing step. A
	// teardown that does not depend on setup would pass here.
	assert.NotEqual(t, domain.StepSkipped, run.Steps[2].Status, "teardown must run after cancellation")
	assert.Equal(t, domain.StepErrored, run.Steps[2].Status)
	require.NotNil(t, run.Steps[2].Error)
	assert.Contains(t, run.Steps[2].Error.Message, "create")
}
