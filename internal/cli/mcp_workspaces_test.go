package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// A daemon replaced under a running MCP bridge mints a new token. The
// primary client re-resolves on a 401; the switcher must too, or every
// switch_workspace fails with a 401 while everything else works -- the
// first friction report an agent filed against Sapien
// (github.com/gs-sinha/sapien/discussions/1).
func TestRemoteSwitcher_ReresolvesTokenAfterDaemonRestart(t *testing.T) {
	const oldToken, newToken = "old-token", "new-token"
	wsDir := t.TempDir()
	ws, err := workspace.Init(wsDir, "team")
	require.NoError(t, err)

	var registered atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+newToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": errs.PermissionDenied, "message": "bad token"}})
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/workspaces":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"dir": ws.Dir, "name": ws.Name}})
		case "POST /v1/workspaces":
			registered.Add(1)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"dir": ws.Dir, "name": ws.Name, "open": true})
		case "GET /v1/workspace":
			_ = json.NewEncoder(w).Encode(ws)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer daemon.Close()

	var resolved atomic.Int32
	resolver := func(ctx context.Context) (string, string, error) {
		resolved.Add(1)
		return daemon.URL, newToken, nil
	}
	primary := enginetest.New(&domain.Workspace{Version: 1, Name: "primary", Dir: t.TempDir()})
	sw := newRemoteSwitcher(daemon.URL, oldToken, resolver, primary)

	// List: the stale token is refused once, the resolver supplies the new
	// one, and the daemon's own list comes back -- not the local fallback.
	list := sw.List()
	require.Len(t, list, 1)
	assert.Equal(t, ws.Dir, list[0].Dir)
	assert.Equal(t, int32(1), resolved.Load())

	// Engine: registers with the refreshed token and opens a client.
	eng, err := sw.Engine(ws.Dir)
	require.NoError(t, err)
	require.NotNil(t, eng)
	assert.Equal(t, int32(1), registered.Load())
	assert.Equal(t, ws.Name, eng.Workspace().Name)
	assert.Equal(t, filepath.Clean(ws.Dir), filepath.Clean(eng.Workspace().Dir))
}

// Without a resolver a refused token is reported as what it is, with the
// daemon's error code, not as an opaque HTTP status.
func TestRemoteSwitcher_RefusedTokenIsPermissionDenied(t *testing.T) {
	wsDir := t.TempDir()
	ws, err := workspace.Init(wsDir, "team")
	require.NoError(t, err)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": errs.PermissionDenied, "message": "bad token"}})
	}))
	defer daemon.Close()

	primary := enginetest.New(&domain.Workspace{Version: 1, Name: "primary", Dir: t.TempDir()})
	sw := newRemoteSwitcher(daemon.URL, "stale", nil, primary)
	_, err = sw.Engine(ws.Dir)
	require.Error(t, err)
	assert.Equal(t, errs.PermissionDenied, errs.As(err).Code)
}
