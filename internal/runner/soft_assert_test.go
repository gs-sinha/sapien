package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

// A soft assertion that does not hold is recorded as a warning: the step
// and the run stay passed, the summary counts it as warned, not failed.
func TestRun_SoftAssertionDoesNotFailStep(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "soft-demo",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Steps: []domain.Step{
			createOrderStep(),
			{
				ID:    "check",
				Call:  "order-service.getOrder",
				Input: map[string]any{"orderId": "${steps.create.out.orderId}"},
				Assert: []domain.Assertion{
					{Expr: "status == 200"},
					{Expr: "status == 999", Soft: true, Message: "port tracking not wired yet"},
					{Status: intp(201), Soft: true},
				},
			},
		},
	}
	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status)
	require.Len(t, run.Steps, 2)
	check := run.Steps[1]
	assert.Equal(t, domain.StepPassed, check.Status)
	require.Len(t, check.Assertions, 3)
	assert.True(t, check.Assertions[0].Passed)
	assert.False(t, check.Assertions[1].Passed)
	assert.True(t, check.Assertions[1].Soft)
	assert.Equal(t, "port tracking not wired yet", check.Assertions[1].Message)
	assert.False(t, check.Assertions[2].Passed)
	assert.True(t, check.Assertions[2].Soft)
	assert.Equal(t, 4, run.Summary.Assertions, "create's own assertion plus the three on check")
	assert.Equal(t, 0, run.Summary.AssertionsFailed)
	assert.Equal(t, 2, run.Summary.AssertionsWarned)
}

// A hard assertion still fails the step when a soft one also fails.
func TestRun_HardAssertionStillFailsBesideSoft(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)
	f := &domain.Flow{
		Version: 1, ID: "soft-hard",
		Inputs: map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Steps: []domain.Step{createOrderStep(), {
			ID: "check", Call: "order-service.getOrder",
			Input:  map[string]any{"orderId": "${steps.create.out.orderId}"},
			Assert: []domain.Assertion{{Expr: "status == 500"}, {Expr: "status == 999", Soft: true}},
		}},
	}
	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunFailed, run.Status)
	assert.Equal(t, 1, run.Summary.AssertionsFailed)
	assert.Equal(t, 1, run.Summary.AssertionsWarned)
}
