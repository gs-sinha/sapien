package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// TestRun_When_False_SkipsStep confirms a step whose `when` evaluates false
// is recorded skipped (skip_reason "when"), sends no request, evaluates no
// assertions, and never fails the run (PLAN §34f.7).
func TestRun_When_False_SkipsStep(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "when-false",
		Steps: []domain.Step{
			createOrderStep(),
			{
				ID:     "skip-me",
				Call:   "allocation-service.allocate",
				When:   "false",
				Body:   map[string]any{"orderId": "${steps.create.out.orderId}"},
				Assert: []domain.Assertion{{Expr: "status == 999"}}, // would fail if it ever ran
			},
		},
		Inputs: map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 2)

	skipped := run.Steps[1]
	assert.Equal(t, domain.StepSkipped, skipped.Status)
	assert.Equal(t, "when", skipped.SkipReason)
	assert.Nil(t, skipped.Request, "a when:false step must send no request")
	assert.Nil(t, skipped.Response)
	assert.Empty(t, skipped.Assertions, "a when:false step must evaluate no assertions")

	assert.Equal(t, domain.RunPassed, run.Status, "a skipped step never fails the run")
	assert.Equal(t, 2, run.Summary.StepsTotal)
	assert.Equal(t, 1, run.Summary.StepsSkipped)
	assert.Equal(t, 1, run.Summary.StepsPassed)
}

// TestRun_When_True_RunsStepNormally confirms a `when` that evaluates true
// changes nothing about how the step runs.
func TestRun_When_True_RunsStepNormally(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "when-true",
		Steps: []domain.Step{
			createOrderStep(),
			{
				ID:     "run-me",
				Call:   "allocation-service.allocate",
				When:   "true",
				Body:   map[string]any{"orderId": "${steps.create.out.orderId}"},
				Until:  "status == 201",
				Poll:   &domain.Poll{Interval: "1ms", Timeout: "1s"},
				Assert: []domain.Assertion{{Status: intp(201)}},
			},
		},
		Inputs: map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 2)
	ran := run.Steps[1]
	assert.Equal(t, domain.StepPassed, ran.Status)
	assert.Empty(t, ran.SkipReason)
	require.NotNil(t, ran.Response)
	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_When_InputBasedCondition exercises `when` reading a flow input,
// the shape the DSL reference's worked example uses.
func TestRun_When_InputBasedCondition(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	buildFlow := func() *domain.Flow {
		return &domain.Flow{
			Version: 1,
			ID:      "when-input",
			Inputs: map[string]domain.InputSpec{
				"customerId": {Type: "string", Default: "cust_1"},
				"releaseNow": {Type: "boolean", Default: false},
			},
			Steps: []domain.Step{
				createOrderStep(),
				{
					ID:    "allocate",
					Call:  "allocation-service.allocate",
					Body:  map[string]any{"orderId": "${steps.create.out.orderId}"},
					Until: "status == 201",
					Poll:  &domain.Poll{Interval: "1ms", Timeout: "1s"},
					Extract: map[string]string{
						"allocationId": "body.allocationId",
					},
				},
				{
					ID:    "release",
					Call:  "allocation-service.releaseAllocation",
					When:  "inputs.releaseNow",
					Input: map[string]any{"allocationId": "${steps.allocate.out.allocationId}"},
				},
			},
		}
	}

	runFalse, err := r.Run(context.Background(), buildFlow(), map[string]any{"releaseNow": false}, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, runFalse.Steps, 3)
	assert.Equal(t, domain.StepSkipped, runFalse.Steps[2].Status)
	assert.Equal(t, "when", runFalse.Steps[2].SkipReason)

	runTrue, err := r.Run(context.Background(), buildFlow(), map[string]any{"releaseNow": true}, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, runTrue.Steps, 3)
	assert.Equal(t, domain.StepPassed, runTrue.Steps[2].Status, "%+v", runTrue.Steps[2])
}

// TestRun_When_EvalErrorFailsStepLikeBadAssert confirms a `when` that fails
// to evaluate (as opposed to evaluating false) errors the step, exactly
// like a bad assert expression, rather than silently skipping it.
func TestRun_When_EvalErrorFailsStepLikeBadAssert(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "when-eval-error",
		Steps: []domain.Step{
			{
				ID:   "a",
				Call: "order-service.createOrder",
				When: "inputs.doesNotExist", // a genuine runtime evaluation error, not a syntax error
				Body: map[string]any{"customerId": "c1", "type": "QCOM", "pickup": map[string]any{"lat": 1, "lng": 2}, "drop": map[string]any{"lat": 1, "lng": 2}},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 1)
	assert.Equal(t, domain.StepErrored, run.Steps[0].Status)
	assert.NotEqual(t, "when", run.Steps[0].SkipReason)
	require.NotNil(t, run.Steps[0].Error)
	assert.Equal(t, domain.RunErrored, run.Status)
}

// TestRun_When_SkippedStepNotInScope_UnguardedReferenceErrorsClearly
// confirms a when-skipped step is left out of `steps` entirely: a later
// step's unguarded reference to it produces the documented, friendly
// error text rather than silently seeing zero values.
func TestRun_When_SkippedStepNotInScope_UnguardedReferenceErrorsClearly(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "when-unguarded-ref",
		Steps: []domain.Step{
			{ID: "a", Call: "allocation-service.allocate", When: "false", Body: map[string]any{"orderId": "o1"}},
			{
				ID:     "b",
				Call:   "allocation-service.allocate",
				Body:   map[string]any{"orderId": "o1"},
				Assert: []domain.Assertion{{Expr: "steps.a.status == 201"}},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 2)
	assert.Equal(t, domain.StepSkipped, run.Steps[0].Status)

	b := run.Steps[1]
	require.Len(t, b.Assertions, 1)
	assert.False(t, b.Assertions[0].Passed)
	assert.Contains(t, b.Assertions[0].Error, "steps.a was skipped (when: false)")
	assert.Contains(t, b.Assertions[0].Error, "has(steps.a)")
}

// TestRun_When_SkippedStepGuardedReferenceEvaluatesCleanly is the has()
// counterpart: a guarded reference to a when-skipped step evaluates
// without error.
func TestRun_When_SkippedStepGuardedReferenceEvaluatesCleanly(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "when-guarded-ref",
		Steps: []domain.Step{
			{ID: "a", Call: "allocation-service.allocate", When: "false", Body: map[string]any{"orderId": "o1"}},
			{
				ID:     "b",
				Call:   "allocation-service.allocate",
				Body:   map[string]any{"orderId": "o1"},
				Assert: []domain.Assertion{{Expr: "!has(steps.a) || steps.a.status == 201"}},
			},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 2)
	b := run.Steps[1]
	require.Len(t, b.Assertions, 1)
	assert.True(t, b.Assertions[0].Passed, "%+v", b.Assertions[0])
	assert.Equal(t, domain.RunPassed, run.Status)
}

// TestRun_When_InSetupAndTeardown confirms `when` works in every phase.
func TestRun_When_InSetupAndTeardown(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "when-phases",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Setup: []domain.Step{
			createOrderStep(),
			{ID: "extra-setup", Call: "order-service.createOrder", When: "false", Body: createOrderStep().Body},
		},
		Steps: []domain.Step{
			{
				ID:     "allocate",
				Call:   "allocation-service.allocate",
				Body:   map[string]any{"orderId": "${steps.create.out.orderId}"},
				Assert: []domain.Assertion{{Status: intp(201)}},
			},
		},
		Teardown: []domain.Step{
			{ID: "skip-teardown", Call: "order-service.cancelOrder", When: "false", Input: map[string]any{"orderId": "${steps.create.out.orderId}"}},
		},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Len(t, run.Steps, 4)

	assert.Equal(t, domain.StepPassed, run.Steps[0].Status, "setup: create")
	assert.Equal(t, domain.StepSkipped, run.Steps[1].Status, "setup: extra-setup")
	assert.Equal(t, "when", run.Steps[1].SkipReason)
	assert.Equal(t, domain.StepPassed, run.Steps[2].Status, "main: allocate")
	assert.Equal(t, domain.StepSkipped, run.Steps[3].Status, "teardown: skip-teardown")
	assert.Equal(t, "when", run.Steps[3].SkipReason)
	assert.Equal(t, "teardown", run.Steps[3].Phase)

	assert.Equal(t, domain.RunPassed, run.Status)
	// Teardown is excluded from the summary; setup+main: create, extra-setup, allocate.
	assert.Equal(t, 3, run.Summary.StepsTotal)
	assert.Equal(t, 1, run.Summary.StepsSkipped)
}
