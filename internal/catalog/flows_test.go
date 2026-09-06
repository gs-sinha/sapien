package catalog_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
)

func TestFlowsUsingOperation(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	flows, err := c.FlowsUsingOperation(ctx, "rider-service.getRider")
	require.NoError(t, err)
	require.Len(t, flows, 1)
	assert.Equal(t, "Onboard Rider", flows[0].Name)

	flows2, err := c.FlowsUsingOperation(ctx, "order-service.createOrder")
	require.NoError(t, err)
	require.Len(t, flows2, 1)
	assert.Equal(t, "Checkout", flows2[0].Name)

	none, err := c.FlowsUsingOperation(ctx, "no-such-operation")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestGetFlowSummary(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	flow, err := c.GetFlowSummary(ctx, "rider-service/flows/onboard.yaml")
	require.NoError(t, err)
	require.NotNil(t, flow)
	assert.Equal(t, "service", flow.OwnerKind)
	assert.Equal(t, "rider-service", flow.OwnerID)
	assert.Equal(t, 2, flow.StepCount)
	assert.ElementsMatch(t, []string{"rider-service.createRider", "rider-service.getRider"}, flow.Operations)

	missing, err := c.GetFlowSummary(ctx, "no-such-flow")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestUpsertFlows_WorkspaceScopeReplacesWholesale(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	workspaceFlows := []domain.FlowSummary{
		{ID: "flows/smoke.yaml", Name: "Smoke test", Path: "flows/smoke.yaml", Operations: []string{"rider-service.getRider"}, StepCount: 1, Hash: "h1", Updated: time.Now().UTC()},
	}
	require.NoError(t, c.UpsertFlows(ctx, "workspace", "", workspaceFlows))

	got, err := c.ListFlows(ctx, "workspace", "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Smoke test", got[0].Name)

	// service-owned flows untouched.
	serviceFlows, err := c.ListFlows(ctx, "service", "")
	require.NoError(t, err)
	assert.Len(t, serviceFlows, 2)

	// Replacing again drops the old set.
	require.NoError(t, c.UpsertFlows(ctx, "workspace", "", nil))
	got2, err := c.ListFlows(ctx, "workspace", "")
	require.NoError(t, err)
	assert.Empty(t, got2)
}
