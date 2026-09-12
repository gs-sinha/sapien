package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// A single-workspace server still answers GET /v1/workspaces, reporting its
// own workspace, so a client can render its picker without special-casing an
// embedding that cannot switch.
func TestWorkspacesList_SingleWorkspaceServer(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/workspaces", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out []workspaces.Info
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out, 1)
	assert.Equal(t, fake.Workspace().Dir, out[0].Dir)
	assert.Equal(t, "logistics", out[0].Name)
	assert.True(t, out[0].Primary)
	assert.True(t, out[0].Open)
}

// Naming the server's own workspace is always fine: a client that always
// sends the header (the UI, once a workspace has been picked) must keep
// working against a daemon that serves only that one.
func TestWorkspaceHeader_OwnWorkspaceIsAccepted(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/workspace", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set(WorkspaceHeader, fake.Workspace().Dir)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// Naming a workspace this server does not have is refused, rather than
// silently served from the wrong workspace -- the failure mode that would
// be hardest to notice and worst to act on.
func TestWorkspaceHeader_UnknownWorkspaceIsRefused(t *testing.T) {
	_, ts := newTestServer(t, nil)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/flows", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set(WorkspaceHeader, "/somewhere/else")

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.WorkspaceNotFound, decodeErrBody(t, resp).Code)
}

// POST /v1/workspaces on a single-workspace daemon reports a conflict with a
// hint, not a generic failure: the caller's next move is to restart it.
func TestWorkspaceRegister_SingleWorkspaceServer(t *testing.T) {
	_, ts := newTestServer(t, nil)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/workspaces", jsonBody(`{"dir":"/tmp/other"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, errs.Conflict, decodeErrBody(t, resp).Code)
}

// jsonBody wraps a literal JSON string as a request body.
func jsonBody(s string) io.Reader { return strings.NewReader(s) }

// A daemon can be alive while its workspace manager cannot open a workspace.
// Health checks must not enter that manager or report the daemon as dead.
func TestHealthDoesNotResolveWorkspace(t *testing.T) {
	fake, ts := newTestServer(t, func(opts *Options) {
		mgr := workspaces.New(opts.Engine.Workspace(), opts.Engine, workspaces.Options{})
		require.NoError(t, mgr.Close())
		opts.Workspaces = mgr
	})
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/health", nil)
	require.NoError(t, err)
	req.Header.Set(WorkspaceHeader, "/unavailable/workspace")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var health healthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))
	assert.True(t, health.OK)
	assert.Equal(t, fake.Workspace().Dir, health.Workspace)
}
