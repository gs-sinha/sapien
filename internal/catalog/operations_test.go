package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func TestResolveOperation_AllForms(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	tests := []struct {
		name   string
		ref    string
		wantID string
	}{
		{"full id", "rider-service.getRider", "rider-service.getRider"},
		{"method+path exact alias", "GET /v1/riders", "rider-service.listRiders"},
		{"method+path templated", "GET /v1/riders/R123", "rider-service.getRider"},
		{"bare path any method", "/v1/orders/O1", "order-service.getOrder"},
		{"bare operationId", "getRider", "rider-service.getRider"},
		{"bare operationId case-insensitive", "GetRider", "rider-service.getRider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, err := c.ResolveOperation(ctx, tt.ref)
			require.NoError(t, err)
			require.NotNil(t, op)
			assert.Equal(t, tt.wantID, op.ID)
		})
	}
}

func TestResolveOperation_ConflictOnBareOperationID(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	// Both rider-service and order-service have a "getStatus" operation.
	_, err := c.ResolveOperation(ctx, "getStatus")
	require.Error(t, err)
	e := errs.As(err)
	assert.Equal(t, errs.Conflict, e.Code)
	candidates, _ := e.Details["candidates"].([]string)
	assert.ElementsMatch(t, []string{"order-service.getStatus", "rider-service.getStatus"}, candidates)
}

func TestResolveOperation_ConflictOnBarePath(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()

	snap := catalog.Snapshot{
		Service: domain.Service{ID: "widget-service", Name: "widget-service"},
		Operations: []domain.Operation{
			{ID: "widget-service.getWidget", ServiceID: "widget-service", Protocol: domain.ProtocolHTTP,
				HTTP: &domain.HTTPBinding{Method: "GET", Path: "/v1/widgets"}, RawOpID: "getWidget", Hash: "h1"},
			{ID: "widget-service.createWidget", ServiceID: "widget-service", Protocol: domain.ProtocolHTTP,
				HTTP: &domain.HTTPBinding{Method: "POST", Path: "/v1/widgets"}, RawOpID: "createWidget", Hash: "h2"},
		},
		Aliases: []domain.Alias{
			{Method: "GET", Path: "/v1/widgets", OperationID: "widget-service.getWidget"},
			{Method: "POST", Path: "/v1/widgets", OperationID: "widget-service.createWidget"},
		},
	}
	_, err := c.Apply(ctx, snap)
	require.NoError(t, err)

	_, err = c.ResolveOperation(ctx, "/v1/widgets")
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}

func TestResolveOperation_NotFound(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	_, err := c.ResolveOperation(ctx, "totallyUnknownOperation")
	require.Error(t, err)
	assert.Equal(t, errs.OperationNotFound, errs.CodeOf(err))
}

func TestGetOperation_NotFoundWithSuggestions(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	_, err := c.GetOperation(ctx, "rider-service.getRiders")
	require.Error(t, err)
	e := errs.As(err)
	assert.Equal(t, errs.OperationNotFound, e.Code)
	suggestions, ok := e.Details["suggestions"].([]string)
	require.True(t, ok)
	require.NotEmpty(t, suggestions)
	assert.Equal(t, "rider-service.getRider", suggestions[0])
}

func TestResolveOperation_SynthesizedBareID(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()

	// No operationId in the contract: RawOpID is empty and the ID is
	// synthesized from method+path (PLAN §5). The bare-operationId lookup
	// must fall back to the ID's suffix in that case.
	snap := catalog.Snapshot{
		Service: domain.Service{ID: "synth-service", Name: "synth-service"},
		Operations: []domain.Operation{
			{
				ID: "synth-service.post_v1_things", ServiceID: "synth-service", Protocol: domain.ProtocolHTTP,
				HTTP: &domain.HTTPBinding{Method: "POST", Path: "/v1/things"}, Synthesized: true,
				Hash: "h1",
			},
		},
	}
	_, err := c.Apply(ctx, snap)
	require.NoError(t, err)

	op, err := c.ResolveOperation(ctx, "post_v1_things")
	require.NoError(t, err)
	assert.Equal(t, "synth-service.post_v1_things", op.ID)

	opUpper, err := c.ResolveOperation(ctx, "POST_V1_THINGS")
	require.NoError(t, err)
	assert.Equal(t, "synth-service.post_v1_things", opUpper.ID)
}

func TestListOperations_Ordering(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	ops, err := c.ListOperations(ctx, "")
	require.NoError(t, err)
	require.Len(t, ops, 6)
	for i := 1; i < len(ops); i++ {
		prev, cur := ops[i-1], ops[i]
		if prev.ServiceID != cur.ServiceID {
			assert.Less(t, prev.ServiceID, cur.ServiceID)
			continue
		}
		if prev.HTTP.Path != cur.HTTP.Path {
			assert.Less(t, prev.HTTP.Path, cur.HTTP.Path)
			continue
		}
		assert.LessOrEqual(t, prev.HTTP.Method, cur.HTTP.Method)
	}
}
