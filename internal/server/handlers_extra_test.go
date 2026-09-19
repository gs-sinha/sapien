package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func doReqBodyReal(t *testing.T, ts *httptest.Server, method, path, token string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestMalformedBodyIsInvalid checks handleX's decodeJSON error branch for
// every route that accepts a JSON body: a broken body must produce a 400
// with errs.Invalid, never reach the engine, and never panic.
func TestMalformedBodyIsInvalid(t *testing.T) {
	_, ts := newTestServer(t, nil)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/services"},
		{http.MethodPost, "/v1/flows"},
		{http.MethodPut, "/v1/flows/create-order-flow"},
		{http.MethodPost, "/v1/flows/validate"},
		{http.MethodPost, "/v1/flows/parse"},
		{http.MethodPost, "/v1/flows/create-order-flow/run"},
		{http.MethodPost, "/v1/call"},
		{http.MethodPost, "/v1/runs/source"},
		{http.MethodPost, "/v1/runs/run_x/pin"},
		{http.MethodPost, "/v1/runs/purge"},
		{http.MethodPost, "/v1/memories"},
		{http.MethodPatch, "/v1/memories/mem_x"},
		{http.MethodPost, "/v1/memories/relevant"},
		{http.MethodPost, "/v1/examples"},
		{http.MethodPut, "/v1/examples/ex_x"},
		{http.MethodPost, "/v1/examples/from-run"},
		{http.MethodPost, "/v1/context"},
		{http.MethodPut, "/v1/environments/default"},
		{http.MethodPut, "/v1/secrets/API_KEY"},
		{http.MethodPut, "/v1/settings/semantic"},
		{http.MethodPost, "/v1/settings/semantic/test"},
		{http.MethodPost, "/v1/settings/semantic/ollama/pull"},
	}

	for _, rt := range routes {
		t.Run(rt.method+"_"+rt.path, func(t *testing.T) {
			// unterminated JSON object: always a decode failure regardless
			// of the target struct's shape.
			resp := doReqBodyReal(t, ts, rt.method, rt.path, "test-token", []byte(`{"unterminated`))
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%s %s", rt.method, rt.path)
			var e errs.Error
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
			assert.Equal(t, errs.Invalid, e.Code)
		})
	}
}

func TestOperationResolveMissingRefIsInvalid(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/operations/resolve", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestMemoryUpdateIDMismatchIsInvalid(t *testing.T) {
	_, ts := newTestServer(t, nil)
	body, err := json.Marshal(domain.Memory{ID: "some-other-id", Text: "x"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPatch, "/v1/memories/mem_target", "test-token", body)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestServiceAddConflict(t *testing.T) {
	_, ts := newTestServer(t, nil)
	body, err := json.Marshal(addServiceRequest{Name: "order-service", Source: domain.Source{Kind: domain.SourceLocal, Path: "x"}})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/services", "test-token", body)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, errs.Conflict, decodeErrBody(t, resp).Code)
}

func TestFlowCreateConflict(t *testing.T) {
	_, ts := newTestServer(t, nil)
	body, err := json.Marshal(flowYAMLRequest{YAML: "version: 1\nid: create-order-flow\nsteps: []\n", Path: "flows/dup.flow.yaml"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows", "test-token", body)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, errs.Conflict, decodeErrBody(t, resp).Code)
}

func TestFlowRunUnknownFlowIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/does-not-exist/run", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.FlowNotFound, decodeErrBody(t, resp).Code)
}

func TestRunPinNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/runs/run_does_not_exist/pin", "test-token", []byte(`{"pinned":true}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.RunNotFound, decodeErrBody(t, resp).Code)
}

func TestCallUnknownOperationIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/call", "test-token", []byte(`{"operation":"nope.doesNotExist"}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.OperationNotFound, decodeErrBody(t, resp).Code)
}

func TestEnvironmentDefaultSetUnknownEnvIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/environments/default", "test-token", []byte(`{"name":"no-such-env"}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.EnvNotFound, decodeErrBody(t, resp).Code)
}

func TestRunStepGet(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	runID := runs[0].ID

	full, err := fake.Runs().Get(context.Background(), runID)
	require.NoError(t, err)
	require.NotEmpty(t, full.Steps)
	stepID := full.Steps[0].StepID

	t.Run("ByStepID", func(t *testing.T) {
		resp := doReq(t, ts, http.MethodGet, "/v1/runs/"+runID+"/steps/"+stepID, reqOpts{token: "test-token"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var step domain.StepResult
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&step))
		assert.Equal(t, stepID, step.StepID)
	})

	t.Run("ByIndex", func(t *testing.T) {
		resp := doReq(t, ts, http.MethodGet, "/v1/runs/"+runID+"/steps/0", reqOpts{token: "test-token"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("StepNotFound", func(t *testing.T) {
		resp := doReq(t, ts, http.MethodGet, "/v1/runs/"+runID+"/steps/no-such-step", reqOpts{token: "test-token"})
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, errs.RunNotFound, decodeErrBody(t, resp).Code)
	})

	t.Run("RunNotFound", func(t *testing.T) {
		resp := doReq(t, ts, http.MethodGet, "/v1/runs/no-such-run/steps/0", reqOpts{token: "test-token"})
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, errs.RunNotFound, decodeErrBody(t, resp).Code)
	})
}

// TestOperationExample covers GET /v1/operations/{id}/example: the endpoint
// the UI's "Try it" form prefills from, so a human is handed a payload instead
// of a schema panel and a blank textarea.
func TestOperationExample(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/operations/order-service.createOrder/example", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ex domain.RequestExample
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ex))
	assert.Equal(t, "order-service.createOrder", ex.Operation)
	assert.Equal(t, domain.RequestExampleSynthesized, ex.Source)
	assert.Equal(t, map[string]any{"customerId": "<customerId>"}, ex.Body)

	// ?fields=all is the "whole shape" button: synthesized from the schema,
	// optional fields included.
	resp = doReq(t, ts, http.MethodGet, "/v1/operations/order-service.getOrder/example?fields=all", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var noBody domain.RequestExample
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&noBody))
	assert.Equal(t, map[string]any{"orderId": "<orderId>"}, noBody.Input)
	assert.Nil(t, noBody.Body, "an operation with no request body has no body to show")

	resp = doReq(t, ts, http.MethodGet, "/v1/operations/order-service.nope/example", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
