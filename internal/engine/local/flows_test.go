package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/engine/enginetest"
	"github.com/growsimplee/sapien/internal/errs"
)

const validFlowYAML = `version: 1
id: order-allocation
name: Order allocation
description: Create an order, allocate, verify rider online.
tags: [allocation, smoke]

steps:
  - id: create
    call: order-service.createOrder
    body:
      customerId: cust_123
      type: QCOM
      pickup: { lat: 12.9716, lng: 77.5946 }
      drop: { lat: 12.9352, lng: 77.6146 }
    extract:
      orderId: body.orderId
    assert:
      - status == 201
`

const invalidFlowYAML = `version: 1
id: broken-flow
steps:
  - id: create
    call: order-service.doesNotExist
    assert:
      - status == 201
`

func TestFlows_CreateValid(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	f, err := l.Flows().Create(ctx, validFlowYAML, "")
	require.NoError(t, err)
	require.NotNil(t, f)
	assert.Equal(t, "order-allocation", f.ID)
	assert.Equal(t, "workspace", f.OwnerKind)
	assert.FileExists(t, filepath.Join(ws.Dir, domain.FlowsDir, "order-allocation.flow.yaml"))

	got, err := l.Flows().Get(ctx, "order-allocation")
	require.NoError(t, err)
	assert.Equal(t, "Order allocation", got.Name)

	list, err := l.Flows().List(ctx, "")
	require.NoError(t, err)
	found := false
	for _, fs := range list {
		if fs.ID == "order-allocation" {
			found = true
			assert.Contains(t, fs.Operations, "order-service.createOrder")
		}
	}
	assert.True(t, found, "expected order-allocation in Flows().List")

	byTag, err := l.Flows().List(ctx, "smoke")
	require.NoError(t, err)
	assert.NotEmpty(t, byTag)

	// Creating again at the same default path must refuse to overwrite.
	_, err = l.Flows().Create(ctx, validFlowYAML, "")
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}

func TestFlows_CreateInvalid(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().Create(ctx, invalidFlowYAML, "")
	require.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))

	e := errs.As(err)
	diags, ok := e.Details["diagnostics"].([]domain.Diagnostic)
	require.True(t, ok, "expected Details[\"diagnostics\"] to be []domain.Diagnostic, got %T", e.Details["diagnostics"])
	assert.NotEmpty(t, diags)
}

func TestFlows_Get_ServiceOwned(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	f, err := l.Flows().Get(ctx, "smoke")
	require.NoError(t, err)
	assert.Equal(t, "smoke", f.ID)
	assert.Equal(t, "service", f.OwnerKind)
	assert.Equal(t, "allocation-service", f.OwnerID)

	_, err = l.Flows().Get(ctx, "does-not-exist")
	require.Error(t, err)
	assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
}

func TestFlows_Parse(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	f, err := l.Flows().Parse(context.Background(), validFlowYAML)
	require.NoError(t, err)
	assert.Equal(t, "order-allocation", f.ID)

	_, err = l.Flows().Parse(context.Background(), "not: valid: yaml: [")
	require.Error(t, err)
}

func TestFlows_UpdateAndDelete(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().Create(ctx, validFlowYAML, "")
	require.NoError(t, err)

	updatedYAML := `version: 1
id: order-allocation
name: Order allocation (updated)
tags: [allocation, smoke, updated]

steps:
  - id: create
    call: order-service.createOrder
    body:
      customerId: cust_123
      type: QCOM
      pickup: { lat: 12.9716, lng: 77.5946 }
      drop: { lat: 12.9352, lng: 77.6146 }
    extract:
      orderId: body.orderId
    assert:
      - status == 201
`
	updated, err := l.Flows().Update(ctx, "order-allocation", updatedYAML)
	require.NoError(t, err)
	assert.Equal(t, "Order allocation (updated)", updated.Name)

	got, err := l.Flows().Get(ctx, "order-allocation")
	require.NoError(t, err)
	assert.Equal(t, "Order allocation (updated)", got.Name)
	assert.Contains(t, got.Tags, "updated")

	// Renaming (id mismatch) is rejected.
	mismatched := `version: 1
id: something-else
steps:
  - id: create
    call: order-service.createOrder
    body: { customerId: c, type: QCOM, pickup: {lat: 1, lng: 1}, drop: {lat: 1, lng: 1} }
`
	_, err = l.Flows().Update(ctx, "order-allocation", mismatched)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	// Deleting a service-owned flow is refused.
	err = l.Flows().Delete(ctx, "smoke")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	// Deleting the workspace flow removes it.
	require.NoError(t, l.Flows().Delete(ctx, "order-allocation"))
	_, err = l.Flows().Get(ctx, "order-allocation")
	require.Error(t, err)
	assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
	_, statErr := os.Stat(filepath.Join(ws.Dir, domain.FlowsDir, "order-allocation.flow.yaml"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestFlows_Validate(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	result, err := l.Flows().Validate(ctx, validFlowYAML)
	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Diagnostics)

	result, err = l.Flows().Validate(ctx, invalidFlowYAML)
	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.NotEmpty(t, result.Diagnostics)
}

func TestFlows_Update_ServiceOwned(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	existing, err := l.Flows().Get(ctx, "smoke")
	require.NoError(t, err)

	// Drop the `id:` line: Update must fall back to the existing file's
	// name-implied id (flowIDOf -> flowIDFromPath) to confirm the match.
	updatedSrc := `version: 1
name: Allocation smoke test (v2)
tags: [smoke, allocation]
inputs:
  orderId: { type: string, required: true }
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${inputs.orderId}" }
    assert: [status == 201]
`
	updated, err := l.Flows().Update(ctx, "smoke", updatedSrc)
	require.NoError(t, err)
	assert.Equal(t, "smoke", updated.ID)
	assert.Equal(t, "Allocation smoke test (v2)", updated.Name)
	assert.Equal(t, existing.Path, updated.Path)

	got, err := l.Flows().Get(ctx, "smoke")
	require.NoError(t, err)
	assert.Equal(t, "Allocation smoke test (v2)", got.Name)

	list, err := l.Flows().List(ctx, "")
	require.NoError(t, err)
	for _, fs := range list {
		if fs.ID == "smoke" {
			assert.Equal(t, "service", fs.OwnerKind)
			assert.Equal(t, "allocation-service", fs.OwnerID)
		}
	}
}

func TestFlows_Reference(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	flowRef, err := l.Flows().Reference(ctx, "flow")
	require.NoError(t, err)
	assert.Contains(t, flowRef, "Flow DSL reference")

	exprRef, err := l.Flows().Reference(ctx, "expressions")
	require.NoError(t, err)
	assert.Contains(t, exprRef, "Expression reference")

	memRef, err := l.Flows().Reference(ctx, "memory")
	require.NoError(t, err)
	assert.Contains(t, memRef, "Memory reference")
	assert.Contains(t, memRef, "subject")

	_, err = l.Flows().Reference(ctx, "bogus")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// ---- flowExampleResolver (PLAN §34b) --------------------------------------
//
// These test the adapter directly against a fake engine.ExampleAPI
// (enginetest.Fake, seeded via Examples().Create), independent of whatever
// internal/example.Store does, so the adapter's own translation (Get ->
// ok/false, List -> ranked suggestions) is covered without a real
// workspace. TestFlows_Create_And_Run_ExampleStep below additionally
// exercises the real thing end to end, now that examples.go is wired to
// internal/example.

func newSeededFakeExamples(t *testing.T, exs ...domain.SavedExample) *flowExampleResolver {
	t.Helper()
	fake := enginetest.New(&domain.Workspace{})
	ctx := context.Background()
	for _, ex := range exs {
		_, err := fake.Examples().Create(ctx, ex)
		require.NoError(t, err)
	}
	return newFlowExampleResolver(fake.Examples())
}

func TestFlowExampleResolver_ExampleFound(t *testing.T) {
	r := newSeededFakeExamples(t, domain.SavedExample{
		ID: "create-qcom-order", Operation: "order-service.createOrder",
		Body: map[string]any{"customerId": "c1"},
	})
	ex, ok := r.Example(context.Background(), "create-qcom-order")
	require.True(t, ok)
	require.NotNil(t, ex)
	assert.Equal(t, "order-service.createOrder", ex.Operation)
}

func TestFlowExampleResolver_ExampleUnknown(t *testing.T) {
	r := newSeededFakeExamples(t)
	ex, ok := r.Example(context.Background(), "nope")
	assert.False(t, ok)
	assert.Nil(t, ex)
}

func TestFlowExampleResolver_SuggestExamples(t *testing.T) {
	r := newSeededFakeExamples(t,
		domain.SavedExample{ID: "create-qcom-order", Operation: "order-service.createOrder"},
		domain.SavedExample{ID: "allocate-happy-path", Operation: "allocation-service.allocate"},
	)
	sugg := r.SuggestExamples(context.Background(), "create-qcom-ordr", 5)
	require.NotEmpty(t, sugg)
	assert.Equal(t, "create-qcom-order", sugg[0])
}

func TestFlowExampleResolver_SuggestExamples_NoExamples(t *testing.T) {
	r := newSeededFakeExamples(t)
	assert.Empty(t, r.SuggestExamples(context.Background(), "anything", 5))
}

// TestFlows_Validate_UnknownExample checks that a step naming an example id
// the workspace has never heard of reports UNKNOWN_EXAMPLE through the real
// engine (no example named "does-not-exist" was ever created here).
func TestFlows_Validate_UnknownExample(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	src := "version: 1\nid: reuse\nsteps:\n  - id: create\n    example: does-not-exist\n"
	result, err := l.Flows().Validate(ctx, src)
	require.NoError(t, err)
	require.False(t, result.Valid)
	found := false
	for _, d := range result.Diagnostics {
		if d.Code == "UNKNOWN_EXAMPLE" {
			found = true
		}
	}
	assert.True(t, found, "%+v", result.Diagnostics)
}

// TestFlows_Create_And_Run_ExampleStep is the end-to-end case: a real saved
// example (created through l.Examples(), backed by internal/example.Store)
// resolved by a flow step, created, then run to completion against the
// fixture mock servers (setupEngineWithMock, the same harness
// TestRunner_RunFlow_SavedFlow uses) -- proving Validate, Create, and a run
// driven through Flows().Get all see the materialized step, and that the
// runner itself needed no changes to execute one.
func TestFlows_Create_And_Run_ExampleStep(t *testing.T) {
	me := setupEngineWithMock(t)
	l := me.l
	ctx := context.Background()

	_, err := l.Examples().Create(ctx, domain.SavedExample{
		ID:        "create-qcom-order",
		Operation: "order-service.createOrder",
		Body: map[string]any{
			"customerId": "cust_123",
			"type":       "QCOM",
			"pickup":     map[string]any{"lat": 12.9716, "lng": 77.5946},
			"drop":       map[string]any{"lat": 12.9352, "lng": 77.6146},
		},
	})
	require.NoError(t, err)

	const flowYAML = `version: 1
id: qcom-allocation-via-example
steps:
  - id: create
    example: create-qcom-order
    extract:
      orderId: body.orderId
    assert:
      - status == 201

  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract:
      riderId: body.riderId
    assert:
      - status == 201

  - id: rider
    call: rider-service.getRider
    input: { riderId: "${steps.allocate.out.riderId}" }
    assert:
      - status == 200
      - body.online == true
`
	created, err := l.Flows().Create(ctx, flowYAML, "")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", created.Steps[0].Call, "Create returns the materialized flow")
	assert.Equal(t, "create-qcom-order", created.Steps[0].Example, "Example is kept for display")

	// A fresh read sees the same materialized step (Get -> materializeFlow).
	got, err := l.Flows().Get(ctx, "qcom-allocation-via-example")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", got.Steps[0].Call)

	run, err := l.Runner().RunFlow(ctx, "qcom-allocation-via-example", engine.RunOptions{Environment: "test"})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps)
	require.Len(t, run.Steps, 3)
	require.NotNil(t, run.Steps[0].Request, "the example's body should have reached the wire")
	assert.Equal(t, "QCOM", run.Steps[0].Request.Body.(map[string]any)["type"])
}

// TestFlows_NonExampleFlows_Unaffected is a regression check that a flow
// with no `example:` steps anywhere validates, creates, and reads back
// identically to before this feature (materialization is a no-op for it).
func TestFlows_NonExampleFlows_Unaffected(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	result, err := l.Flows().Validate(ctx, validFlowYAML)
	require.NoError(t, err)
	assert.True(t, result.Valid, "%+v", result.Diagnostics)

	f, err := l.Flows().Create(ctx, validFlowYAML, "")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", f.Steps[0].Call)

	got, err := l.Flows().Get(ctx, "order-allocation")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", got.Steps[0].Call)
	assert.Empty(t, got.Steps[0].Example)
}
