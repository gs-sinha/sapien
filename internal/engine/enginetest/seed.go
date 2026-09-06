package enginetest

import (
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gs-sinha/sapien/internal/domain"
)

// Seed populates f with a small, consistent sample workspace: 2 services, 4
// operations (with fields), 1 doc, 2 flows, 1 run with steps, 3 memories,
// and 2 environments (one of them production). It is meant to be called
// once, right after New, before concurrent use begins.
func Seed(f *Fake) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now().UTC()

	// --- services ---
	orderSvc := domain.Service{
		ID:             "order-service",
		Name:           "order-service",
		Description:    "Order management",
		Owners:         []string{"logistics-team"},
		Concepts:       []string{"order"},
		Source:         domain.Source{Kind: domain.SourceLocal, Path: "services/order-service"},
		PackageDir:     "/workspace/order-service/api",
		ContractFiles:  []string{"openapi.yaml"},
		Environments:   map[string]domain.EnvHint{"local": {BaseURL: "http://localhost:4010"}},
		Status:         domain.SyncOK,
		LastIndexed:    now,
		OperationCount: 2,
	}
	riderSvc := domain.Service{
		ID:             "rider-service",
		Name:           "rider-service",
		Description:    "Rider assignment",
		Owners:         []string{"logistics-team"},
		Concepts:       []string{"rider"},
		Source:         domain.Source{Kind: domain.SourceLocal, Path: "services/rider-service"},
		PackageDir:     "/workspace/rider-service/api",
		ContractFiles:  []string{"openapi.yaml"},
		Environments:   map[string]domain.EnvHint{"local": {BaseURL: "http://localhost:4011"}},
		Status:         domain.SyncOK,
		LastIndexed:    now,
		OperationCount: 2,
	}
	f.services[orderSvc.ID] = orderSvc
	f.services[riderSvc.ID] = riderSvc

	// --- operations + fields ---
	createOrder := domain.Operation{
		ID:        "order-service.createOrder",
		ServiceID: "order-service",
		Protocol:  domain.ProtocolHTTP,
		HTTP:      &domain.HTTPBinding{Method: "POST", Path: "/v1/orders"},
		RawOpID:   "createOrder",
		Summary:   "Create an order",
		RequestBody: &domain.Body{
			ContentType: "application/json",
			Required:    true,
			Schema: &domain.Schema{
				Kind:       domain.KindObject,
				Properties: map[string]*domain.Schema{"customerId": {Kind: domain.KindString}},
				Required:   []string{"customerId"},
			},
		},
		Responses: []domain.Response{{
			Status:      "201",
			ContentType: "application/json",
			Schema: &domain.Schema{
				Kind:       domain.KindObject,
				Properties: map[string]*domain.Schema{"orderId": {Kind: domain.KindString}},
			},
		}},
		Source: domain.SourceLoc{File: "order-service/api/openapi.yaml", Pointer: "/paths/~1v1~1orders/post"},
		Hash:   hashOf("order-service.createOrder"),
	}
	getOrder := domain.Operation{
		ID:        "order-service.getOrder",
		ServiceID: "order-service",
		Protocol:  domain.ProtocolHTTP,
		HTTP:      &domain.HTTPBinding{Method: "GET", Path: "/v1/orders/{orderId}"},
		RawOpID:   "getOrder",
		Summary:   "Get an order",
		Params: []domain.Param{{
			Name: "orderId", In: domain.InPath, Required: true, Schema: &domain.Schema{Kind: domain.KindString},
		}},
		Responses: []domain.Response{{
			Status:      "200",
			ContentType: "application/json",
			Schema: &domain.Schema{
				Kind:       domain.KindObject,
				Properties: map[string]*domain.Schema{"orderId": {Kind: domain.KindString}},
			},
		}},
		Source: domain.SourceLoc{File: "order-service/api/openapi.yaml", Pointer: "/paths/~1v1~1orders~1{orderId}/get"},
		Hash:   hashOf("order-service.getOrder"),
	}
	getRider := domain.Operation{
		ID:        "rider-service.getRider",
		ServiceID: "rider-service",
		Protocol:  domain.ProtocolHTTP,
		HTTP:      &domain.HTTPBinding{Method: "GET", Path: "/v1/riders/{riderId}"},
		RawOpID:   "getRider",
		Summary:   "Get a rider",
		Params: []domain.Param{{
			Name: "riderId", In: domain.InPath, Required: true, Schema: &domain.Schema{Kind: domain.KindString},
		}},
		Responses: []domain.Response{{
			Status:      "200",
			ContentType: "application/json",
			Schema: &domain.Schema{
				Kind:       domain.KindObject,
				Properties: map[string]*domain.Schema{"riderId": {Kind: domain.KindString}},
			},
		}},
		Source: domain.SourceLoc{File: "rider-service/api/openapi.yaml", Pointer: "/paths/~1v1~1riders~1{riderId}/get"},
		Hash:   hashOf("rider-service.getRider"),
	}
	assignRider := domain.Operation{
		ID:        "rider-service.assignRider",
		ServiceID: "rider-service",
		Protocol:  domain.ProtocolHTTP,
		HTTP:      &domain.HTTPBinding{Method: "POST", Path: "/v1/riders/{riderId}/assign"},
		RawOpID:   "assignRider",
		Summary:   "Assign a rider to an order",
		Params: []domain.Param{{
			Name: "riderId", In: domain.InPath, Required: true, Schema: &domain.Schema{Kind: domain.KindString},
		}},
		RequestBody: &domain.Body{
			ContentType: "application/json",
			Required:    true,
			Schema: &domain.Schema{
				Kind:       domain.KindObject,
				Properties: map[string]*domain.Schema{"orderId": {Kind: domain.KindString}},
				Required:   []string{"orderId"},
			},
		},
		Responses: []domain.Response{{Status: "204"}},
		Source:    domain.SourceLoc{File: "rider-service/api/openapi.yaml", Pointer: "/paths/~1v1~1riders~1{riderId}~1assign/post"},
		Hash:      hashOf("rider-service.assignRider"),
	}

	for _, op := range []domain.Operation{createOrder, getOrder, getRider, assignRider} {
		f.operations[op.ID] = op
	}

	f.fields[createOrder.ID] = []domain.Field{
		{OperationID: createOrder.ID, Path: "request.body.customerId", Type: "string", Required: true},
		{OperationID: createOrder.ID, Path: "response.201.body.orderId", Type: "string"},
	}
	f.fields[getOrder.ID] = []domain.Field{
		{OperationID: getOrder.ID, Path: "request.path.orderId", Type: "string", Required: true},
		{OperationID: getOrder.ID, Path: "response.200.body.orderId", Type: "string"},
	}
	f.fields[getRider.ID] = []domain.Field{
		{OperationID: getRider.ID, Path: "request.path.riderId", Type: "string", Required: true},
		{OperationID: getRider.ID, Path: "response.200.body.riderId", Type: "string"},
	}
	f.fields[assignRider.ID] = []domain.Field{
		{OperationID: assignRider.ID, Path: "request.path.riderId", Type: "string", Required: true},
		{OperationID: assignRider.ID, Path: "request.body.orderId", Type: "string", Required: true},
	}

	// --- schemas ---
	f.schemas["order-service.Order"] = domain.NamedSchema{
		ServiceID: "order-service",
		Name:      "Order",
		Hash:      hashOf("order-service.Order"),
		Schema: &domain.Schema{
			Kind:       domain.KindObject,
			Name:       "Order",
			Properties: map[string]*domain.Schema{"orderId": {Kind: domain.KindString}, "customerId": {Kind: domain.KindString}},
			Required:   []string{"orderId", "customerId"},
		},
		UsedBy: []string{createOrder.ID, getOrder.ID},
	}

	// --- doc ---
	doc := domain.Doc{
		ID:        "order-service/docs/orders.md",
		ServiceID: "order-service",
		Path:      "docs/orders.md",
		Title:     "Orders",
		Source:    domain.DocSourceFile,
		Hash:      hashOf("orders.md"),
		Sections: []domain.DocSection{{
			ID:      "order-service/docs/orders.md#overview",
			Ord:     0,
			Heading: "Overview",
			Level:   1,
			Body:    "Orders represent a customer purchase awaiting rider assignment.",
			Refs:    []domain.DocRef{{Kind: domain.RefOperation, Value: createOrder.ID}},
		}},
	}
	f.docs[doc.ID] = doc

	// --- flows ---
	createOrderFlowYAML := "version: 1\n" +
		"id: create-order-flow\n" +
		"name: Create order\n" +
		"description: Create an order and fetch it back.\n" +
		"tags: [order]\n" +
		"steps:\n" +
		"  - id: create\n" +
		"    call: order-service.createOrder\n" +
		"    body:\n" +
		"      customerId: cust_1\n" +
		"  - id: fetch\n" +
		"    call: order-service.getOrder\n" +
		"    params:\n" +
		"      path:\n" +
		"        orderId: ${create.out.orderId}\n"
	createOrderFlow := domain.Flow{
		Version:     1,
		ID:          "create-order-flow",
		Name:        "Create order",
		Description: "Create an order and fetch it back.",
		Tags:        []string{"order"},
		Steps: []domain.Step{
			{ID: "create", Call: createOrder.ID, Body: map[string]any{"customerId": "cust_1"}},
			{ID: "fetch", Call: getOrder.ID, Params: &domain.ExplicitParams{Path: map[string]any{"orderId": "${create.out.orderId}"}}},
		},
		Path:      "flows/create-order-flow.flow.yaml",
		OwnerKind: "workspace",
		Source:    createOrderFlowYAML,
	}

	assignRiderFlowYAML := "version: 1\n" +
		"id: assign-rider-flow\n" +
		"name: Assign rider\n" +
		"tags: [rider]\n" +
		"steps:\n" +
		"  - id: assign\n" +
		"    call: rider-service.assignRider\n" +
		"    params:\n" +
		"      path:\n" +
		"        riderId: rider_1\n" +
		"    body:\n" +
		"      orderId: order_1\n"
	assignRiderFlow := domain.Flow{
		Version: 1,
		ID:      "assign-rider-flow",
		Name:    "Assign rider",
		Tags:    []string{"rider"},
		Steps: []domain.Step{
			{
				ID:     "assign",
				Call:   assignRider.ID,
				Params: &domain.ExplicitParams{Path: map[string]any{"riderId": "rider_1"}},
				Body:   map[string]any{"orderId": "order_1"},
			},
		},
		Path:      "flows/assign-rider-flow.flow.yaml",
		OwnerKind: "workspace",
		Source:    assignRiderFlowYAML,
	}

	f.flows[createOrderFlow.ID] = createOrderFlow
	f.flows[assignRiderFlow.ID] = assignRiderFlow
	f.flowUpdated[createOrderFlow.ID] = now
	f.flowUpdated[assignRiderFlow.ID] = now

	// --- run (with steps), for create-order-flow ---
	runStart := now.Add(-time.Minute)
	runStep1Finish := runStart.Add(8 * time.Millisecond)
	runStep2Finish := runStep1Finish.Add(6 * time.Millisecond)
	run := domain.Run{
		ID:          "run_" + ulid.Make().String(),
		FlowID:      createOrderFlow.ID,
		Environment: "local",
		Inputs:      map[string]any{"customerId": "cust_1"},
		Status:      domain.RunPassed,
		Started:     runStart,
		Finished:    runStep2Finish,
		DurationMs:  runStep2Finish.Sub(runStart).Milliseconds(),
		Trigger:     "cli",
		Steps: []domain.StepResult{
			{
				StepID:    "create",
				Index:     0,
				Operation: createOrder.ID,
				Status:    domain.StepPassed,
				Attempts:  1,
				Request:   &domain.RequestRecord{Method: "POST", URL: "http://localhost:4010/v1/orders", Body: map[string]any{"customerId": "cust_1"}},
				Response:  &domain.ResponseRecord{Status: 201, Body: map[string]any{"orderId": "order_1"}, Size: 20},
				Timings:   &domain.Timings{TotalMs: 8},
				Out:       map[string]any{"orderId": "order_1"},
				Started:   runStart,
				Finished:  runStep1Finish,
			},
			{
				StepID:    "fetch",
				Index:     1,
				Operation: getOrder.ID,
				Status:    domain.StepPassed,
				Attempts:  1,
				Request:   &domain.RequestRecord{Method: "GET", URL: "http://localhost:4010/v1/orders/order_1"},
				Response:  &domain.ResponseRecord{Status: 200, Body: map[string]any{"orderId": "order_1"}, Size: 20},
				Timings:   &domain.Timings{TotalMs: 6},
				Assertions: []domain.AssertionResult{
					{Expr: "response.body.orderId == 'order_1'", Passed: true},
				},
				Started:  runStep1Finish,
				Finished: runStep2Finish,
			},
		},
		Summary: domain.RunSummary{StepsTotal: 2, StepsPassed: 2, Assertions: 1},
	}
	f.runs[run.ID] = run
	f.runSeq[run.ID] = f.nextSeqLocked()

	// --- memories ---
	mem1 := domain.Memory{
		ID:      "mem_" + ulid.Make().String(),
		Type:    domain.MemoryGotcha,
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "order-service", Operation: createOrder.ID},
		Tags:    []string{"orders"},
		Source:  domain.MemorySource{Kind: "user"},
		Status:  domain.MemoryActive,
		Created: now,
		Updated: now,
		Text:    "createOrder requires customerId even though older specs mark it optional.",
	}
	mem2 := domain.Memory{
		ID:      "mem_" + ulid.Make().String(),
		Type:    domain.MemoryInvariant,
		Scope:   domain.ScopeService,
		Subject: domain.Subject{Service: "rider-service", Operation: getRider.ID},
		Source:  domain.MemorySource{Kind: "agent", Client: "claude-code"},
		Status:  domain.MemoryActive,
		Created: now,
		Updated: now,
		Text:    "riderId in the response always matches the path riderId.",
	}
	mem3 := domain.Memory{
		ID:      "mem_" + ulid.Make().String(),
		Type:    domain.MemoryTesting,
		Scope:   domain.ScopePersonal,
		Subject: domain.Subject{Flow: createOrderFlow.ID},
		Source:  domain.MemorySource{Kind: "run", RunID: run.ID},
		Status:  domain.MemoryActive,
		Created: now,
		Updated: now,
		Text:    "Use a fresh customerId per run; the sandbox rejects duplicates within 5 minutes.",
	}
	for _, mem := range []domain.Memory{mem1, mem2, mem3} {
		mem.Hash = hashOf(mem.Text)
		f.memories[mem.ID] = mem
	}

	// --- environments ---
	local := domain.Environment{
		Version: 1,
		Name:    "local",
		Services: map[string]domain.ServiceEnv{
			"order-service": {BaseURL: "http://localhost:4010"},
			"rider-service": {BaseURL: "http://localhost:4011"},
		},
		Vars: map[string]string{"REGION": "local"},
	}
	production := domain.Environment{
		Version:    1,
		Name:       "production",
		Production: true,
		Services: map[string]domain.ServiceEnv{
			"order-service": {BaseURL: "https://orders.prod.example.com"},
			"rider-service": {BaseURL: "https://riders.prod.example.com"},
		},
	}
	f.environments[local.Name] = local
	f.environments[production.Name] = production
	f.defaultEnv = local.Name
}
