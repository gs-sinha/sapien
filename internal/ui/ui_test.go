package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     {Data: []byte("<html>app shell</html>")},
		"assets/app.js":  {Data: []byte("console.log('hi')")},
		"favicon.ico":    {Data: []byte("icon")},
		"nested/deep.js": {Data: []byte("deep")},
	}
}

func doGet(t *testing.T, h http.Handler, target string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestHandlerFS_ServesIndexAtUISlash(t *testing.T) {
	h := HandlerFS(testFS())
	resp := doGet(t, h, "/ui/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
}

func TestHandlerFS_RedirectsBareUIToUISlash(t *testing.T) {
	h := HandlerFS(testFS())
	resp := doGet(t, h, "/ui")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/ui/", resp.Header.Get("Location"))
}

func TestHandlerFS_ServesExistingAsset(t *testing.T) {
	h := HandlerFS(testFS())
	resp := doGet(t, h, "/ui/assets/app.js")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Cache-Control"), "immutable")
	assert.Contains(t, resp.Header.Get("Cache-Control"), "max-age=31536000")
}

func TestHandlerFS_ServesNonAssetExistingFileWithoutLongCache(t *testing.T) {
	h := HandlerFS(testFS())
	resp := doGet(t, h, "/ui/favicon.ico")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Cache-Control"))
}

func TestHandlerFS_ServesNestedExistingFile(t *testing.T) {
	h := HandlerFS(testFS())
	resp := doGet(t, h, "/ui/nested/deep.js")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandlerFS_SPAFallbackForUnknownPath(t *testing.T) {
	h := HandlerFS(testFS())
	resp := doGet(t, h, "/ui/runs/run_123")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
}

func TestHandlerFS_PathTraversalFallsBackToIndexSafely(t *testing.T) {
	h := HandlerFS(testFS())
	resp := doGet(t, h, "/ui/../../etc/passwd")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandler_ServesEmbeddedPlaceholder(t *testing.T) {
	resp := doGet(t, Handler(), "/ui/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRelPath(t *testing.T) {
	cases := map[string]string{
		"/ui":                 "index.html", // unreachable via Handler (redirected first); relPath alone still degrades safely
		"/ui/":                "index.html",
		"/ui/assets/app.js":   "assets/app.js",
		"/ui/../../etc/hosts": "etc/hosts",
	}
	for in, want := range cases {
		assert.Equalf(t, want, relPath(in), "relPath(%q)", in)
	}
}
