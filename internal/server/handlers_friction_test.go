package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/friction"
)

// stubGH is a gh that answers the two GraphQL calls Publish makes and
// nothing else, so a test exercises the whole send path without GitHub.
const stubGH = `#!/bin/sh
case "$*" in
  *hasDiscussionsEnabled*) echo '{"data":{"repository":{"id":"R_1","hasDiscussionsEnabled":true,"discussionCategories":{"nodes":[{"id":"DIC_1","name":"General","slug":"general"}]}}}}' ;;
  *createDiscussion*) echo '{"data":{"createDiscussion":{"discussion":{"url":"https://github.com/gs-sinha/sapien/discussions/7"}}}}' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`

// frictionFixture points the store at a temp dir, puts a stub gh first on
// PATH, and files one pending report.
func frictionFixture(t *testing.T) friction.Report {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SAPIEN_FRICTION_DIR", dir)
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(stubGH), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	created, err := friction.New("").Create(context.Background(), friction.Report{
		Title:    "get_api omits array examples",
		Happened: "request_example had no items element",
		Client:   "test",
	})
	require.NoError(t, err)
	return *created
}

func decodeInto[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var out T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func TestFrictionListPreviewSendDrop(t *testing.T) {
	rep := frictionFixture(t)
	_, ts := newTestServer(t, nil)
	auth := reqOpts{token: "test-token"}

	resp := doReq(t, ts, http.MethodGet, "/v1/friction", auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	list := decodeInto[[]friction.Report](t, resp)
	require.Len(t, list, 1)
	assert.Equal(t, rep.ID, list[0].ID)
	assert.Equal(t, friction.StatusPending, list[0].Status)

	// The preview is the exact Discussion send would create, plus where.
	resp = doReq(t, ts, http.MethodGet, "/v1/friction/"+rep.ID+"/preview", auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	preview := decodeInto[frictionPreview](t, resp)
	assert.Contains(t, preview.Title, "get_api omits array examples")
	assert.Contains(t, preview.Body, "request_example had no items element")
	assert.Equal(t, "gs-sinha/sapien", preview.Repo)
	assert.Equal(t, "General", preview.Category)

	resp = doReq(t, ts, http.MethodPost, "/v1/friction/"+rep.ID+"/send", auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	sent := decodeInto[friction.Report](t, resp)
	assert.Equal(t, friction.StatusSent, sent.Status)
	assert.Equal(t, "https://github.com/gs-sinha/sapien/discussions/7", sent.SentURL)

	resp = doReq(t, ts, http.MethodDelete, "/v1/friction/"+rep.ID, auth)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	resp = doReq(t, ts, http.MethodGet, "/v1/friction", auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, decodeInto[[]friction.Report](t, resp))
}

func TestFrictionUnknownReport(t *testing.T) {
	t.Setenv("SAPIEN_FRICTION_DIR", t.TempDir())
	_, ts := newTestServer(t, nil)
	auth := reqOpts{token: "test-token"}

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/friction/fr_nope/preview"},
		{http.MethodPost, "/v1/friction/fr_nope/send"},
		{http.MethodDelete, "/v1/friction/fr_nope"},
	} {
		resp := doReq(t, ts, tc.method, tc.path, auth)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "%s %s", tc.method, tc.path)
		assert.NotEmpty(t, decodeErrBody(t, resp).Code)
	}
}

// Every friction route sits behind the bearer token like the rest of the
// API: the queue is the user's, not the loopback's.
func TestFrictionRequiresAuth(t *testing.T) {
	t.Setenv("SAPIEN_FRICTION_DIR", t.TempDir())
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/friction", reqOpts{})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, errs.PermissionDenied, decodeErrBody(t, resp).Code)
}
