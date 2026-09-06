package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/env"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/events"
	"github.com/growsimplee/sapien/internal/runs"
	"github.com/growsimplee/sapien/internal/store"

	"github.com/growsimplee/sapien/fixtures/logistics/mock"
)

func intp(v int) *int    { return &v }
func boolp(v bool) *bool { return &v }

// createOrderStep is the "create a QCOM order" step shared by several tests.
func createOrderStep() domain.Step {
	return domain.Step{
		ID:   "create",
		Call: "order-service.createOrder",
		Body: map[string]any{
			"customerId": "${inputs.customerId}",
			"type":       "QCOM",
			"pickup":     map[string]any{"lat": 12.9716, "lng": 77.5946},
			"drop":       map[string]any{"lat": 12.9352, "lng": 77.6146},
		},
		Extract: map[string]string{"orderId": "body.orderId"},
		Assert:  []domain.Assertion{{Status: intp(201)}},
	}
}

func allocateStep() domain.Step {
	return domain.Step{
		ID:      "allocate",
		Call:    "allocation-service.allocate",
		Body:    map[string]any{"orderId": "${steps.create.out.orderId}"},
		Until:   "status == 201",
		Poll:    &domain.Poll{Interval: "1ms", Timeout: "1s"},
		Extract: map[string]string{"riderId": "body.riderId"},
	}
}

func riderStep() domain.Step {
	return domain.Step{
		ID:   "rider",
		Call: "rider-service.getRider",
		Input: map[string]any{
			"riderId": "${steps.allocate.out.riderId}",
		},
		Assert: []domain.Assertion{
			{Expr: "status == 200"},
			{Expr: "body.online == true"},
			{Schema: "contract"},
			{Path: "body.qcomSkill", Eq: true, Message: "QCOM riders must have qcomSkill"},
		},
		Extract: map[string]string{"riderId": "body.riderId"},
	}
}

func successFlow() *domain.Flow {
	return &domain.Flow{
		Version: 1,
		ID:      "success",
		Source:  "version: 1\nid: success\n",
		Inputs: map[string]domain.InputSpec{
			"customerId": {Type: "string", Default: "cust_123"},
		},
		Steps: []domain.Step{createOrderStep(), allocateStep(), riderStep()},
	}
}

func startFixtures(t *testing.T) (ops Operations, e *env.Resolved, world *mock.World) {
	t.Helper()
	world = mock.NewWorld()
	world.SetTimelineDelay(0)
	orderURL, allocURL, riderURL, _ := mock.StartAll(t, mock.Options{World: world})
	ops = buildTestOperations(t)
	e = buildTestEnv("test", false, orderURL, allocURL, riderURL)
	return ops, e, world
}

func TestRun_SuccessScenario(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	run, err := r.Run(context.Background(), successFlow(), nil, Options{Env: e})
	require.NoError(t, err)
	require.NotNil(t, run)

	assert.Equal(t, domain.RunPassed, run.Status)
	assert.NoError(t, ErrorFor(run))
	require.Len(t, run.Steps, 3)

	create, allocate, rider := run.Steps[0], run.Steps[1], run.Steps[2]

	assert.Equal(t, domain.StepPassed, create.Status)
	require.NotNil(t, create.Response)
	assert.Equal(t, 201, create.Response.Status)
	require.NotNil(t, create.Timings)
	assert.Greater(t, create.Timings.TotalMs, 0.0)
	orderID, _ := create.Out["orderId"].(string)
	assert.NotEmpty(t, orderID)

	assert.Equal(t, domain.StepPassed, allocate.Status)
	assert.GreaterOrEqual(t, allocate.Attempts, 1)
	riderID, _ := allocate.Out["riderId"].(string)
	assert.NotEmpty(t, riderID)

	assert.Equal(t, domain.StepPassed, rider.Status)
	require.Len(t, rider.Assertions, 4)
	for _, a := range rider.Assertions {
		assert.True(t, a.Passed, "assertion %q should pass: %s %s", a.Expr, a.Message, a.Error)
	}
	assert.Equal(t, riderID, rider.Out["riderId"])

	assert.Equal(t, 3, run.Summary.StepsTotal)
	assert.Equal(t, 3, run.Summary.StepsPassed)
	assert.Contains(t, run.OperationHashes, "order-service.createOrder")
	assert.Contains(t, run.OperationHashes, "allocation-service.allocate")
	assert.Contains(t, run.OperationHashes, "rider-service.getRider")
}

func TestRun_AssertionFailure(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := successFlow()
	// Force the rider step's assertion to fail: assert offline when the
	// rider is actually online.
	f.Steps[2].Assert = []domain.Assertion{{Expr: "body.online == false"}}
	f.Steps = append(f.Steps, domain.Step{
		ID:    "after",
		Call:  "rider-service.getRider",
		Input: map[string]any{"riderId": "${steps.allocate.out.riderId}"},
	})

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	assert.Equal(t, domain.RunFailed, run.Status)
	require.Len(t, run.Steps, 4)
	assert.Equal(t, domain.StepPassed, run.Steps[0].Status)
	assert.Equal(t, domain.StepPassed, run.Steps[1].Status)
	assert.Equal(t, domain.StepFailed, run.Steps[2].Status)
	assert.False(t, run.Steps[2].Assertions[0].Passed)
	assert.Equal(t, domain.StepSkipped, run.Steps[3].Status)

	err = ErrorFor(run)
	require.Error(t, err)
	assert.Equal(t, errs.AssertionFailed, errs.CodeOf(err))
	e2 := errs.As(err)
	assert.Equal(t, []string{"rider"}, e2.Details["failed_steps"])
	assert.Equal(t, run.ID, e2.Details["run_id"])
	assert.Equal(t, 1, errs.ExitCode(err))
}

func TestRun_ContinueOnFailure(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := successFlow()
	f.Steps[2].Assert = []domain.Assertion{{Expr: "body.online == false"}}
	f.Steps = append(f.Steps, domain.Step{
		ID:    "after",
		Call:  "rider-service.getRider",
		Input: map[string]any{"riderId": "${steps.allocate.out.riderId}"},
	})

	run, err := r.Run(context.Background(), f, nil, Options{Env: e, ContinueOnFailure: true})
	require.NoError(t, err)

	assert.Equal(t, domain.RunFailed, run.Status)
	require.Len(t, run.Steps, 4)
	assert.Equal(t, domain.StepFailed, run.Steps[2].Status)
	assert.Equal(t, domain.StepPassed, run.Steps[3].Status, "with ContinueOnFailure, later steps still run")
}

func TestRun_UntilTimeout(t *testing.T) {
	ops, e, world := startFixtures(t)
	world.SetTimelineDelay(time.Hour) // never ready within the test

	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "timeout",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Steps: []domain.Step{
			createOrderStep(),
			{
				ID:    "timeline",
				Call:  "order-service.getOrderTimeline",
				Input: map[string]any{"orderId": "${steps.create.out.orderId}"},
				Until: "status == 200",
				Poll:  &domain.Poll{Interval: "1ms", Timeout: "2500ms"},
			},
		},
	}

	// A fake, monotonically-advancing clock: each call adds one second, so
	// the poll loop's own timeout check trips deterministically without any
	// real wall-clock waiting (Sleep below is instant too).
	callCount := 0
	fakeNow := func() time.Time {
		callCount++
		return time.Unix(0, 0).Add(time.Duration(callCount) * time.Second)
	}

	run, err := r.Run(context.Background(), f, nil, Options{
		Env:   e,
		Now:   fakeNow,
		Sleep: instantSleep,
	})
	require.NoError(t, err)

	assert.Equal(t, domain.RunFailed, run.Status)
	require.Len(t, run.Steps, 2)
	timeline := run.Steps[1]
	assert.Equal(t, domain.StepFailed, timeline.Status)
	require.NotNil(t, timeline.Error)
	assert.Equal(t, string(errs.UntilTimeout), timeline.Error.Code)
	attempts, _ := timeline.Error.Details["attempts"].(int)
	assert.Greater(t, attempts, 1)
	assert.Equal(t, timeline.Attempts, attempts)
	lastStatus, _ := timeline.Error.Details["last_status"].(int)
	assert.Equal(t, 404, lastStatus)
	require.NotNil(t, timeline.Response)
	assert.Equal(t, 404, timeline.Response.Status)
}

func TestRun_TransportError(t *testing.T) {
	ops, e, _ := startFixtures(t)
	// Point order-service at a port nothing is listening on.
	e.BaseURLs["order-service"] = "http://127.0.0.1:1"

	r := New(ops)
	f := &domain.Flow{
		Version: 1,
		ID:      "transport-error",
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Default: "cust_1"}},
		Steps:   []domain.Step{createOrderStep()},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	assert.Equal(t, domain.RunErrored, run.Status)
	require.Len(t, run.Steps, 1)
	assert.Equal(t, domain.StepErrored, run.Steps[0].Status)
	require.NotNil(t, run.Steps[0].Error)
	assert.Equal(t, string(errs.HTTPTransport), run.Steps[0].Error.Code)

	cerr := ErrorFor(run)
	require.Error(t, cerr)
	assert.Equal(t, errs.Internal, errs.CodeOf(cerr))
	assert.Equal(t, 2, errs.ExitCode(cerr))
}

func TestRun_SecretHeaderRedactedButSent(t *testing.T) {
	ops, e, _ := startFixtures(t)

	var mu sync.Mutex
	var receivedAuth string
	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		receivedAuth = req.Header.Get("X-Api-Key")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"riderId": "R123", "name": "Asha Rao", "online": true,
			"qcomSkill": true, "upcomingTrips": 1, "city": "Bangalore",
		})
	}))
	defer capture.Close()
	e.BaseURLs["rider-service"] = capture.URL

	secrets := env.NewMemoryStore()
	require.NoError(t, secrets.Set("API_KEY", "sk-super-secret-value-xyz"))

	r := New(ops)
	f := &domain.Flow{
		Version: 1,
		ID:      "secret-header",
		Steps: []domain.Step{{
			ID:      "get",
			Call:    "rider-service.getRider",
			Input:   map[string]any{"riderId": "R123"},
			Headers: map[string]string{"X-Api-Key": "${secret.API_KEY}"},
		}},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e, Secrets: secrets})
	require.NoError(t, err)
	require.Equal(t, domain.RunPassed, run.Status)

	mu.Lock()
	got := receivedAuth
	mu.Unlock()
	assert.Equal(t, "sk-super-secret-value-xyz", got, "the mock must receive the real secret value")

	require.NotNil(t, run.Steps[0].Request)
	assert.Equal(t, "[REDACTED]", run.Steps[0].Request.Headers["X-Api-Key"], "the persisted record must redact it")
}

func TestRun_SecretInBodyErrors(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "secret-in-body",
		Steps: []domain.Step{{
			ID:   "create",
			Call: "order-service.createOrder",
			Body: map[string]any{
				"customerId": "${secret.CUSTOMER_TOKEN}",
				"type":       "QCOM",
				"pickup":     map[string]any{"lat": 1.0, "lng": 2.0},
				"drop":       map[string]any{"lat": 1.0, "lng": 2.0},
			},
		}},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)

	assert.Equal(t, domain.RunErrored, run.Status)
	require.Len(t, run.Steps, 1)
	assert.Equal(t, domain.StepErrored, run.Steps[0].Status)
	require.NotNil(t, run.Steps[0].Error)
	assert.Equal(t, string(errs.Expr), run.Steps[0].Error.Code)
}

func TestRun_MissingRequiredInput(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	f := &domain.Flow{
		Version: 1,
		ID:      "missing-input",
		Inputs:  map[string]domain.InputSpec{"city": {Type: "string", Required: true}},
		Steps:   []domain.Step{createOrderStep()},
	}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.Error(t, err)
	assert.Nil(t, run)
	assert.Equal(t, errs.InputMissing, errs.CodeOf(err))
	assert.Equal(t, "city", errs.As(err).Details["input"])
}

func TestRun_ProductionBlocked(t *testing.T) {
	ops, e, _ := startFixtures(t)
	e.Env.Production = true
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "prod", Steps: []domain.Step{createOrderStep()}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.Error(t, err)
	assert.Nil(t, run)
	assert.Equal(t, errs.ProductionBlocked, errs.CodeOf(err))

	run, err = r.Run(context.Background(), f, map[string]any{"customerId": "c1"}, Options{Env: e, AllowProduction: true})
	require.NoError(t, err)
	require.NotNil(t, run)
}

func TestRun_EnvNil(t *testing.T) {
	r := New(mapOperations{ops: map[string]*domain.Operation{}})
	run, err := r.Run(context.Background(), &domain.Flow{}, nil, Options{})
	require.Error(t, err)
	assert.Nil(t, run)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestRun_OperationNotFound(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)
	f := &domain.Flow{Version: 1, ID: "bad-op", Steps: []domain.Step{{ID: "x", Call: "nope.nope"}}}
	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.Error(t, err)
	assert.Nil(t, run)
	assert.Equal(t, errs.OperationNotFound, errs.CodeOf(err))
}

func TestRun_CancellationMidRun(t *testing.T) {
	ops, e, _ := startFixtures(t)

	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"orderId": "ord_1", "status": "CREATED"})
	}))
	defer slow.Close()
	defer close(release)
	e.BaseURLs["order-service"] = slow.URL

	r := New(ops)
	f := &domain.Flow{
		Version: 1,
		ID:      "cancel",
		Steps: []domain.Step{
			createOrderStep(),
			{ID: "second", Call: "order-service.createOrder", Body: map[string]any{
				"customerId": "cust_2", "type": "QCOM",
				"pickup": map[string]any{"lat": 1.0, "lng": 2.0},
				"drop":   map[string]any{"lat": 1.0, "lng": 2.0},
			}},
		},
	}

	var runID string
	var once sync.Once
	observer := func(ev domain.Event) {
		if ev.Type == domain.EventRunStarted {
			run, ok := ev.Payload.(*domain.Run)
			if !ok {
				return
			}
			once.Do(func() {
				runID = run.ID
				go func() {
					time.Sleep(30 * time.Millisecond)
					if !r.Cancel(runID) {
						t.Errorf("Cancel(%q) returned false while run should be active", runID)
					}
				}()
			})
		}
	}

	run, err := r.Run(context.Background(), f, map[string]any{"customerId": "cust_1"}, Options{Env: e, Observer: observer})
	require.NoError(t, err)
	require.NotNil(t, run)

	assert.Equal(t, domain.RunCancelled, run.Status)
	require.Len(t, run.Steps, 2)
	assert.Equal(t, domain.StepCancelled, run.Steps[0].Status)
	assert.Equal(t, domain.StepSkipped, run.Steps[1].Status)

	cerr := ErrorFor(run)
	require.Error(t, cerr)
	assert.Equal(t, errs.Cancelled, errs.CodeOf(cerr))
	assert.Equal(t, 2, errs.ExitCode(cerr))

	assert.False(t, r.Cancel(runID), "Cancel should return false for a finished run")
	assert.False(t, r.Cancel("run_does_not_exist"))
}

func TestCall_OneStep(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	run, err := r.Call(context.Background(), "rider-service.getRider", map[string]any{"riderId": "R123"}, nil, nil, Options{Env: e})
	require.NoError(t, err)
	require.NotNil(t, run)

	assert.Equal(t, domain.RunPassed, run.Status)
	assert.Equal(t, "call", run.FlowID)
	assert.Contains(t, run.FlowSnapshot, "call: rider-service.getRider")
	require.Len(t, run.Steps, 1)
	assert.Equal(t, "rider-service.getRider", run.Steps[0].Operation)
}

func TestRun_PersistenceRoundTrip(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)

	db, err := store.Open(":memory:")
	require.NoError(t, err)
	rs := runs.New(db)

	run, err := r.Run(context.Background(), successFlow(), nil, Options{Env: e, Runs: rs, Trigger: "cli"})
	require.NoError(t, err)
	require.NotNil(t, run)

	loaded, err := rs.Get(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.Status, loaded.Status)
	assert.Equal(t, run.FlowID, loaded.FlowID)
	assert.Equal(t, "cli", loaded.Trigger)
	require.Len(t, loaded.Steps, 3)
	assert.Equal(t, "create", loaded.Steps[0].StepID)
	assert.Equal(t, "allocate", loaded.Steps[1].StepID)
	assert.Equal(t, "rider", loaded.Steps[2].StepID)
	assert.NotEmpty(t, loaded.Steps[2].Out["riderId"])
}

func TestRun_EventsOrder(t *testing.T) {
	ops, e, _ := startFixtures(t)
	bus := events.New()
	sub, unsubscribe := bus.Subscribe(context.Background())
	defer unsubscribe()

	r := New(ops)
	f := &domain.Flow{
		Version: 1,
		ID:      "events",
		Steps:   []domain.Step{createOrderStep()},
	}

	var collected []domain.Event
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range sub {
			collected = append(collected, ev)
		}
	}()

	run, err := r.Run(context.Background(), f, map[string]any{"customerId": "cust_1"}, Options{Env: e, Bus: bus})
	require.NoError(t, err)
	require.Equal(t, domain.RunPassed, run.Status)

	unsubscribe()
	wg.Wait()

	require.NotEmpty(t, collected)
	assert.Equal(t, domain.EventRunStarted, collected[0].Type)
	assert.Equal(t, domain.EventRunFinished, collected[len(collected)-1].Type)

	var sawResolving, sawRequesting, sawPassed bool
	for _, ev := range collected {
		if ev.Type != domain.EventRunStep {
			continue
		}
		payload, ok := ev.Payload.(map[string]any)
		require.True(t, ok)
		switch payload["status"] {
		case string(domain.StepResolving):
			sawResolving = true
		case string(domain.StepRequesting):
			sawRequesting = true
		case string(domain.StepPassed):
			sawPassed = true
		}
	}
	assert.True(t, sawResolving)
	assert.True(t, sawRequesting)
	assert.True(t, sawPassed)
}

func TestErrorFor_Passed(t *testing.T) {
	assert.NoError(t, ErrorFor(&domain.Run{Status: domain.RunPassed}))
}

func TestErrorFor_Nil(t *testing.T) {
	assert.NoError(t, ErrorFor(nil))
}

func TestErrorFor_UnknownStatus(t *testing.T) {
	assert.NoError(t, ErrorFor(&domain.Run{Status: domain.RunQueued}))
}

func TestFindParam(t *testing.T) {
	op := &domain.Operation{Params: []domain.Param{
		{Name: "riderId", In: domain.InPath},
		{Name: "X-Request-Id", In: domain.InHeader},
	}}
	p, ok := findParam(op, "riderId")
	require.True(t, ok)
	assert.Equal(t, domain.InPath, p.In)

	p, ok = findParam(op, "x-request-id")
	require.True(t, ok)
	assert.Equal(t, domain.InHeader, p.In)

	_, ok = findParam(op, "nope")
	assert.False(t, ok)
}

func TestHeaderString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"x", "x"},
		{true, "true"},
		{int64(5), "5"},
		{float64(5), "5"},
		{float64(5.5), "5.5"},
		{7, "7"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, headerString(c.in), "headerString(%v)", c.in)
	}
}

func TestPickResponseSchema(t *testing.T) {
	s200 := &domain.Schema{Kind: domain.KindString}
	s2xx := &domain.Schema{Kind: domain.KindBoolean}
	sdef := &domain.Schema{Kind: domain.KindNumber}
	op := &domain.Operation{Responses: []domain.Response{
		{Status: "200", Schema: s200},
		{Status: "2XX", Schema: s2xx},
		{Status: "default", Schema: sdef},
	}}

	got, ok := pickResponseSchema(op, 200)
	require.True(t, ok)
	assert.Same(t, s200, got)

	got, ok = pickResponseSchema(op, 201)
	require.True(t, ok)
	assert.Same(t, s2xx, got)

	got, ok = pickResponseSchema(op, 500)
	require.True(t, ok)
	assert.Same(t, sdef, got)

	_, ok = pickResponseSchema(&domain.Operation{}, 200)
	assert.False(t, ok)
}
