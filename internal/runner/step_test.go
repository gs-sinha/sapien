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

// ---- BUG A: `${...}` inside bare CEL (assert/expr) compares the native
// extracted value, not the literal template text ------------------------

// TestRun_BareAssertTemplateComparesNativeValue is BUG A's repro made a
// runner test: 'body.orderId != "${steps.a.out.id}"' used to leave the
// template as literal text inside the CEL string, so this assertion always
// passed no matter what steps.a.out.id actually was. With equal values it
// must now correctly evaluate the comparison and FAIL.
func TestRun_BareAssertTemplateComparesNativeValue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "X1"})
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"orderId": "X1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opA := &domain.Operation{ID: "svc.a", ServiceID: "svc", HTTP: &domain.HTTPBinding{Method: "GET", Path: "/a"}, Responses: anySchemaResponses()}
	opB := &domain.Operation{ID: "svc.b", ServiceID: "svc", HTTP: &domain.HTTPBinding{Method: "GET", Path: "/b"}, Responses: anySchemaResponses()}
	ops := mapOperations{ops: map[string]*domain.Operation{opA.ID: opA, opB.ID: opB}}
	e := singleServiceEnv("svc", srv.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "bare-template-compare", Steps: []domain.Step{
		{ID: "a", Call: "svc.a", Extract: map[string]string{"id": "body.id"}},
		{ID: "b", Call: "svc.b", Assert: []domain.Assertion{{Expr: `body.orderId != "${steps.a.out.id}"`}}},
	}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunFailed, run.Status, "orderId equals steps.a.out.id, so != must now evaluate false")
	require.Len(t, run.Steps[1].Assertions, 1)
	a := run.Steps[1].Assertions[0]
	assert.False(t, a.Passed, "a templated bare CEL assertion must compare the native extracted value, not the literal template text")
	assert.Equal(t, `body.orderId != "${steps.a.out.id}"`, a.Expr, "Expr still shows the user's original text")
	assert.Equal(t, "X1", a.Actual)
}

// ---- BUG B: out.<name> in assert/until (extract runs before assert) ------

func TestRun_AssertUsesOut(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"orderId": "ORD1"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "out-in-assert", Steps: []domain.Step{{
		ID:      "call",
		Call:    "svc.op",
		Input:   map[string]any{"id": "1"},
		Extract: map[string]string{"tid": "body.orderId"},
		Assert:  []domain.Assertion{{Expr: "out.tid == body.orderId"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps[0])
	require.Len(t, run.Steps[0].Assertions, 1)
	assert.True(t, run.Steps[0].Assertions[0].Passed, run.Steps[0].Assertions[0].Error)
	assert.Equal(t, "ORD1", run.Steps[0].Out["tid"])
}

// TestRun_AssertOutInsideListMacro confirms `out` works inside a CEL
// comprehension macro too (not just a top-level comparison): a list.exists
// closure referencing out.<name>.
func TestRun_AssertOutInsideListMacro(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{
		"items": []any{
			map[string]any{"id": "R1"},
			map[string]any{"id": "R2"},
		},
	})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "out-in-list-macro", Steps: []domain.Step{{
		ID:      "call",
		Call:    "svc.op",
		Input:   map[string]any{"id": "1"},
		Extract: map[string]string{"id": `"R1"`},
		Assert:  []domain.Assertion{{Expr: "body.items.exists(t, t.id == out.id)"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps[0])
	assert.True(t, run.Steps[0].Assertions[0].Passed, run.Steps[0].Assertions[0].Error)
}

// TestRun_FailedAssertionAndFailedExtract_AssertionIsPrimary covers the
// outcome rule when both a hard assertion and an extract fail on the same
// step: the assertion failure is what decides the step's status (Failed,
// no StepResult.Error), and the extract error is only secondary detail, on
// Warnings.
func TestRun_FailedAssertionAndFailedExtract_AssertionIsPrimary(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"orderId": "ORD1"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "assert-and-extract-fail", Steps: []domain.Step{{
		ID:      "call",
		Call:    "svc.op",
		Input:   map[string]any{"id": "1"},
		Extract: map[string]string{"missing": "body.doesNotExist"},
		Assert:  []domain.Assertion{{Expr: "status == 999"}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunFailed, run.Status)
	step := run.Steps[0]
	assert.Equal(t, domain.StepFailed, step.Status, "the assertion failure decides the step's status, not the extract error")
	require.Len(t, step.Assertions, 1)
	assert.False(t, step.Assertions[0].Passed)
	assert.Nil(t, step.Error, "the step itself must not carry an error; the assertion failure is primary")
	require.Len(t, step.Warnings, 1)
	assert.Contains(t, step.Warnings[0], "missing")
}

// TestRun_ExtractTolerant_OneFailsOneSucceeds confirms one extract entry's
// failure doesn't stop a different entry in the same step from succeeding
// (the step still errors overall, same as a single failing extract always
// has, but the successful entry's value is not lost).
func TestRun_ExtractTolerant_OneFailsOneSucceeds(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"orderId": "ORD1"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "extract-tolerant", Steps: []domain.Step{{
		ID:    "call",
		Call:  "svc.op",
		Input: map[string]any{"id": "1"},
		Extract: map[string]string{
			"good":    "body.orderId",
			"missing": "body.doesNotExist",
		},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	step := run.Steps[0]
	assert.Equal(t, domain.StepErrored, step.Status)
	require.NotNil(t, step.Error)
	assert.Equal(t, "missing", step.Error.Details["extract"])
	assert.Equal(t, "ORD1", step.Out["good"], "a failing extract must not stop a different one from succeeding")
}

// TestRun_OutReferenceToFailedExtract_ExplainsWhy confirms a reference to
// out.<name> naming a failed extract gets a specific "was not extracted"
// message instead of a bare "no such key".
func TestRun_OutReferenceToFailedExtract_ExplainsWhy(t *testing.T) {
	cs := newCaptureServer()
	defer cs.Close()
	cs.setResponse(http.StatusOK, map[string]any{"orderId": "ORD1"})

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", cs.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "out-ref-failed-extract", Steps: []domain.Step{{
		ID:      "call",
		Call:    "svc.op",
		Input:   map[string]any{"id": "1"},
		Extract: map[string]string{"tid": "body.doesNotExist"},
		Assert:  []domain.Assertion{{Expr: `out.tid == "ORD1"`}},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	step := run.Steps[0]
	require.Len(t, step.Assertions, 1)
	a := step.Assertions[0]
	assert.False(t, a.Passed)
	assert.Contains(t, a.Error, "out.tid was not extracted")
	assert.Contains(t, a.Error, "no such key")
}

// TestRun_UntilWithOut confirms `until` sees `out` populated by this same
// step's own `extract:`, evaluated tolerantly on every poll attempt (not
// just once after polling ends).
func TestRun_UntilWithOut(t *testing.T) {
	var mu sync.Mutex
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempt++
		n := attempt
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": n >= 3})
	}))
	defer srv.Close()

	op := syntheticOp("svc", anySchemaResponses())
	ops := mapOperations{ops: map[string]*domain.Operation{op.ID: op}}
	e := singleServiceEnv("svc", srv.URL)
	r := New(ops)

	f := &domain.Flow{Version: 1, ID: "until-out", Steps: []domain.Step{{
		ID:      "call",
		Call:    "svc.op",
		Input:   map[string]any{"id": "1"},
		Extract: map[string]string{"ready": "body.ready"},
		Until:   "out.ready == true",
		Poll:    &domain.Poll{Interval: "1ms", Timeout: "10s"},
	}}}

	run, err := r.Run(context.Background(), f, nil, Options{Env: e, Sleep: instantSleep})
	require.NoError(t, err)
	assert.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps[0])
	assert.GreaterOrEqual(t, run.Steps[0].Attempts, 3)
	assert.Equal(t, true, run.Steps[0].Out["ready"])
}
