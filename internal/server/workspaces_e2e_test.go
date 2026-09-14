package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/workspace"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// The whole point of the multi-workspace daemon: one HTTP server, one
// origin, one session cookie, and requests that land in different
// workspaces according to a header.
func TestMultiWorkspaceServer_RoutesByHeader(t *testing.T) {
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	primaryDir := writeWorkspace(t, "primary")
	otherDir := writeWorkspace(t, "other")
	require.NoError(t, config.AddWorkspace(otherDir))

	primary, err := workspace.Load(filepath.Join(primaryDir, domain.WorkspaceFileName))
	require.NoError(t, err)

	primaryEng := enginetest.New(primary)
	mgr := workspaces.New(primary, primaryEng, workspaces.Options{
		PID: os.Getpid(),
		Open: func(ws *domain.Workspace, _ local.Options) (engine.Engine, error) {
			return enginetest.New(ws), nil
		},
	})
	t.Cleanup(func() { _ = mgr.Close() })

	srv := New(Options{
		Engine:     primaryEng,
		Workspaces: mgr,
		Token:      "test-token",
		Version:    "1.2.3",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// No header: the primary.
	assert.Equal(t, primaryDir, getWorkspaceDir(t, ts, ""))
	// Header: the other workspace, opened on demand by the manager.
	assert.Equal(t, otherDir, getWorkspaceDir(t, ts, otherDir))
	// And back, with the first workspace still open and unaffected.
	assert.Equal(t, primaryDir, getWorkspaceDir(t, ts, primaryDir))

	// Both now show up as open in the listing the picker reads.
	resp := doReq(t, ts, http.MethodGet, "/v1/workspaces", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list []workspaces.Info
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))

	byDir := map[string]workspaces.Info{}
	for _, w := range list {
		byDir[w.Dir] = w
	}
	require.Contains(t, byDir, primaryDir)
	require.Contains(t, byDir, otherDir)
	assert.True(t, byDir[primaryDir].Primary)
	assert.False(t, byDir[otherDir].Primary)
	assert.True(t, byDir[otherDir].Open)
}

func writeWorkspace(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, domain.WorkspaceFileName),
		[]byte("version: 1\nname: "+name+"\n"), 0o644))
	return dir
}

// getWorkspaceDir asks GET /v1/workspace which workspace a request with this
// selector lands in.
func getWorkspaceDir(t *testing.T, ts *httptest.Server, selector string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/workspace", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	if selector != "" {
		req.Header.Set(WorkspaceHeader, selector)
	}

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var ws domain.Workspace
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ws))
	return ws.Dir
}
