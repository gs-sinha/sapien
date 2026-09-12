package local

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/registry"
	"github.com/gs-sinha/sapien/internal/search"
	"github.com/gs-sinha/sapien/internal/store"
)

func TestCatalogIndexer_ReviewTasksEmitsUndiscoverableOperation(t *testing.T) {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cat := catalog.New(db)
	idx := &catalogIndexer{cat: cat, srch: search.New(db)}
	op := domain.Operation{ID: "delivery.completeTrip", ServiceID: "delivery", Protocol: domain.ProtocolHTTP, HTTP: &domain.HTTPBinding{Method: "POST", Path: "/trips/complete"}, RawOpID: "completeTrip", Summary: "Finalize trip", Hash: "one"}
	task := domain.Task{ID: "complete-delivery", Phrases: []string{"mark a delivery as delivered"}, Targets: []domain.TaskTarget{{Operation: op.ID}}, Tests: []domain.TaskTest{{Query: "capture doorstep evidence", ExpectAny: []string{op.ID}, TopK: 3}}}
	snap := registry.Snapshot{Service: domain.Service{ID: "delivery", Name: "delivery", Tasks: []domain.Task{task}}, Operations: []domain.Operation{op}, Tasks: []domain.Task{task}}
	_, err = idx.Apply(t.Context(), snap)
	require.NoError(t, err)

	svc, err := idx.ReviewTasks(t.Context(), snap)
	require.NoError(t, err)
	require.NotNil(t, svc.TaskCoverage)
	assert.Equal(t, 1, svc.TaskCoverage.Assertions)
	assert.Zero(t, svc.TaskCoverage.Discoverable)
	require.Len(t, svc.Warnings, 1)
	assert.Equal(t, "UNDISCOVERABLE_OPERATION", svc.Warnings[0].Code)

	persisted, err := cat.GetService(t.Context(), "delivery")
	require.NoError(t, err)
	require.Len(t, persisted.Warnings, 1)
	assert.Equal(t, "UNDISCOVERABLE_OPERATION", persisted.Warnings[0].Code)

	snap.Service.WarningRules = []domain.AcceptedWarning{{Code: "UNDISCOVERABLE_OPERATION", Match: "capture doorstep evidence", Reason: "semantic-only vocabulary is accepted for now"}}
	_, err = idx.Apply(t.Context(), snap)
	require.NoError(t, err)
	svc, err = idx.ReviewTasks(t.Context(), snap)
	require.NoError(t, err)
	assert.Empty(t, svc.Warnings)
	require.Len(t, svc.AcceptedWarnings, 1)
	assert.Equal(t, "semantic-only vocabulary is accepted for now", svc.AcceptedWarnings[0].Reason)
}
