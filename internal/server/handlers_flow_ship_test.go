package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
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
