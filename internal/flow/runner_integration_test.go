package flow_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/flow"
	"github.com/gs-sinha/sapien/internal/runner"
)

// runnerOpsAdapter is a minimal runner.Operations backed by a single
// *domain.Operation, enough to drive one step through the real runner.
type runnerOpsAdapter struct{ op *domain.Operation }

func (a runnerOpsAdapter) Operation(_ context.Context, id string) (*domain.Operation, error) {
	if id != a.op.ID {
		return nil, errs.New(errs.OperationNotFound, "operation %q not found", id)
	}
	return a.op, nil
}

// oneExampleResolver is a one-entry flow.ExampleResolver, enough to drive
// this test's single `example:` step. This file lives in the flow_test
// package (rather than flow, where its sibling tests live) specifically so
// it can import internal/runner: internal/runner also imports internal/flow
// (to compare a reused step's definition across runs, PLAN §9), so a
// same-package test importing runner would be an import cycle.
type oneExampleResolver struct{ ex domain.SavedExample }

func (r oneExampleResolver) Example(_ context.Context, id string) (*domain.SavedExample, bool) {
	if id != r.ex.ID {
		return nil, false
	}
	return &r.ex, true
}

func (r oneExampleResolver) SuggestExamples(_ context.Context, _ string, _ int) []string { return nil }

// TestMaterialize_RunsThroughRealRunner is the "runner needs to see
// materialised steps" check called out in the task: it proves internal/flow
// materializing an `example:` step once, before anything else sees the
// flow, is enough -- internal/runner executes the merged
// call/input/body/headers exactly like an ordinary step, with no changes of
// its own for that part. It also exercises the one behavior Materialize's
// static tests can't: that a `${...}` template copied in from the example
// actually interpolates against the flow's own inputs at run time, and that
// headers/body precedence (example base, step override) survives all the
// way to the wire.
func TestMaterialize_RunsThroughRealRunner(t *testing.T) {
	var gotBody map[string]any
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Trace-Source")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"orderId":"ord_1"}`))
	}))
	defer srv.Close()

	op := &domain.Operation{
		ID: "order-service.createOrder", ServiceID: "order-service", Protocol: domain.ProtocolHTTP,
		HTTP:        &domain.HTTPBinding{Method: "POST", Path: "/v1/orders"},
		RequestBody: &domain.Body{ContentType: "application/json", Required: true},
		Responses:   []domain.Response{{Status: "201"}},
	}

	resolver := oneExampleResolver{ex: domain.SavedExample{
		ID:        "create-qcom-order",
		Operation: "order-service.createOrder",
		Body: map[string]any{
			"customerId": "${inputs.customerId}", // preserved, interpolated at run time
			"type":       "QCOM",
		},
		Headers: map[string]string{"X-Trace-Source": "example"},
	}}

	f := &domain.Flow{
		Version: 1,
		Inputs:  map[string]domain.InputSpec{"customerId": {Type: "string", Required: true}},
		Steps: []domain.Step{{
			ID: "create", Example: "create-qcom-order",
			Assert: []domain.Assertion{{Expr: "status == 201"}},
		}},
	}
	materialized, diags := flow.Materialize(context.Background(), f, resolver)
	require.Empty(t, diags)

	envDoc := domain.Environment{
		Version: 1, Name: "test",
		Services: map[string]domain.ServiceEnv{"order-service": {BaseURL: srv.URL}},
	}
	resolvedEnv := env.Resolve(envDoc, []domain.Service{{Name: "order-service"}}, nil)

	r := runner.New(runnerOpsAdapter{op: op})
	run, err := r.Run(context.Background(), materialized, map[string]any{"customerId": "c42"}, runner.Options{Env: resolvedEnv})
	require.NoError(t, err)
	require.Len(t, run.Steps, 1)
	assert.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps[0])
	assert.Equal(t, domain.StepPassed, run.Steps[0].Status)

	assert.Equal(t, "example", gotHeader, "the example's header reached the wire")
	assert.Equal(t, map[string]any{"customerId": "c42", "type": "QCOM"}, gotBody,
		"the example's templated body interpolated the flow's own input at run time")
}
