package flow

import (
	"context"
	"sort"

	"github.com/growsimplee/sapien/internal/domain"
)

// fakeCatalog is a small, hand-built stand-in for internal/catalog (which
// this package must not import -- it's being built in parallel) modelled
// on fixtures/logistics: order-service.createOrder, allocation-service.
// {allocate,getAllocation,releaseAllocation,allocateV1}, rider-service.
// {getRider,searchRiders}.
type fakeCatalog struct {
	ops    map[string]*domain.Operation
	fields map[string][]domain.Field
}

func newFakeCatalog() *fakeCatalog {
	c := &fakeCatalog{ops: map[string]*domain.Operation{}, fields: map[string][]domain.Field{}}
	c.add(createOrderOp(), createOrderFields())
	c.add(allocateOp(), allocateFields())
	c.add(getAllocationOp(), getAllocationFields())
	c.add(releaseAllocationOp(), releaseAllocationFields())
	c.add(allocateV1Op(), allocateV1Fields())
	c.add(getRiderOp(), getRiderFields())
	c.add(searchRidersOp(), searchRidersFields())
	return c
}

func (c *fakeCatalog) add(op *domain.Operation, fields []domain.Field) {
	c.ops[op.ID] = op
	c.fields[op.ID] = fields
}

func (c *fakeCatalog) Operation(_ context.Context, id string) (*domain.Operation, bool) {
	op, ok := c.ops[id]
	return op, ok
}

func (c *fakeCatalog) Suggest(_ context.Context, ref string, n int) []string {
	ids := make([]string, 0, len(c.ops))
	for id := range c.ops {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return nearestSuggestions(ref, ids, n)
}

func (c *fakeCatalog) Fields(_ context.Context, operationID string) []domain.Field {
	return c.fields[operationID]
}

func strParam(name string, in domain.ParamLocation, required bool) domain.Param {
	return domain.Param{Name: name, In: in, Required: required, Schema: &domain.Schema{Kind: domain.KindString}}
}

func field(opID, path, typ string, required bool) domain.Field {
	return domain.Field{OperationID: opID, Path: path, Type: typ, Required: required}
}

// ---- order-service.createOrder --------------------------------------------

func createOrderOp() *domain.Operation {
	return &domain.Operation{
		ID: "order-service.createOrder", ServiceID: "order-service", Protocol: domain.ProtocolHTTP,
		HTTP:        &domain.HTTPBinding{Method: "POST", Path: "/v1/orders"},
		Summary:     "Create a new order",
		RequestBody: &domain.Body{ContentType: "application/json", Required: true},
		Responses:   []domain.Response{{Status: "201"}, {Status: "400"}},
	}
}

func createOrderFields() []domain.Field {
	id := "order-service.createOrder"
	return []domain.Field{
		field(id, "request.body.customerId", "string", true),
		field(id, "request.body.type", "string", true),
		field(id, "request.body.pickup.lat", "number", true),
		field(id, "request.body.pickup.lng", "number", true),
		field(id, "request.body.drop.lat", "number", true),
		field(id, "request.body.drop.lng", "number", true),
		field(id, "response.201.body.orderId", "string", true),
		field(id, "response.201.body.status", "string", true),
		field(id, "response.201.body.type", "string", true),
		field(id, "response.201.body.customerId", "string", true),
		field(id, "response.201.body.createdAt", "string", true),
	}
}

// ---- allocation-service.allocate -------------------------------------------

func allocateOp() *domain.Operation {
	return &domain.Operation{
		ID: "allocation-service.allocate", ServiceID: "allocation-service", Protocol: domain.ProtocolHTTP,
		HTTP:        &domain.HTTPBinding{Method: "POST", Path: "/v1/allocations"},
		RequestBody: &domain.Body{ContentType: "application/json", Required: true},
		Responses:   []domain.Response{{Status: "201"}, {Status: "404"}, {Status: "409"}},
	}
}

func allocateFields() []domain.Field {
	id := "allocation-service.allocate"
	return []domain.Field{
		field(id, "request.body.orderId", "string", true),
		field(id, "response.201.body.allocationId", "string", true),
		field(id, "response.201.body.orderId", "string", true),
		field(id, "response.201.body.riderId", "string", true),
		field(id, "response.201.body.status", "string", true),
		field(id, "response.201.body.allocatedAt", "string", true),
	}
}

// ---- allocation-service.getAllocation --------------------------------------

func getAllocationOp() *domain.Operation {
	return &domain.Operation{
		ID: "allocation-service.getAllocation", ServiceID: "allocation-service", Protocol: domain.ProtocolHTTP,
		HTTP:      &domain.HTTPBinding{Method: "GET", Path: "/v1/allocations/{allocationId}"},
		Params:    []domain.Param{strParam("allocationId", domain.InPath, true)},
		Responses: []domain.Response{{Status: "200"}, {Status: "404"}},
	}
}

func getAllocationFields() []domain.Field {
	id := "allocation-service.getAllocation"
	return []domain.Field{
		field(id, "response.200.body.allocationId", "string", true),
		field(id, "response.200.body.orderId", "string", true),
		field(id, "response.200.body.riderId", "string", true),
		field(id, "response.200.body.status", "string", true),
		field(id, "response.200.body.allocatedAt", "string", true),
	}
}

// ---- allocation-service.releaseAllocation -----------------------------------

func releaseAllocationOp() *domain.Operation {
	return &domain.Operation{
		ID: "allocation-service.releaseAllocation", ServiceID: "allocation-service", Protocol: domain.ProtocolHTTP,
		HTTP:      &domain.HTTPBinding{Method: "POST", Path: "/v1/allocations/{allocationId}/release"},
		Params:    []domain.Param{strParam("allocationId", domain.InPath, true)},
		Responses: []domain.Response{{Status: "200"}, {Status: "404"}, {Status: "409"}},
	}
}

func releaseAllocationFields() []domain.Field {
	id := "allocation-service.releaseAllocation"
	return []domain.Field{
		field(id, "response.200.body.allocationId", "string", true),
		field(id, "response.200.body.orderId", "string", true),
		field(id, "response.200.body.riderId", "string", true),
		field(id, "response.200.body.status", "string", true),
		field(id, "response.200.body.allocatedAt", "string", true),
	}
}

// ---- allocation-service.allocateV1 (deprecated) ----------------------------

func allocateV1Op() *domain.Operation {
	return &domain.Operation{
		ID: "allocation-service.allocateV1", ServiceID: "allocation-service", Protocol: domain.ProtocolHTTP,
		HTTP:        &domain.HTTPBinding{Method: "POST", Path: "/v1/allocate"},
		Deprecated:  true,
		RequestBody: &domain.Body{ContentType: "application/json", Required: true},
		Responses:   []domain.Response{{Status: "201"}, {Status: "409"}},
	}
}

func allocateV1Fields() []domain.Field {
	id := "allocation-service.allocateV1"
	return []domain.Field{
		field(id, "request.body.orderId", "string", true),
		field(id, "response.201.body.allocationId", "string", true),
		field(id, "response.201.body.orderId", "string", true),
		field(id, "response.201.body.riderId", "string", true),
		field(id, "response.201.body.status", "string", true),
		field(id, "response.201.body.allocatedAt", "string", true),
	}
}

// ---- rider-service.getRider -------------------------------------------------

func getRiderOp() *domain.Operation {
	return &domain.Operation{
		ID: "rider-service.getRider", ServiceID: "rider-service", Protocol: domain.ProtocolHTTP,
		HTTP:      &domain.HTTPBinding{Method: "GET", Path: "/v1/riders/{riderId}"},
		Params:    []domain.Param{strParam("riderId", domain.InPath, true)},
		Responses: []domain.Response{{Status: "200"}, {Status: "404"}},
	}
}

func getRiderFields() []domain.Field {
	id := "rider-service.getRider"
	return []domain.Field{
		field(id, "response.200.body.riderId", "string", true),
		field(id, "response.200.body.name", "string", true),
		field(id, "response.200.body.online", "boolean", true),
		field(id, "response.200.body.qcomSkill", "boolean", true),
		field(id, "response.200.body.upcomingTrips", "integer", true),
		field(id, "response.200.body.city", "string", false),
		field(id, "response.200.body.rating", "number|null", false),
		field(id, "response.200.body.vehicle", "object", false),
	}
}

// ---- rider-service.searchRiders ---------------------------------------------

func searchRidersOp() *domain.Operation {
	return &domain.Operation{
		ID: "rider-service.searchRiders", ServiceID: "rider-service", Protocol: domain.ProtocolHTTP,
		HTTP:        &domain.HTTPBinding{Method: "POST", Path: "/v1/riders/search"},
		RequestBody: &domain.Body{ContentType: "application/json", Required: true},
		Responses:   []domain.Response{{Status: "200"}},
	}
}

// ---- fake ExampleResolver (mirrors fakeCatalog, PLAN §34b) ----------------

// fakeResolver is a small in-memory ExampleResolver for tests: SuggestExamples
// ranks every known id with the same nearestSuggestions helper Validate uses
// for UNKNOWN_OPERATION, so "did you mean" behavior is exercised end to end.
type fakeResolver struct {
	examples map[string]*domain.SavedExample
}

// newFakeResolver indexes exs by ID.
func newFakeResolver(exs ...domain.SavedExample) *fakeResolver {
	r := &fakeResolver{examples: map[string]*domain.SavedExample{}}
	for i := range exs {
		ex := exs[i]
		r.examples[ex.ID] = &ex
	}
	return r
}

func (r *fakeResolver) Example(_ context.Context, id string) (*domain.SavedExample, bool) {
	ex, ok := r.examples[id]
	return ex, ok
}

func (r *fakeResolver) SuggestExamples(_ context.Context, ref string, n int) []string {
	ids := make([]string, 0, len(r.examples))
	for id := range r.examples {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return nearestSuggestions(ref, ids, n)
}

func searchRidersFields() []domain.Field {
	id := "rider-service.searchRiders"
	return []domain.Field{
		field(id, "request.body.lat", "number", true),
		field(id, "request.body.lng", "number", true),
		field(id, "request.body.radiusKm", "number", true),
		field(id, "request.body.qcomOnly", "boolean", false),
		field(id, "response.200.body.riders[].riderId", "string", true),
		field(id, "response.200.body.riders[].name", "string", true),
		field(id, "response.200.body.riders[].online", "boolean", true),
		field(id, "response.200.body.riders[].qcomSkill", "boolean", true),
	}
}
