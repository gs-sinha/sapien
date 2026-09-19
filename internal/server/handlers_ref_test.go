package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// seedGitService registers a git-sourced service on fake, for tests that
// need one -- the default seed's two services are both local (PLAN §34f
// item 2's ref endpoints only apply to a git source).
func seedGitService(t *testing.T, fake interface {
	Services() engine.ServiceAPI
}, name string) {
	t.Helper()
	_, err := fake.Services().Add(context.Background(), name,
		domain.Source{Kind: domain.SourceGit, URL: "git@github.com:acme/" + name + ".git", Ref: "main"})
	require.NoError(t, err)
}

// --- PUT /v1/services/{id}/ref -------------------------------------------

func TestServiceSetRef_RoundTrip(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	seedGitService(t, fake, "billing-service")

	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/billing-service/ref", "test-token",
		[]byte(`{"ref":"release-2"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var svc domain.Service
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&svc))
	assert.Equal(t, "release-2", svc.Source.Ref)

	call := recordedCall(t, fake, "Services.SetRef")
	args, ok := call.Args.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "billing-service", args["name"])
	assert.Equal(t, "release-2", args["ref"])
}

func TestServiceSetRef_TeamScope(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	seedGitService(t, fake, "billing-service")

	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/billing-service/ref", "test-token",
		[]byte(`{"ref":"release-2","scope":"team"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	call := recordedCall(t, fake, "Services.SetRef")
	args, ok := call.Args.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "team", args["scope"])
}

func TestServiceSetRef_EmptyRefIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	seedGitService(t, fake, "billing-service")

	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/billing-service/ref", "test-token",
		[]byte(`{"ref":""}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestServiceSetRef_NonGitServiceIsInvalid(t *testing.T) {
	_, ts := newTestServer(t, nil)
	// order-service, from the default seed, is a local source.
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/order-service/ref", "test-token",
		[]byte(`{"ref":"release-2"}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestServiceSetRef_UnknownServiceIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/services/no-such-service/ref", "test-token",
		[]byte(`{"ref":"release-2"}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ServiceNotFound, decodeErrBody(t, resp).Code)
}

// --- DELETE /v1/services/{id}/ref -----------------------------------------

func TestServiceClearRef_RoundTrip(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	seedGitService(t, fake, "billing-service")
	_, err := fake.Services().SetRef(context.Background(), "billing-service", "release-2", "")
	require.NoError(t, err)

	resp := doReq(t, ts, http.MethodDelete, "/v1/services/billing-service/ref", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, hasRecordedCall(fake, "Services.ClearRef"))
}

func TestServiceClearRef_NoneToClearIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	seedGitService(t, fake, "billing-service")

	resp := doReq(t, ts, http.MethodDelete, "/v1/services/billing-service/ref", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

// --- GET /v1/services/{id}/branches ---------------------------------------

func TestServiceBranches_RoundTrip(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	seedGitService(t, fake, "billing-service")

	resp := doReq(t, ts, http.MethodGet, "/v1/services/billing-service/branches", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out engine.BranchList
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, "main", out.Default)
	assert.Contains(t, out.Branches, "main")
	assert.True(t, hasRecordedCall(fake, "Services.Branches"))
}

func TestServiceBranches_UnknownServiceIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/services/no-such-service/branches", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ServiceNotFound, decodeErrBody(t, resp).Code)
}
