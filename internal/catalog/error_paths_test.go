package catalog_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/catalog"
	"github.com/growsimplee/sapien/internal/domain"
)

// canceledContext returns a context that is already canceled, so any query
// issued against it fails deterministically and immediately (verified
// against the modernc.org/sqlite driver: database/sql checks ctx.Err()
// before ever reaching the driver). Used to exercise the error-wrapping
// branches of every read method without needing to fake a broken database.
func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestReadMethods_PropagateContextErrors(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	bad := canceledContext()

	t.Run("ListServices", func(t *testing.T) {
		_, err := c.ListServices(bad)
		assert.Error(t, err)
	})
	t.Run("GetService", func(t *testing.T) {
		_, err := c.GetService(bad, "rider-service")
		assert.Error(t, err)
	})
	t.Run("ListOperations", func(t *testing.T) {
		_, err := c.ListOperations(bad, "")
		assert.Error(t, err)
	})
	t.Run("GetOperation", func(t *testing.T) {
		_, err := c.GetOperation(bad, "rider-service.getRider")
		assert.Error(t, err)
	})
	t.Run("ResolveOperation full id", func(t *testing.T) {
		_, err := c.ResolveOperation(bad, "rider-service.getRider")
		assert.Error(t, err)
	})
	t.Run("ResolveOperation method+path", func(t *testing.T) {
		_, err := c.ResolveOperation(bad, "GET /v1/riders")
		assert.Error(t, err)
	})
	t.Run("ResolveOperation bare path", func(t *testing.T) {
		_, err := c.ResolveOperation(bad, "/v1/riders")
		assert.Error(t, err)
	})
	t.Run("ResolveOperation bare operationId", func(t *testing.T) {
		_, err := c.ResolveOperation(bad, "getRider")
		assert.Error(t, err)
	})
	t.Run("Fields", func(t *testing.T) {
		_, err := c.Fields(bad, "rider-service.getRider")
		assert.Error(t, err)
	})
	t.Run("FieldsByName", func(t *testing.T) {
		_, err := c.FieldsByName(bad, "rider-service", "riderId")
		assert.Error(t, err)
	})
	t.Run("GetSchema", func(t *testing.T) {
		_, err := c.GetSchema(bad, "rider-service", "Rider")
		assert.Error(t, err)
	})
	t.Run("ListSchemas", func(t *testing.T) {
		_, err := c.ListSchemas(bad, "")
		assert.Error(t, err)
	})
	t.Run("ListDocs", func(t *testing.T) {
		_, err := c.ListDocs(bad, "")
		assert.Error(t, err)
	})
	t.Run("GetDoc", func(t *testing.T) {
		_, err := c.GetDoc(bad, "rider-service", "docs/allocation.md")
		assert.Error(t, err)
	})
	t.Run("GetDocSection", func(t *testing.T) {
		_, _, err := c.GetDocSection(bad, "rider-service/docs/allocation.md#overview")
		assert.Error(t, err)
	})
	t.Run("DocsReferencing", func(t *testing.T) {
		_, err := c.DocsReferencing(bad, domain.RefOperation, "rider-service.getRider", 0)
		assert.Error(t, err)
	})
	t.Run("ListFlows", func(t *testing.T) {
		_, err := c.ListFlows(bad, "", "")
		assert.Error(t, err)
	})
	t.Run("GetFlowSummary", func(t *testing.T) {
		_, err := c.GetFlowSummary(bad, "rider-service/flows/onboard.yaml")
		assert.Error(t, err)
	})
	t.Run("FlowsUsingOperation", func(t *testing.T) {
		_, err := c.FlowsUsingOperation(bad, "rider-service.getRider")
		assert.Error(t, err)
	})
	t.Run("UpsertFlows", func(t *testing.T) {
		err := c.UpsertFlows(bad, "workspace", "", nil)
		assert.Error(t, err)
	})
	t.Run("Stats", func(t *testing.T) {
		_, err := c.Stats(bad)
		assert.Error(t, err)
	})
	t.Run("SuggestOperationIDs", func(t *testing.T) {
		_, err := c.SuggestOperationIDs(bad, "getRider", 5)
		assert.Error(t, err)
	})
	t.Run("Apply", func(t *testing.T) {
		_, err := c.Apply(bad, riderServiceSnapshot())
		assert.Error(t, err)
	})
	t.Run("MarkServiceError", func(t *testing.T) {
		err := c.MarkServiceError(bad, domain.Service{ID: "rider-service", Name: "rider-service"}, "boom")
		assert.Error(t, err)
	})
	t.Run("RemoveService", func(t *testing.T) {
		err := c.RemoveService(bad, "rider-service")
		assert.Error(t, err)
	})

	// The unmodified context still works after all the canceled-context calls above.
	svc, err := c.GetService(ctx, "rider-service")
	require.NoError(t, err)
	assert.Equal(t, "rider-service", svc.ID)
}

// TestApply_DuplicatePrimaryKeysFail exercises the INSERT error branches
// inside Apply's transaction (operations, schemas, docs, and flows all have
// a real primary key SQLite will reject a duplicate for), by feeding it
// snapshots with an internally-duplicated key.
func TestApply_DuplicatePrimaryKeysFail(t *testing.T) {
	t.Run("duplicate field path", func(t *testing.T) {
		db := openTestDB(t)
		c := catalog.New(db)
		ctx := t.Context()
		op := domain.Operation{
			ID: "dup-service.op", ServiceID: "dup-service", Protocol: domain.ProtocolHTTP,
			HTTP: &domain.HTTPBinding{Method: "GET", Path: "/v1/x"}, RawOpID: "op", Hash: "h1",
		}
		snap := catalog.Snapshot{
			Service:    domain.Service{ID: "dup-service", Name: "dup-service"},
			Operations: []domain.Operation{op},
			Fields: []domain.Field{
				{OperationID: "dup-service.op", Path: "request.query.limit", Type: "integer"},
				{OperationID: "dup-service.op", Path: "request.query.limit", Type: "integer"},
			},
		}
		_, err := c.Apply(ctx, snap)
		assert.Error(t, err)
	})

	t.Run("duplicate schema name", func(t *testing.T) {
		db := openTestDB(t)
		c := catalog.New(db)
		ctx := t.Context()
		snap := catalog.Snapshot{
			Service: domain.Service{ID: "dup-service", Name: "dup-service"},
			Schemas: []domain.NamedSchema{
				{ServiceID: "dup-service", Name: "Thing", Hash: "h1"},
				{ServiceID: "dup-service", Name: "Thing", Hash: "h2"},
			},
		}
		_, err := c.Apply(ctx, snap)
		assert.Error(t, err)
	})

	t.Run("duplicate doc id", func(t *testing.T) {
		db := openTestDB(t)
		c := catalog.New(db)
		ctx := t.Context()
		doc := domain.Doc{ID: "dup-service/docs/a.md", ServiceID: "dup-service", Path: "docs/a.md"}
		snap := catalog.Snapshot{
			Service: domain.Service{ID: "dup-service", Name: "dup-service"},
			Docs:    []domain.Doc{doc, doc},
		}
		_, err := c.Apply(ctx, snap)
		assert.Error(t, err)
	})

	t.Run("duplicate doc section id", func(t *testing.T) {
		db := openTestDB(t)
		c := catalog.New(db)
		ctx := t.Context()
		sec := domain.DocSection{ID: "dup-service/docs/a.md#s", Heading: "S"}
		snap := catalog.Snapshot{
			Service: domain.Service{ID: "dup-service", Name: "dup-service"},
			Docs: []domain.Doc{{
				ID: "dup-service/docs/a.md", ServiceID: "dup-service", Path: "docs/a.md",
				Sections: []domain.DocSection{sec, sec},
			}},
		}
		_, err := c.Apply(ctx, snap)
		assert.Error(t, err)
	})

	t.Run("duplicate flow id", func(t *testing.T) {
		db := openTestDB(t)
		c := catalog.New(db)
		ctx := t.Context()
		flow := domain.FlowSummary{ID: "dup-flow", Path: "flows/a.yaml"}
		snap := catalog.Snapshot{
			Service: domain.Service{ID: "dup-service", Name: "dup-service"},
			Flows:   []domain.FlowSummary{flow, flow},
		}
		_, err := c.Apply(ctx, snap)
		assert.Error(t, err)
	})
}
