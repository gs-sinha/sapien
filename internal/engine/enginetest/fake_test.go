package enginetest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/engine/enginetest"
	"github.com/growsimplee/sapien/internal/errs"
)

func seeded(t *testing.T) *enginetest.Fake {
	t.Helper()
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	f := enginetest.New(ws)
	enginetest.Seed(f)
	return f
}

func TestSeedPopulatesExpectedCounts(t *testing.T) {
	f := seeded(t)
	ctx := context.Background()

	services, err := f.Services().List(ctx)
	require.NoError(t, err)
	assert.Len(t, services, 2)

	var opCount int
	for _, svc := range services {
		ops, err := f.Catalog().ListOperations(ctx, svc.Name)
		require.NoError(t, err)
		opCount += len(ops)
	}
	assert.Equal(t, 4, opCount)

	docs, err := f.Catalog().ListDocs(ctx, "")
	require.NoError(t, err)
	assert.Len(t, docs, 1)

	flows, err := f.Flows().List(ctx, "")
	require.NoError(t, err)
	assert.Len(t, flows, 2)

	runs, err := f.Runs().List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Empty(t, runs[0].Steps, "List should omit steps")

	full, err := f.Runs().Get(ctx, runs[0].ID)
	require.NoError(t, err)
	assert.Len(t, full.Steps, 2)

	mems, err := f.Memories().List(ctx, domain.MemoryQuery{})
	require.NoError(t, err)
	assert.Len(t, mems, 3)

	envs, err := f.Envs().List(ctx)
	require.NoError(t, err)
	require.Len(t, envs, 2)
	var sawProd bool
	for _, e := range envs {
		if e.Production {
			sawProd = true
		}
	}
	assert.True(t, sawProd)
}

func TestGetOperationNotFound(t *testing.T) {
	f := seeded(t)
	_, err := f.Catalog().GetOperation(context.Background(), "nope.doesNotExist")
	require.Error(t, err)
	assert.Equal(t, errs.OperationNotFound, errs.CodeOf(err))
	e := errs.As(err)
	assert.Equal(t, "nope.doesNotExist", e.Details["id"])
}

func TestResolveOperationByMethodAndPath(t *testing.T) {
	f := seeded(t)
	op, err := f.Catalog().ResolveOperation(context.Background(), "GET /v1/riders/{riderId}")
	require.NoError(t, err)
	assert.Equal(t, "rider-service.getRider", op.ID)
}

func TestResolveOperationByBareID(t *testing.T) {
	f := seeded(t)
	op, err := f.Catalog().ResolveOperation(context.Background(), "createOrder")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", op.ID)
}

func TestRunFlowRecordsOptsAndReturnsCannedPassedRun(t *testing.T) {
	f := seeded(t)
	ctx := context.Background()

	var observed []domain.Event
	opts := engine.RunOptions{
		Environment: "local",
		Inputs:      map[string]any{"customerId": "cust_2"},
		Trigger:     "cli",
		Observer:    func(ev domain.Event) { observed = append(observed, ev) },
	}
	run, err := f.Runner().RunFlow(ctx, "create-order-flow", opts)
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status)
	assert.Equal(t, "local", run.Environment)
	assert.NotEmpty(t, observed)

	// opts were recorded without the Observer func.
	last := f.Calls[len(f.Calls)-1]
	assert.Equal(t, "Runner.RunFlow", last.Method)
	args := last.Args.(map[string]any)
	recordedOpts := args["opts"].(engine.RunOptions)
	assert.Nil(t, recordedOpts.Observer)

	// The run must be retrievable afterward.
	got, err := f.Runs().Get(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.ID, got.ID)
}

func TestRunFlowUnknownFlow(t *testing.T) {
	f := seeded(t)
	_, err := f.Runner().RunFlow(context.Background(), "does-not-exist", engine.RunOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
}

func TestEventsPublishSubscribe(t *testing.T) {
	f := seeded(t)
	ctx, cancel := context.WithCancel(context.Background())

	ch, unsub := f.Events().Subscribe(ctx)
	defer unsub()

	f.Publish(domain.Event{Type: domain.EventCatalogChanged})
	f.Publish(domain.Event{Type: domain.EventRunStarted})
	f.Publish(domain.Event{Type: domain.EventRunFinished})

	for i := 0; i < 3; i++ {
		select {
		case ev := <-ch:
			_ = ev
		default:
			t.Fatalf("expected event %d to be buffered", i)
		}
	}

	cancel()
	// after cancellation the channel should eventually close.
	for {
		if _, ok := <-ch; !ok {
			break
		}
	}
}

func TestMemoriesRelevant(t *testing.T) {
	f := seeded(t)
	scored, err := f.Memories().Relevant(context.Background(), []domain.Subject{{Service: "order-service", Operation: "order-service.createOrder"}}, 10)
	require.NoError(t, err)
	require.NotEmpty(t, scored)
	assert.Contains(t, scored[0].Reasons, "service match")
}

func TestEnvSecretsRoundTrip(t *testing.T) {
	f := seeded(t)
	ctx := context.Background()

	require.NoError(t, f.Envs().SetSecret(ctx, "API_KEY", "sekret"))
	names, err := f.Envs().ListSecrets(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"API_KEY"}, names)

	require.NoError(t, f.Envs().DeleteSecret(ctx, "API_KEY"))
	_, err = f.Envs().Get(ctx, "does-not-exist")
	require.Error(t, err)
	assert.Equal(t, errs.EnvNotFound, errs.CodeOf(err))
}

var _ engine.Engine = (*enginetest.Fake)(nil)
