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

// --- GET /v1/workspace/repo ---------------------------------------------

// TestWorkspaceRepoStatus_RoundTrip proves GET /v1/workspace/repo reports
// exactly what the engine's Repo().Status returns, after a test seeds it
// through the fake's SetRepoStatus.
func TestWorkspaceRepoStatus_RoundTrip(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{
		InGit: true, Root: "/ws", Branch: "main", Remote: "git@github.com:org/ws.git",
		Upstream: "origin/main", Behind: 3, Ahead: 1, Dirty: 2,
	})

	resp := doReq(t, ts, http.MethodGet, "/v1/workspace/repo", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var st domain.RepoStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.True(t, st.InGit)
	assert.Equal(t, "main", st.Branch)
	assert.Equal(t, "origin/main", st.Upstream)
	assert.Equal(t, 3, st.Behind)
	assert.Equal(t, 1, st.Ahead)
	assert.Equal(t, 2, st.Dirty)

	assert.True(t, hasRecordedCall(fake, "Repo.Status"))
}

// --- POST /v1/workspace/repo/fetch --------------------------------------

func TestWorkspaceRepoFetch_RecordsCall(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main"})

	resp := doReq(t, ts, http.MethodPost, "/v1/workspace/repo/fetch", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var st domain.RepoStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.Equal(t, "main", st.Branch)
	assert.True(t, hasRecordedCall(fake, "Repo.Fetch"))
}

// --- POST /v1/workspace/repo/pull ---------------------------------------

// TestWorkspaceRepoPull_DirtyTreeIsConflict: the fake's Pull refuses a
// dirty tree with errs.Conflict, and that reaches the client as a plain
// 409 -- the engine's own error passes through writeError unchanged.
func TestWorkspaceRepoPull_DirtyTreeIsConflict(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Dirty: 1})

	resp := doReq(t, ts, http.MethodPost, "/v1/workspace/repo/pull", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, errs.Conflict, decodeErrBody(t, resp).Code)
	assert.True(t, hasRecordedCall(fake, "Repo.Pull"))
}

// TestWorkspaceRepoPull_FastForwards: a clean, behind tree pulls, and the
// response carries Pulled/PulledCount.
func TestWorkspaceRepoPull_FastForwards(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 4})

	resp := doReq(t, ts, http.MethodPost, "/v1/workspace/repo/pull", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var st domain.RepoStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.True(t, st.Pulled)
	assert.Equal(t, 4, st.PulledCount)
	assert.Equal(t, 0, st.Behind)
}

// --- POST /v1/workspace/repo/sync ---------------------------------------

// TestWorkspaceRepoSync_BehindCleanPulls: sync on a behind, clean status
// pulls and reports the count -- unlike pull, sync never errors for a
// reason it can instead record in Skipped.
func TestWorkspaceRepoSync_BehindCleanPulls(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 5})

	resp := doReq(t, ts, http.MethodPost, "/v1/workspace/repo/sync", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var st domain.RepoStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.True(t, st.Pulled)
	assert.Equal(t, 5, st.PulledCount)
	assert.Equal(t, 0, st.Behind)
	assert.True(t, hasRecordedCall(fake, "Repo.Sync"))
}

// TestWorkspaceRepoSync_DirtyTreeSkipsWithoutError: sync on a dirty tree
// never errors; it reports why nothing moved.
func TestWorkspaceRepoSync_DirtyTreeSkipsWithoutError(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 2, Dirty: 1})

	resp := doReq(t, ts, http.MethodPost, "/v1/workspace/repo/sync", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var st domain.RepoStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.False(t, st.Pulled)
	assert.Equal(t, "uncommitted changes", st.Skipped)
	assert.True(t, hasRecordedCall(fake, "Repo.Sync"))
}
