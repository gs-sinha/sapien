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

// These tests drive the memory/example tier routes PLAN §7b added -- POST
// /v1/memories/{id}/move, /commit, the same pair under /v1/examples, and
// POST /v1/workspace/repo/push -- against the enginetest fake, seeding
// through its Create/SetRepoStatus the way handlers_flow_ship_test.go and
// handlers_repo_test.go already do for flows and the repo status routes.

// --- POST /v1/memories/{id}/move ----------------------------------------

func TestMemoryMove_ForwardsIDAndTier(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	mem, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "move me",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/"+mem.ID+"/move", "test-token",
		[]byte(`{"tier":"workspace"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.Memory
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, mem.ID, got.ID)
	assert.Equal(t, domain.TierWorkspace, got.Tier)
	assert.Equal(t, domain.ShipUntracked, got.Shipped)

	call := recordedCall(t, fake, "Memories.Move")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "Move args: %#v", call.Args)
	assert.Equal(t, mem.ID, args["id"])
	assert.Equal(t, "workspace", args["tier"])
}

func TestMemoryMove_BlankTierIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	mem, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "no tier given",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/"+mem.ID+"/move", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Memories.Move"))
}

func TestMemoryMove_UnknownMemoryIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/mem_nope/move", "test-token", []byte(`{"tier":"workspace"}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.MemoryNotFound, decodeErrBody(t, resp).Code)
}

// --- POST /v1/memories/{id}/commit ---------------------------------------

func TestMemoryCommit_ForwardsIDAndMessage(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	mem, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "commit me",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/"+mem.ID+"/commit", "test-token",
		[]byte(`{"message":"Ship it"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.Memory
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, mem.ID, got.ID)
	assert.Equal(t, domain.ShipUnpushed, got.Shipped)

	call := recordedCall(t, fake, "Memories.Commit")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "Commit args: %#v", call.Args)
	assert.Equal(t, mem.ID, args["id"])
	assert.Equal(t, "Ship it", args["message"])
}

func TestMemoryCommit_WithoutMessage(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	mem, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "commit me too",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/"+mem.ID+"/commit", "test-token", []byte(`{}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	call := recordedCall(t, fake, "Memories.Commit")
	args := call.Args.(map[string]string)
	assert.Equal(t, "", args["message"])
}

func TestMemoryCommit_UnknownMemoryIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/mem_nope/commit", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.MemoryNotFound, decodeErrBody(t, resp).Code)
}

func TestMemoryCommit_BadBodyIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	mem, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "bad body",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/"+mem.ID+"/commit", "test-token", []byte(`{"unterminated`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Memories.Commit"))
}

// --- POST /v1/examples/{id}/move ------------------------------------------

func TestExampleMove_ForwardsIDAndTier(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	ex, err := fake.Examples().Create(t.Context(), domain.SavedExample{
		ID: "move-example", Operation: "order-service.createOrder", Scope: domain.ExampleScopeWorkspace,
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/examples/"+ex.ID+"/move", "test-token",
		[]byte(`{"tier":"local"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.SavedExample
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, ex.ID, got.ID)
	assert.Equal(t, domain.TierLocal, got.Tier)
	assert.Equal(t, "", got.Shipped)

	call := recordedCall(t, fake, "Examples.Move")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "Move args: %#v", call.Args)
	assert.Equal(t, ex.ID, args["id"])
	assert.Equal(t, "local", args["tier"])
}

func TestExampleMove_BlankTierIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	ex, err := fake.Examples().Create(t.Context(), domain.SavedExample{
		ID: "move-blank", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/examples/"+ex.ID+"/move", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Examples.Move"))
}

func TestExampleMove_UnknownExampleIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/examples/no-such-example/move", "test-token", []byte(`{"tier":"workspace"}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ExampleNotFound, decodeErrBody(t, resp).Code)
}

// --- POST /v1/examples/{id}/commit -----------------------------------------

func TestExampleCommit_ForwardsIDAndMessage(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	ex, err := fake.Examples().Create(t.Context(), domain.SavedExample{
		ID: "commit-example", Operation: "order-service.createOrder", Scope: domain.ExampleScopeWorkspace,
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/examples/"+ex.ID+"/commit", "test-token",
		[]byte(`{"message":"Ship the example"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.SavedExample
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, ex.ID, got.ID)
	assert.Equal(t, domain.ShipUnpushed, got.Shipped)

	call := recordedCall(t, fake, "Examples.Commit")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "Commit args: %#v", call.Args)
	assert.Equal(t, ex.ID, args["id"])
	assert.Equal(t, "Ship the example", args["message"])
}

func TestExampleCommit_UnknownExampleIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/examples/no-such-example/commit", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ExampleNotFound, decodeErrBody(t, resp).Code)
}

// --- POST /v1/workspace/repo/push -----------------------------------------

// TestWorkspaceRepoPush_BehindIsConflict: the fake's Push refuses a branch
// that is behind with errs.Conflict, the same way Pull does, and that
// reaches the client as a plain 409.
func TestWorkspaceRepoPush_BehindIsConflict(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 2})

	resp := doReq(t, ts, http.MethodPost, "/v1/workspace/repo/push", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, errs.Conflict, decodeErrBody(t, resp).Code)
	assert.True(t, hasRecordedCall(fake, "Repo.Push"))
}

// TestWorkspaceRepoPush_AheadPushes: an ahead, non-behind branch pushes,
// and the response carries Pushed/PushedCount.
func TestWorkspaceRepoPush_AheadPushes(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Ahead: 3})

	resp := doReq(t, ts, http.MethodPost, "/v1/workspace/repo/push", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var st domain.RepoStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.True(t, st.Pushed)
	assert.Equal(t, 3, st.PushedCount)
	assert.Equal(t, 0, st.Ahead)
	assert.True(t, hasRecordedCall(fake, "Repo.Push"))
}

// TestWorkspaceRepoPush_NothingToPush: neither ahead nor behind is a
// no-op success, not an error.
func TestWorkspaceRepoPush_NothingToPush(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main"})

	resp := doReq(t, ts, http.MethodPost, "/v1/workspace/repo/push", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var st domain.RepoStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.False(t, st.Pushed)
	assert.Equal(t, 0, st.PushedCount)
}
