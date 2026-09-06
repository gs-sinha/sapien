package catalog_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/catalog"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var fixtureTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// riderServiceSnapshot returns the "rider-service" fixture: 3 operations, 1
// schema, 1 doc (2 sections, 1 with a ref), 1 flow.
func riderServiceSnapshot() catalog.Snapshot {
	svc := domain.Service{
		ID:          "rider-service",
		Name:        "rider-service",
		Description: "Manages riders.",
		Owners:      []string{"team-riders"},
		Source:      domain.Source{Kind: domain.SourceLocal, Path: "services/rider-service"},
	}

	ops := []domain.Operation{
		{
			ID: "rider-service.getRider", ServiceID: svc.ID, Protocol: domain.ProtocolHTTP,
			HTTP:    &domain.HTTPBinding{Method: "GET", Path: "/v1/riders/{riderId}"},
			RawOpID: "getRider", Summary: "Get a rider", Description: "Fetch a single rider by ID.",
			Tags: []string{"riders"}, Concepts: []string{"allocation"},
			Params: []domain.Param{{Name: "riderId", In: domain.InPath, Required: true}},
			Hash:   "h-get-rider-1",
		},
		{
			ID: "rider-service.listRiders", ServiceID: svc.ID, Protocol: domain.ProtocolHTTP,
			HTTP:    &domain.HTTPBinding{Method: "GET", Path: "/v1/riders"},
			RawOpID: "listRiders", Summary: "List riders",
			Params: []domain.Param{{Name: "limit", In: domain.InQuery}},
			Hash:   "h-list-riders-1",
		},
		{
			ID: "rider-service.getStatus", ServiceID: svc.ID, Protocol: domain.ProtocolHTTP,
			HTTP:    &domain.HTTPBinding{Method: "GET", Path: "/v1/riders/status"},
			RawOpID: "getStatus", Summary: "Rider service status",
			Hash: "h-get-status-rider-1",
		},
	}

	aliases := []domain.Alias{
		{Method: "GET", Path: "/v1/riders/{riderId}", OperationID: "rider-service.getRider"},
		{Method: "GET", Path: "/v1/riders", OperationID: "rider-service.listRiders"},
		{Method: "GET", Path: "/v1/riders/status", OperationID: "rider-service.getStatus"},
	}

	fields := []domain.Field{
		{OperationID: "rider-service.getRider", Path: "request.path.riderId", Type: "string", Required: true},
		{OperationID: "rider-service.getRider", Path: "response.200.body.riderId", Type: "string"},
		{OperationID: "rider-service.getRider", Path: "response.200.body.name", Type: "string"},
		{OperationID: "rider-service.listRiders", Path: "response.200.body.items[].riderId", Type: "string"},
		{OperationID: "rider-service.listRiders", Path: "response.200.body.items[].name", Type: "string"},
	}

	schemas := []domain.NamedSchema{
		{ServiceID: svc.ID, Name: "Rider", Hash: "h-schema-rider", Schema: &domain.Schema{Kind: domain.KindObject}},
	}

	docs := []domain.Doc{
		{
			ID: "rider-service/docs/allocation.md", ServiceID: svc.ID, Path: "docs/allocation.md",
			Title: "Rider Allocation", Source: domain.DocSourceFile, Hash: "h-doc-allocation",
			Sections: []domain.DocSection{
				{
					ID: "rider-service/docs/allocation.md#overview", Ord: 0, Heading: "Overview", Level: 1,
					Body: "Riders are matched via the getRider operation.",
					Refs: []domain.DocRef{{Kind: domain.RefOperation, Value: "rider-service.getRider"}},
				},
				{
					ID: "rider-service/docs/allocation.md#status-field", Ord: 1, Heading: "Status field", Level: 2,
					Body: "The status field of a rider indicates availability.",
				},
			},
		},
	}

	flows := []domain.FlowSummary{
		{
			ID: "rider-service/flows/onboard.yaml", Name: "Onboard Rider", Path: "flows/onboard.yaml",
			OwnerKind: "service", OwnerID: svc.ID, Tags: []string{"onboarding"},
			Operations: []string{"rider-service.createRider", "rider-service.getRider"},
			StepCount:  2, Hash: "h-flow-onboard", Updated: fixtureTime,
		},
	}

	return catalog.Snapshot{
		Service:       svc,
		Operations:    ops,
		Schemas:       schemas,
		Fields:        fields,
		Aliases:       aliases,
		Docs:          docs,
		Flows:         flows,
		ContractFiles: map[string]string{"api/openapi.yaml": "h-contract-rider"},
	}
}

// orderServiceSnapshot returns the "order-service" fixture: 3 operations, 2
// schemas, 1 doc (1 section with a ref), 1 flow.
func orderServiceSnapshot() catalog.Snapshot {
	svc := domain.Service{
		ID:          "order-service",
		Name:        "order-service",
		Description: "Manages orders.",
		Source:      domain.Source{Kind: domain.SourceLocal, Path: "services/order-service"},
	}

	ops := []domain.Operation{
		{
			ID: "order-service.getOrder", ServiceID: svc.ID, Protocol: domain.ProtocolHTTP,
			HTTP:    &domain.HTTPBinding{Method: "GET", Path: "/v1/orders/{orderId}"},
			RawOpID: "getOrder", Summary: "Get an order",
			Params: []domain.Param{{Name: "orderId", In: domain.InPath, Required: true}},
			Hash:   "h-get-order-1",
		},
		{
			ID: "order-service.createOrder", ServiceID: svc.ID, Protocol: domain.ProtocolHTTP,
			HTTP:    &domain.HTTPBinding{Method: "POST", Path: "/v1/orders"},
			RawOpID: "createOrder", Summary: "Create an order",
			Hash: "h-create-order-1",
		},
		{
			ID: "order-service.getStatus", ServiceID: svc.ID, Protocol: domain.ProtocolHTTP,
			HTTP:    &domain.HTTPBinding{Method: "GET", Path: "/v1/orders/status"},
			RawOpID: "getStatus", Summary: "Order service status",
			Hash: "h-get-status-order-1",
		},
	}

	aliases := []domain.Alias{
		{Method: "GET", Path: "/v1/orders/{orderId}", OperationID: "order-service.getOrder"},
		{Method: "POST", Path: "/v1/orders", OperationID: "order-service.createOrder"},
		{Method: "GET", Path: "/v1/orders/status", OperationID: "order-service.getStatus"},
	}

	fields := []domain.Field{
		{OperationID: "order-service.createOrder", Path: "request.body.riderId", Type: "string"},
		{OperationID: "order-service.createOrder", Path: "request.body.customerId", Type: "string"},
		{OperationID: "order-service.createOrder", Path: "response.200.body.orderId", Type: "string"},
		{OperationID: "order-service.getOrder", Path: "response.200.body.orderId", Type: "string"},
		{OperationID: "order-service.getOrder", Path: "response.200.body.riderId", Type: "string"},
	}

	schemas := []domain.NamedSchema{
		{ServiceID: svc.ID, Name: "Order", Hash: "h-schema-order", Schema: &domain.Schema{Kind: domain.KindObject}},
		{ServiceID: svc.ID, Name: "Customer", Hash: "h-schema-customer", Schema: &domain.Schema{Kind: domain.KindObject}},
	}

	docs := []domain.Doc{
		{
			ID: "order-service/docs/lifecycle.md", ServiceID: svc.ID, Path: "docs/lifecycle.md",
			Title: "Order Lifecycle", Source: domain.DocSourceFile, Hash: "h-doc-lifecycle",
			Sections: []domain.DocSection{
				{
					ID: "order-service/docs/lifecycle.md#cancellation", Ord: 0, Heading: "Cancellation", Level: 1,
					Body: "See order-service.createOrder for creation.",
					Refs: []domain.DocRef{{Kind: domain.RefOperation, Value: "order-service.createOrder"}},
				},
			},
		},
	}

	flows := []domain.FlowSummary{
		{
			ID: "order-service/flows/checkout.yaml", Name: "Checkout", Path: "flows/checkout.yaml",
			OwnerKind: "service", OwnerID: svc.ID,
			Operations: []string{"order-service.createOrder", "order-service.getOrder"},
			StepCount:  2, Hash: "h-flow-checkout", Updated: fixtureTime,
		},
	}

	return catalog.Snapshot{
		Service:       svc,
		Operations:    ops,
		Schemas:       schemas,
		Fields:        fields,
		Aliases:       aliases,
		Docs:          docs,
		Flows:         flows,
		ContractFiles: map[string]string{"api/openapi.yaml": "h-contract-order"},
	}
}

// applyBaseFixtures applies both service snapshots and returns their changes
// (rider-service, order-service).
func applyBaseFixtures(t *testing.T, c *catalog.Catalog) (riderChange, orderChange domain.CatalogChange) {
	t.Helper()
	ctx := t.Context()

	riderChange, err := c.Apply(ctx, riderServiceSnapshot())
	require.NoError(t, err)
	orderChange, err = c.Apply(ctx, orderServiceSnapshot())
	require.NoError(t, err)
	return riderChange, orderChange
}
