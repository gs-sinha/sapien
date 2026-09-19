package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
)

// noRedirectClient returns an *http.Client that stops at the first
// redirect instead of following it, so tests can inspect the 302 itself
// (status, Location, Set-Cookie).
func noRedirectClient(c *http.Client) *http.Client {
	c2 := *c
	c2.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	return &c2
}

func TestUISession_SetsCookieAndRedirects(t *testing.T) {
	_, ts := newTestServer(t, nil)
	client := noRedirectClient(ts.Client())

	resp, err := client.Get(ts.URL + "/ui/session?token=test-token")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/ui/", resp.Header.Get("Location"))

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	require.NotNil(t, cookie, "expected a %s cookie to be set", sessionCookieName)
	assert.Equal(t, "test-token", cookie.Value)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	assert.Equal(t, "/", cookie.Path)
	assert.False(t, cookie.Secure)
	// PLAN §34f item 3: a restart persists the daemon's bearer token, so
	// the cookie itself should now outlive the browser's own session
	// rather than expiring with it.
	assert.Equal(t, sessionCookieMaxAge, cookie.MaxAge)
}

func TestUISession_WrongTokenIs401(t *testing.T) {
	_, ts := newTestServer(t, nil)
	client := noRedirectClient(ts.Client())

	resp, err := client.Get(ts.URL + "/ui/session?token=not-the-token")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Empty(t, resp.Cookies())
}

func TestUISession_MissingTokenIs401(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp, err := ts.Client().Get(ts.URL + "/ui/session")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestCookieAcceptedOnAPIRequest(t *testing.T) {
	_, ts := newTestServer(t, nil)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/workspace", nil)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-token"})

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestCookieWithWrongValueOnAPIRequestIs401(t *testing.T) {
	_, ts := newTestServer(t, nil)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/workspace", nil)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "not-the-token"})

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestHeaderStillWorksAlongsideCookieSupport guards against a regression
// where adding cookie support to authMiddleware broke the existing
// Authorization-header path every non-browser client (CLI, MCP hosts)
// uses.
func TestHeaderStillWorksAlongsideCookieSupport(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/workspace", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestCookieOnPOSTWithoutOriginIsRejected(t *testing.T) {
	_, ts := newTestServer(t, nil)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/services/sync", nil)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-token"})

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestCookieOnPOSTWithOriginIsAccepted(t *testing.T) {
	_, ts := newTestServer(t, nil)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/services/sync", nil)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-token"})
	req.Header.Set("Origin", "http://127.0.0.1:5173")

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestHeaderOnPOSTNeedsNoOrigin confirms the CSRF-defense Origin
// requirement is specific to cookie auth: a header-authenticated
// state-changing request (every non-browser client) needs no Origin at
// all, exactly as before cookie support existed.
func TestHeaderOnPOSTNeedsNoOrigin(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodPost, "/v1/services/sync", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestCookieAcceptedOnWebSocket(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	srv := New(Options{
		Engine:  fake,
		Token:   "test-token",
		Version: "1.0.0",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, err := srv.ListenAndServe(ctx, "127.0.0.1:0")
	require.NoError(t, err)

	conn, _, err := websocket.Dial(context.Background(), "ws://"+addr+"/v1/events", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{sessionCookieName + "=test-token"}},
	})
	require.NoError(t, err)
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func TestUIServedWithoutAuth(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp, err := ts.Client().Get(ts.URL + "/ui/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestUIRedirectsBareUI(t *testing.T) {
	_, ts := newTestServer(t, nil)
	client := noRedirectClient(ts.Client())

	resp, err := client.Get(ts.URL + "/ui")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/ui/", resp.Header.Get("Location"))
}
