package runner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/flow"

	"github.com/gs-sinha/sapien/fixtures/logistics/mock"
)

// TestRun_FixtureSmokeFlow executes the real allocation-service smoke flow
// (fixtures/logistics/allocation-service/api/flows/smoke.flow.yaml) end to
// end against the logistics mock servers, exercising the fixture's
// `eq: "${steps.allocate.out.riderId}"` structured assertion for real: it
// must compare against the rider ID the allocate step actually extracted,
// not the literal template text (PLAN §8).
func TestRun_FixtureSmokeFlow(t *testing.T) {
	ops, e, world := startFixtures(t)
	r := New(ops)

	// allocate needs an existing order to allocate a rider to; seed one
	// directly in the shared World, the same way order-service.createOrder
	// would.
	order := world.CreateOrder("cust_1", "QCOM",
		mock.LatLng{Lat: 12.9716, Lng: 77.5946},
		mock.LatLng{Lat: 12.9352, Lng: 77.6146},
	)

	path := filepath.Join(fixturesRoot(t), "allocation-service", "api", "flows", "smoke.flow.yaml")
	f, err := flow.ParseFile(path)
	require.NoError(t, err)
	require.Equal(t, "smoke", f.ID)

	run, err := r.Run(context.Background(), f, map[string]any{"orderId": order.OrderID}, Options{Env: e})
	require.NoError(t, err)
	require.NotNil(t, run)

	for _, st := range run.Steps {
		for _, a := range st.Assertions {
			assert.True(t, a.Passed, "step %s assertion %q should pass: message=%q error=%q", st.StepID, a.Expr, a.Message, a.Error)
		}
	}
	assert.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps)
	require.Len(t, run.Steps, 2)

	allocate, check := run.Steps[0], run.Steps[1]
	assert.Equal(t, domain.StepPassed, allocate.Status)
	riderID, _ := allocate.Out["riderId"].(string)
	assert.NotEmpty(t, riderID)

	assert.Equal(t, domain.StepPassed, check.Status)
	require.Len(t, check.Assertions, 2)
	eqAssertion := check.Assertions[1]
	assert.True(t, eqAssertion.Passed)
	// The recorded Expr must show what was actually compared -- the
	// interpolated rider ID, not the raw "${...}" template text.
	assert.NotContains(t, eqAssertion.Expr, "${")
	assert.Contains(t, eqAssertion.Expr, riderID)
}

// TestRun_FixtureBulkAllocateFlow executes the real allocation-service
// bulk-allocate flow (fixtures/logistics/allocation-service/api/flows/
// bulk-allocate.flow.yaml) end to end against the logistics mock servers:
// a foreach block over several existing orders, a nested `when` that skips
// a "skip-me" sentinel item, and a step after the loop reading the block's
// count and its last iteration's own extracted value (PLAN §34f.7-8).
func TestRun_FixtureBulkAllocateFlow(t *testing.T) {
	ops, e, world := startFixtures(t)
	r := New(ops)

	// The World seeds exactly two online, qcomSkill riders (R123, R126,
	// see fixtures/logistics/mock/world.go); two QCOM orders is as many as
	// can be allocated without a release step in between.
	var orderIDs []any
	for i := 0; i < 2; i++ {
		order := world.CreateOrder("cust_1", "QCOM",
			mock.LatLng{Lat: 12.9716, Lng: 77.5946},
			mock.LatLng{Lat: 12.9352, Lng: 77.6146},
		)
		orderIDs = append(orderIDs, order.OrderID)
	}
	// A sentinel the flow's nested `when` skips outright, never calling
	// allocate for it -- so it must not appear in the mock's allocations.
	orderIDs = append(orderIDs, "skip-me")

	path := filepath.Join(fixturesRoot(t), "allocation-service", "api", "flows", "bulk-allocate.flow.yaml")
	f, err := flow.ParseFile(path)
	require.NoError(t, err)
	require.Equal(t, "bulk-allocate", f.ID)

	run, err := r.Run(context.Background(), f, map[string]any{"orderIds": orderIDs}, Options{Env: e})
	require.NoError(t, err)
	require.NotNil(t, run)

	assert.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps)

	block := findStepResult(t, run, "allocate_each", nil)
	assert.Equal(t, domain.StepPassed, block.Status, "%+v", run.Steps)
	assert.Equal(t, "foreach", block.Kind)
	assert.Equal(t, 3, block.Count, "one iteration per order id, including the skipped sentinel")

	for i := 0; i < 2; i++ {
		allocate := findStepResult(t, run, "allocate", intp(i))
		assert.Equal(t, domain.StepPassed, allocate.Status)
		check := findStepResult(t, run, "check", intp(i))
		assert.Equal(t, domain.StepPassed, check.Status)
	}
	skipped := findStepResult(t, run, "allocate", intp(2))
	assert.Equal(t, domain.StepSkipped, skipped.Status)
	assert.Equal(t, "when", skipped.SkipReason)

	summary := findStepResult(t, run, "summary", nil)
	assert.Equal(t, domain.StepPassed, summary.Status, "%+v", summary.Assertions)
	for _, a := range summary.Assertions {
		assert.True(t, a.Passed, "assertion %q: %s %s", a.Expr, a.Message, a.Error)
	}
}
