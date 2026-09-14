package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// TestFlowRescopeForwardsCommitAndMessage proves the wire shape PLAN §7b
// added to POST /v1/flows/{id}/rescope (commit, message) reaches the
// engine as engine.RescopeOptions, through Flows().RescopeWith rather than
// the plain Rescope every other rescope test here still exercises (the
// fake's RescopeWith also records a Flows.Rescope call, so those keep
// passing unchanged).
func TestFlowRescopeForwardsCommitAndMessage(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/rescope", "test-token",
		[]byte(`{"owner_kind":"workspace","commit":true,"message":"Ship it"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	call := recordedCall(t, fake, "Flows.RescopeWith")
	args, ok := call.Args.(map[string]any)
	require.True(t, ok, "RescopeWith args: %#v", call.Args)
	assert.Equal(t, "create-order-flow", args["id"])
	assert.Equal(t, domain.FlowOwnerWorkspace, args["owner_kind"])
	assert.Equal(t, true, args["commit"])
	assert.Equal(t, "Ship it", args["message"])
}

// TestFlowRescopeWithoutCommitDefaultsFalse: an ordinary rescope body (no
// commit/message) still reaches RescopeWith, but with Commit false and an
// empty Message -- the wire's default, not the engine's own default
// message, since that is filled in by the local engine, not the client.
func TestFlowRescopeWithoutCommitDefaultsFalse(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/rescope", "test-token",
		[]byte(`{"owner_kind":"local"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	call := recordedCall(t, fake, "Flows.RescopeWith")
	args := call.Args.(map[string]any)
	assert.Equal(t, false, args["commit"])
	assert.Equal(t, "", args["message"])
}

// --- POST /v1/flows/{id}/commit ---------------------------------------

// TestFlowCommitForwardsMessage proves the standalone commit route reaches
// Flows().Commit with the id and message, and that the response is the
// FlowSummary the engine returned, its Shipped now unpushed (the fake's
// Commit models exactly that state change).
func TestFlowCommitForwardsMessage(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/commit", "test-token",
		[]byte(`{"message":"Ship it"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var sum domain.FlowSummary
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sum))
	assert.Equal(t, "create-order-flow", sum.ID)
	assert.Equal(t, domain.ShipUnpushed, sum.Shipped)

	call := recordedCall(t, fake, "Flows.Commit")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "Commit args: %#v", call.Args)
	assert.Equal(t, "create-order-flow", args["id"])
	assert.Equal(t, "Ship it", args["message"])
}

// TestFlowCommitWithoutMessage: an empty body still reaches the engine,
// with an empty message so the engine's own default picks the wording.
func TestFlowCommitWithoutMessage(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/commit", "test-token", []byte(`{}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	call := recordedCall(t, fake, "Flows.Commit")
	args := call.Args.(map[string]string)
	assert.Equal(t, "", args["message"])
}

// TestFlowCommitBadBodyIsInvalid: a malformed JSON body never reaches the
// engine.
func TestFlowCommitBadBodyIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/create-order-flow/commit", "test-token", []byte(`{"unterminated`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Flows.Commit"))
}

// TestFlowCommitUnknownFlowIsNotFound.
func TestFlowCommitUnknownFlowIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/no-such-flow/commit", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.FlowNotFound, decodeErrBody(t, resp).Code)
}
