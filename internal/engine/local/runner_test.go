package local

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/env"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/workspace"
)

// TestRunner_RunFlowSource_Success runs the PRD §48 success scenario (create
// a QCOM order, allocate, fetch the rider) via RunFlowSource against the
// real fixture mock servers, and checks the run is persisted with redacted
// records and that run.started/run.step/run.finished events are observed.
func TestRunner_RunFlowSource_Success(t *testing.T) {
	me := setupEngineWithMock(t)
	l := me.l
	ctx := context.Background()

	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	run, err := l.Runner().RunFlowSource(ctx, successFlowYAML, engine.RunOptions{Environment: "test", Trigger: "cli"})
	require.NoError(t, err)
	require.NotNil(t, run)

	assert.Equal(t, domain.RunPassed, run.Status)
	require.Len(t, run.Steps, 3)
	assert.NoError(t, RunError(run))

	// Persisted, with steps, retrievable through Runs().Get.
	persisted, err := l.Runs().Get(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, persisted.Status)
	require.Len(t, persisted.Steps, 3)
	for _, st := range persisted.Steps {
		require.NotNil(t, st.Request, "step %s should have a redacted request record", st.StepID)
	}

	list, err := l.Runs().List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	found := false
	for _, r := range list {
		if r.ID == run.ID {
			found = true
		}
	}
	assert.True(t, found, "expected run %s in Runs().List", run.ID)

	seenStarted, seenStep, seenFinished := false, false, false
	deadline := time.After(2 * time.Second)
	for !(seenStarted && seenStep && seenFinished) {
		select {
		case ev := <-ch:
			switch ev.Type {
			case domain.EventRunStarted:
				seenStarted = true
			case domain.EventRunStep:
				seenStep = true
			case domain.EventRunFinished:
				seenFinished = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for run events (started=%v step=%v finished=%v)", seenStarted, seenStep, seenFinished)
		}
	}
}

// TestRunner_RunFlowSource_AssertionFailure forces the rider step's
// assertion to fail and checks the run fails with RunError reporting
// E_ASSERTION_FAILED.
func TestRunner_RunFlowSource_AssertionFailure(t *testing.T) {
	me := setupEngineWithMock(t)
	l := me.l
	ctx := context.Background()

	failingYAML := `version: 1
id: qcom-allocation-failing
steps:
  - id: create
    call: order-service.createOrder
    body: { customerId: cust_1, type: QCOM, pickup: {lat: 1, lng: 1}, drop: {lat: 1, lng: 1} }
    extract: { orderId: body.orderId }
    assert: [status == 201]
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract: { riderId: body.riderId }
    assert: [status == 201]
  - id: rider
    call: rider-service.getRider
    input: { riderId: "${steps.allocate.out.riderId}" }
    assert:
      - body.online == false
`
	run, err := l.Runner().RunFlowSource(ctx, failingYAML, engine.RunOptions{Environment: "test"})
	require.NoError(t, err)
	require.NotNil(t, run)

	assert.Equal(t, domain.RunFailed, run.Status)
	runErr := RunError(run)
	require.Error(t, runErr)
	assert.Equal(t, errs.AssertionFailed, errs.CodeOf(runErr))
}

// TestRunner_RunFlowSource_Invalid checks that an invalid flow source is
// rejected before anything runs.
func TestRunner_RunFlowSource_Invalid(t *testing.T) {
	me := setupEngineWithMock(t)
	ctx := context.Background()

	_, err := me.l.Runner().RunFlowSource(ctx, invalidFlowYAML, engine.RunOptions{Environment: "test"})
	require.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
}

// TestRunner_Call_OneStep exercises Call() directly against getRider.
func TestRunner_Call_OneStep(t *testing.T) {
	me := setupEngineWithMock(t)
	ctx := context.Background()

	run, err := me.l.Runner().Call(ctx, engine.CallRequest{
		Operation: "rider-service.getRider",
		Params:    map[string]any{"riderId": "R123"},
		Env:       "test",
		Trigger:   "cli",
	})
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, domain.RunPassed, run.Status)
	require.Len(t, run.Steps, 1)
	assert.Equal(t, "rider-service.getRider", run.Steps[0].Operation)
}

// TestRunner_RunFlow_SavedFlow exercises RunFlow(id) against a flow created
// through Flows().Create.
func TestRunner_RunFlow_SavedFlow(t *testing.T) {
	me := setupEngineWithMock(t)
	l := me.l
	ctx := context.Background()

	_, err := l.Flows().Create(ctx, successFlowYAML, "")
	require.NoError(t, err)

	run, err := l.Runner().RunFlow(ctx, "qcom-allocation", engine.RunOptions{Environment: "test"})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status)
	assert.Equal(t, "qcom-allocation", run.FlowID)
}

// TestRunner_ProductionBlocked checks that a production environment refuses
// to run without AllowProduction.
func TestRunner_ProductionBlocked(t *testing.T) {
	ws, _ := setupWorkspace(t)
	prodEnv := &domain.Environment{Version: 1, Name: "prod", Production: true}
	require.NoError(t, workspace.SaveEnvironment(ws, prodEnv))

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Runner().RunFlowSource(ctx, validFlowYAML, engine.RunOptions{Environment: "prod"})
	require.Error(t, err)
	assert.Equal(t, errs.ProductionBlocked, errs.CodeOf(err))

	// The Call() path is guarded the same way.
	_, err = l.Runner().Call(ctx, engine.CallRequest{Operation: "order-service.createOrder", Env: "prod"})
	require.Error(t, err)
	assert.Equal(t, errs.ProductionBlocked, errs.CodeOf(err))
}

func TestRunner_Cancel_UnknownRun(t *testing.T) {
	me := setupEngineWithMock(t)
	err := me.l.Runner().Cancel(context.Background(), "run_does_not_exist")
	require.Error(t, err)
	assert.Equal(t, errs.RunNotFound, errs.CodeOf(err))
}
