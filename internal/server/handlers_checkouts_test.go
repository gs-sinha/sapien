package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// bindCapture records the arguments a BindWith call actually reached the
// engine with, captured at the engine.Engine boundary rather than parsed
// back out of a *enginetest.Fake's generic call log -- so these tests hold
// regardless of exactly what (if anything) the shared fake's own BindWith
// records about itself.
type bindCapture struct {
	called     bool
	name, path string
	force      bool
}

// bindCapturingEngine wraps the engine.Engine newTestServer built (a
// *enginetest.Fake) so handleServiceBind's and
// handleServiceBindByCheckout's calls to Services().BindWith can be
// inspected directly, then delegates to the real fake as normal.
type bindCapturingEngine struct {
	engine.Engine
	capture *bindCapture
}

func (e *bindCapturingEngine) Services() engine.ServiceAPI {
	return bindCapturingServices{ServiceAPI: e.Engine.Services(), capture: e.capture}
}

type bindCapturingServices struct {
	engine.ServiceAPI
	capture *bindCapture
}

func (s bindCapturingServices) BindWith(ctx context.Context, name, path string, opts engine.BindOptions) (*domain.Service, error) {
	s.capture.called = true
	s.capture.name = name
	s.capture.path = path
	s.capture.force = opts.Force
	return s.ServiceAPI.BindWith(ctx, name, path, opts)
}

// withBindCapture builds a test server whose engine records every
// BindWith call into the returned *bindCapture.
func withBindCapture(t *testing.T) (*bindCapture, *httptest.Server) {
	t.Helper()
	cap := &bindCapture{}
	_, ts := newTestServer(t, func(o *Options) {
		o.Engine = &bindCapturingEngine{Engine: o.Engine, capture: cap}
	})
	return cap, ts
}

// --- PUT /v1/services/{id}/binding: force ---------------------------------

// TestServiceBindForcePassesThrough proves handleServiceBind forwards the
// body's force flag to BindWith (rather than the plain Bind it used
// before, which has no such option): the fake has no filesystem to
// validate a checkout's origin against, so force changes nothing
// observable in the response itself, and only a capture at the engine
// boundary can prove the handler forwarded it.
func TestServiceBindForcePassesThrough(t *testing.T) {
	cap, ts := withBindCapture(t)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/order-service/binding", "test-token",
		[]byte(`{"path":"/home/dev/code/order-service","force":true}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var svc domain.Service
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&svc))
	assert.Equal(t, domain.BindingLocal, svc.Binding.Mode)

	require.True(t, cap.called)
	assert.Equal(t, "order-service", cap.name)
	assert.Equal(t, "/home/dev/code/order-service", cap.path)
	assert.True(t, cap.force, "the handler must forward the request body's force flag to BindWith")
}

// --- PUT /v1/services/binding: inferred bind ------------------------------

// TestServiceBindByCheckoutInfersName proves PUT /v1/services/binding (no
// {id} segment) calls BindWith with an empty name so the engine infers the
// service from the checkout's origin, and that the route is never confused
// with PUT /v1/services/{id}/binding: "binding" is not a registered
// service, so a misroute would 404 instead of returning order-service.
func TestServiceBindByCheckoutInfersName(t *testing.T) {
	cap, ts := withBindCapture(t)
	// Give the fake a service already bound at this path, so BindWith("",
	// path, ...) has a candidate to infer from.
	pre := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/order-service/binding", "test-token",
		[]byte(`{"path":"/home/dev/code/order-service"}`))
	require.Equal(t, http.StatusOK, pre.StatusCode)

	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/binding", "test-token",
		[]byte(`{"path":"/home/dev/code/order-service"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var svc domain.Service
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&svc))
	assert.Equal(t, "order-service", svc.Name, "PUT /v1/services/binding must resolve the service by checkout, not treat \"binding\" as {id}")

	require.True(t, cap.called)
	assert.Empty(t, cap.name, "the handler must call BindWith with an empty name so the engine infers the service")
	assert.Equal(t, "/home/dev/code/order-service", cap.path)
}

func TestServiceBindByCheckoutEmptyPathIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/binding", "test-token", []byte(`{"path":"  "}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Services.BindWith"))
	assert.False(t, hasRecordedCall(fake, "Services.Bind"))
}

func TestServiceBindByCheckoutNoMatchIsInvalid(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/binding", "test-token", []byte(`{"path":"/nowhere"}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

// --- GET /v1/services/{id}/checkouts --------------------------------------

func TestServiceBrowseCheckouts(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/services/order-service/checkouts?path=%2Ftmp%2Fcode", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var listing engine.DirListing
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listing))
	assert.Equal(t, "/tmp/code", listing.Path)
}

func TestServiceBrowseCheckoutsNoPathMeansHome(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/services/order-service/checkouts", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var listing engine.DirListing
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listing))
	assert.NotEmpty(t, listing.Path)
}

func TestServiceBrowseCheckoutsUnknownServiceIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/services/no-such-service/checkouts", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ServiceNotFound, decodeErrBody(t, resp).Code)
}

// --- POST /v1/services/from-checkout --------------------------------------

func TestServiceAddFromCheckout(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/services/from-checkout", "test-token",
		[]byte(`{"name":"new-repo","path":"/home/dev/code/new-repo","ref":"main"}`))
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var svc domain.Service
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&svc))
	assert.Equal(t, "new-repo", svc.Name)
	require.NotNil(t, svc.Binding)
	assert.Equal(t, domain.BindingLocal, svc.Binding.Mode)
	require.NotNil(t, svc.Binding.Local)
	assert.Equal(t, "/home/dev/code/new-repo", svc.Binding.Local.Path)

	call := recordedCall(t, fake, "Services.AddFromCheckout")
	args, ok := call.Args.(map[string]any)
	require.True(t, ok, "AddFromCheckout args: %#v", call.Args)
	assert.Equal(t, "new-repo", args["name"])
	assert.Equal(t, "/home/dev/code/new-repo", args["path"])
	assert.Equal(t, "main", args["ref"])
}

func TestServiceAddFromCheckoutEmptyPathIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/services/from-checkout", "test-token", []byte(`{"name":"new-repo","path":"  "}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Services.AddFromCheckout"), "an empty path must never reach the engine")
}

func TestServiceAddFromCheckoutMalformedBodyIsInvalid(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/services/from-checkout", "test-token", []byte(`{"unterminated`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}
