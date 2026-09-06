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
