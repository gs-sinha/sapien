package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
)

// syntheticOp is a hand-built operation (not sourced from any real fixture
// contract) used to exercise binding edge cases the logistics fixtures don't
// happen to declare (an explicit header parameter, precedence between
// Input- and Params-bound values, etc).
func syntheticOp(serviceID string, responses []domain.Response) *domain.Operation {
	return &domain.Operation{
		ID:        serviceID + ".op",
		ServiceID: serviceID,
		HTTP:      &domain.HTTPBinding{Method: "GET", Path: "/items/{id}"},
		Params: []domain.Param{
			{Name: "id", In: domain.InPath},
			{Name: "q", In: domain.InQuery},
			{Name: "X-Trace", In: domain.InHeader},
		},
		Responses: responses,
	}
}

func singleServiceEnv(serviceID, baseURL string) *env.Resolved {
	e := domain.Environment{
		Version:  1,
		Name:     "test",
		Services: map[string]domain.ServiceEnv{serviceID: {BaseURL: baseURL}},
	}
	return env.Resolve(e, []domain.Service{{Name: serviceID}}, nil)
}

// capturedRequest is a snapshot of one request a captureServer received.
type capturedRequest struct {
	path  string
	query string
	trace string
	authz string
}

// captureServer is an httptest server that records the last request it
// received and replies with a configurable status/body.
type captureServer struct {
	*httptest.Server
	mu     sync.Mutex
	last   capturedRequest
	status int
	body   any
}

func newCaptureServer() *captureServer {
	cs := &captureServer{status: http.StatusOK, body: map[string]any{"ok": true}}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.last = capturedRequest{
			path:  r.URL.Path,
			query: r.URL.RawQuery,
			trace: r.Header.Get("X-Trace"),
			authz: r.Header.Get("Authorization"),
		}
		status, body := cs.status, cs.body
		cs.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	return cs
}

func (cs *captureServer) setResponse(status int, body any) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.status = status
	cs.body = body
}

func (cs *captureServer) lastRequest() capturedRequest {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.last
}

func anySchemaResponses() []domain.Response {
	return []domain.Response{{Status: "200", Schema: &domain.Schema{Kind: domain.KindAny}}}
}

func TestRun_BindParamsPrecedence(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "bind", Steps: []domain.Step{{
		ID:   "call",
		Call: "svc.op",
		Input: map[string]any{
			"id":      "1",
			"q":       "input-q",
			"X-Trace": "input-trace",
		},
		Params: &domain.ExplicitParams{
			Query:   map[string]any{"q": "explicit-q"},
			Headers: map[string]string{"X-Trace": "explicit-trace"},
		},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.RunPassed, run.Status)

	got := cs.lastRequest()
	assert.Equal(t, "/items/1", got.path)
	assert.Equal(t, "q=explicit-q", got.query)
	assert.Equal(t, "explicit-trace", got.trace)
}

func TestRun_UnknownInputParam(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "bad-input", Steps: []domain.Step{{
		ID:    "call",
		Call:  "svc.op",
		Input: map[string]any{"id": "1", "bogus": "nope"},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.RunErrored, run.Status)
	require.NotNil(t, run.Steps[0].Error)
	assert.Equal(t, string(errs.Invalid), run.Steps[0].Error.Code)
}

func TestRun_MissingBaseURL(t *testing.T) {
	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("other-svc", "http://127.0.0.1:1")
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "no-base-url", Steps: []domain.Step{{
		ID: "call", Call: "svc.op", Input: map[string]any{"id": "1"},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.RunErrored, run.Status)
	require.NotNil(t, run.Steps[0].Error)
	assert.Equal(t, string(errs.Invalid), run.Steps[0].Error.Code)
}

func TestRun_InvalidTimeout(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "bad-timeout", Steps: []domain.Step{{
		ID: "call", Call: "svc.op", Input: map[string]any{"id": "1"}, Timeout: "not-a-duration",
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.StepErrored, run.Steps[0].Status)
	assert.Equal(t, string(errs.Invalid), run.Steps[0].Error.Code)
}

func TestRun_InvalidPollDurations(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)

	for _, poll := range []*domain.Poll{
		{Interval: "bad"},
		{Timeout: "bad"},
	} {
		r := New(ops)
		f := &domain.Flow{Version: 1, ID: "bad-poll", Steps: []domain.Step{{
			ID: "call", Call: "svc.op", Input: map[string]any{"id": "1"}, Until: "status == 200", Poll: poll,
		}}}
		run, err := r.Run(context.Background(), f, nil, Options{Env: e})
		require.NoError(t, err)
		require.Equal(t, domain.StepErrored, run.Steps[0].Status)
		assert.Equal(t, string(errs.Invalid), run.Steps[0].Error.Code)
	}
}

func TestRun_ExtractError(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "extract-error", Steps: []domain.Step{{
		ID:      "call",
		Call:    "svc.op",
		Input:   map[string]any{"id": "1"},
		Extract: map[string]string{"missing": "body.doesNotExist"},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.StepErrored, run.Steps[0].Status)
	require.NotNil(t, run.Steps[0].Error)
	assert.Equal(t, string(errs.Expr), run.Steps[0].Error.Code)
	assert.Equal(t, "missing", run.Steps[0].Error.Details["extract"])
}

func TestRun_CompileAssertionError(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "bad-assert", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Path: "body.ok", Eq: true, Neq: false}}, // ambiguous: both eq and neq
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.StepFailed, run.Steps[0].Status)
	require.Len(t, run.Steps[0].Assertions, 1)
	assert.False(t, run.Steps[0].Assertions[0].Passed)
	assert.NotEmpty(t, run.Steps[0].Assertions[0].Error)
}

func TestRun_SchemaAssertion_NoSchemaForStatus(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusNotFound, map[string]any{"code": "NOT_FOUND"})

	op := syntheticOp("svc", anySchemaResponses()) // only declares "200"
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "no-schema", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Schema: "contract"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.StepFailed, run.Steps[0].Status)
	require.Len(t, run.Steps[0].Assertions, 1)
	assert.False(t, run.Steps[0].Assertions[0].Passed)
	assert.Contains(t, run.Steps[0].Assertions[0].Message, "no response schema for status 404")
}

func TestRun_SchemaAssertion_ValidationProblems(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"name": 5}) // name should be a string

	responses := []domain.Response{{
		Status: "200",
		Schema: &domain.Schema{
			Kind:       domain.KindObject,
			Properties: map[string]*domain.Schema{"name": {Kind: domain.KindString}},
			Required:   []string{"name"},
		},
	}}
	op := syntheticOp("svc", responses)
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "schema-problems", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Schema: "contract"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	require.Equal(t, domain.StepFailed, run.Steps[0].Status)
	require.Len(t, run.Steps[0].Assertions, 1)
	assert.False(t, run.Steps[0].Assertions[0].Passed)
	assert.Contains(t, run.Steps[0].Assertions[0].Message, "body.name")
}

func TestRun_SchemaAssertion_Passes(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"name": "ok"})

	responses := []domain.Response{{
		Status: "200",
		Schema: &domain.Schema{
			Kind:       domain.KindObject,
			Properties: map[string]*domain.Schema{"name": {Kind: domain.KindString}},
			Required:   []string{"name"},
		},
	}}
	op := syntheticOp("svc", responses)
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "schema-ok", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Schema: "contract"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.StepPassed, run.Steps[0].Status)
	assert.True(t, run.Steps[0].Assertions[0].Passed)
}

// ---- structured assertions with ${...} templates in eq/neq/contains/matches

func TestRun_AssertEqTemplatedStringKeepsType(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"riderId": "R123", "echo": "R123"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "eq-templated-string", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Path: "body.riderId", Eq: "${body.echo}"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status)
	require.Len(t, run.Steps[0].Assertions, 1)
	a := run.Steps[0].Assertions[0]
	assert.True(t, a.Passed, "assertion should pass: %s", a.Error)
	assert.Equal(t, `body.riderId == "R123"`, a.Expr, "the interpolated literal must be recorded in Expr")
}

func TestRun_AssertEqTemplatedNumberKeepsType(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"count": 5, "echoCount": 5})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "eq-templated-number", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Path: "body.count", Eq: "${body.echoCount}"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status)
	require.Len(t, run.Steps[0].Assertions, 1)
	a := run.Steps[0].Assertions[0]
	assert.True(t, a.Passed, "assertion should pass: %s", a.Error)
	assert.Equal(t, "body.count == 5", a.Expr, "a templated eq against a number must stay numeric, not become a quoted string")
}

func TestRun_AssertNeqTemplated(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"riderId": "R123", "other": "R124"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "neq-templated", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Path: "body.riderId", Neq: "${body.other}"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status)
	assert.True(t, run.Steps[0].Assertions[0].Passed)
}

func TestRun_AssertContainsTemplated(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"name": "Rider R123 online", "tag": "R123"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "contains-templated", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Path: "body.name", Contains: "${body.tag}"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status)
	assert.True(t, run.Steps[0].Assertions[0].Passed)
}

func TestRun_AssertMatchesTemplated(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"riderId": "R123", "pattern": "^R123$"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "matches-templated", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Path: "body.riderId", Matches: "${body.pattern}"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status)
	assert.True(t, run.Steps[0].Assertions[0].Passed)
}

func TestRun_AssertTemplateInterpolationError(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"riderId": "R123"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "templated-eq-error", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Path: "body.riderId", Eq: "${body.missingField}"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunFailed, run.Status, "an interpolation error fails the assertion, not the step")
	require.Len(t, run.Steps[0].Assertions, 1)
	a := run.Steps[0].Assertions[0]
	assert.False(t, a.Passed)
	assert.NotEmpty(t, a.Error)
	assert.Equal(t, domain.StepFailed, run.Steps[0].Status)
	assert.Nil(t, run.Steps[0].Error, "the step itself must not error")
}

func TestRun_AssertTemplateSecretRejected(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"riderId": "R123"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "templated-eq-secret", Steps: []domain.Step{{
		ID:     "call",
		Call:   "svc.op",
		Input:  map[string]any{"id": "1"},
		Assert: []domain.Assertion{{Path: "body.riderId", Eq: "${secret.API_KEY}"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunFailed, run.Status)
	require.Len(t, run.Steps[0].Assertions, 1)
	a := run.Steps[0].Assertions[0]
	assert.False(t, a.Passed)
	assert.Contains(t, a.Error, "secret")
	assert.Equal(t, domain.StepFailed, run.Steps[0].Status)
}
