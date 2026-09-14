package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/errs"
)

// recordedCall returns the last call the fake recorded for method, so a
// test can prove the handler forwarded what the client sent rather than a
// default of its own.
func recordedCall(t *testing.T, fake *enginetest.Fake, method string) enginetest.Call {
	t.Helper()
	for i := len(fake.Calls) - 1; i >= 0; i-- {
		if fake.Calls[i].Method == method {
			return fake.Calls[i]
		}
	}
	t.Fatalf("fake recorded no %s call", method)
	return enginetest.Call{}
}

func hasRecordedCall(fake *enginetest.Fake, method string) bool {
	for _, c := range fake.Calls {
		if c.Method == method {
			return true
		}
	}
	return false
}

// --- GET/PUT/DELETE /v1/services/{id}/binding ---------------------------

func TestServiceBindingGet(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/services/order-service/binding", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var info engine.BindingInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&info))
	assert.Equal(t, "order-service", info.Service)
	// The seed registers order-service from a local path, so with no
	// override it is already read locally and writable.
	assert.Equal(t, domain.BindingLocal, info.Binding.Mode)
	assert.True(t, info.Binding.Writable)
}

func TestServiceBindingGetUnknownIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/services/no-such-service/binding", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ServiceNotFound, decodeErrBody(t, resp).Code)
}

func TestServiceBind(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/order-service/binding", "test-token",
		[]byte(`{"path":"/home/dev/code/order-service"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var svc domain.Service
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&svc))
	require.NotNil(t, svc.Binding)
	assert.Equal(t, domain.BindingLocal, svc.Binding.Mode)
	require.NotNil(t, svc.Binding.Local)
	assert.Equal(t, "/home/dev/code/order-service", svc.Binding.Local.Path)
	assert.True(t, svc.Binding.Writable)

	call := recordedCall(t, fake, "Services.Bind")
	args, ok := call.Args.(map[string]any)
	require.True(t, ok, "Bind args: %#v", call.Args)
	assert.Equal(t, "order-service", args["name"])
	assert.Equal(t, "/home/dev/code/order-service", args["path"])
}

func TestServiceBindEmptyPathIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/order-service/binding", "test-token", []byte(`{"path":"  "}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Services.Bind"), "an empty path must never reach the engine")
}

func TestServiceBindMalformedBodyIsInvalid(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/order-service/binding", "test-token", []byte(`{"unterminated`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestServiceBindUnknownIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/no-such-service/binding", "test-token", []byte(`{"path":"/x"}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ServiceNotFound, decodeErrBody(t, resp).Code)
}

func TestServiceUnbindRestoresCommittedSource(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	bind := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/order-service/binding", "test-token", []byte(`{"path":"/home/dev/code/order-service"}`))
	require.Equal(t, http.StatusOK, bind.StatusCode)

	resp := doReq(t, ts, http.MethodDelete, "/v1/services/order-service/binding", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var svc domain.Service
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&svc))
	assert.Equal(t, "services/order-service", svc.Source.Path, "the committed source is read again")
	require.NotNil(t, svc.Binding)
	assert.Equal(t, domain.BindingLocal, svc.Binding.Mode)
	assert.Equal(t, "services/order-service", svc.Binding.Local.Path)

	call := recordedCall(t, fake, "Services.Unbind")
	assert.Equal(t, "order-service", call.Args)
}

func TestServiceUnbindUnknownIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodDelete, "/v1/services/no-such-service/binding", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ServiceNotFound, decodeErrBody(t, resp).Code)
}

// --- POST /v1/flows with owner_kind ---------------------------------------

const tierFlowYAML = "version: 1\nid: tier-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n"

func TestFlowCreateOwnerKindLocalStampsOwner(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	body, err := json.Marshal(createFlowRequest{YAML: tierFlowYAML, OwnerKind: domain.FlowOwnerLocal})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows", "test-token", body)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var flow domain.Flow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&flow))
	assert.Equal(t, "tier-flow", flow.ID)
	assert.Equal(t, domain.FlowOwnerLocal, flow.OwnerKind)
	assert.Empty(t, flow.OwnerID)

	call := recordedCall(t, fake, "Flows.CreateIn")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "CreateIn args: %#v", call.Args)
	assert.Equal(t, domain.FlowOwnerLocal, args["owner_kind"])
}

func TestFlowCreateOwnerKindServiceStampsService(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	body, err := json.Marshal(createFlowRequest{YAML: tierFlowYAML, Path: "tier-flow.flow.yaml", OwnerKind: domain.FlowOwnerService, OwnerID: "order-service"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows", "test-token", body)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var flow domain.Flow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&flow))
	assert.Equal(t, domain.FlowOwnerService, flow.OwnerKind)
	assert.Equal(t, "order-service", flow.OwnerID)

	args := recordedCall(t, fake, "Flows.CreateIn").Args.(map[string]string)
	assert.Equal(t, "order-service", args["owner_id"])
}

// TestFlowCreateWithoutOwnerKindKeepsWorkspaceTier pins the compatibility
// rule in handleFlowCreate: a body with no owner_kind is an older client,
// and its flow still lands in the workspace tier via Create, never in
// CreateIn's local default.
func TestFlowCreateWithoutOwnerKindKeepsWorkspaceTier(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	body, err := json.Marshal(flowYAMLRequest{YAML: tierFlowYAML})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows", "test-token", body)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var flow domain.Flow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&flow))
	assert.Equal(t, domain.FlowOwnerWorkspace, flow.OwnerKind)
	assert.True(t, hasRecordedCall(fake, "Flows.Create"))
	assert.False(t, hasRecordedCall(fake, "Flows.CreateIn"))
}

// --- POST /v1/flows/{id}/rescope -----------------------------------------

func TestFlowRescopeChangesOwner(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/rescope", "test-token",
		[]byte(`{"owner_kind":"local"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var flow domain.Flow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&flow))
	assert.Equal(t, "create-order-flow", flow.ID)
	assert.Equal(t, domain.FlowOwnerLocal, flow.OwnerKind)

	args := recordedCall(t, fake, "Flows.Rescope").Args.(map[string]string)
	assert.Equal(t, "create-order-flow", args["id"])
	assert.Equal(t, domain.FlowOwnerLocal, args["owner_kind"])

	// And on to a service, which needs owner_id.
	resp = doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/rescope", "test-token",
		[]byte(`{"owner_kind":"service","owner_id":"order-service"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&flow))
	assert.Equal(t, domain.FlowOwnerService, flow.OwnerKind)
	assert.Equal(t, "order-service", flow.OwnerID)
}

func TestFlowRescopeUnknownFlowIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/no-such-flow/rescope", "test-token", []byte(`{"owner_kind":"workspace"}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.FlowNotFound, decodeErrBody(t, resp).Code)
}

func TestFlowRescopeBadBodyIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/rescope", "test-token", []byte(`{"unterminated`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)

	// A well-formed body that names no tier is just as useless to the engine.
	resp = doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/rescope", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Flows.Rescope"))
}
