package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/errs"
)

// newTestServer builds a Server over a freshly seeded Fake and serves it
// via httptest, so tests exercise the real chi router, middleware stack,
// and handlers end to end. Callers may adjust Options via mutate before
// the Server is constructed.
func newTestServer(t *testing.T, mutate func(*Options)) (*enginetest.Fake, *httptest.Server) {
	t.Helper()
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	opts := Options{
		Engine:  fake,
		Token:   "test-token",
		Version: "1.2.3",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if mutate != nil {
		mutate(&opts)
	}

	srv := New(opts)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return fake, ts
}

// reqOpts customizes one test request. host, when set, overrides the Host
// header (net/http requires this go through Request.Host, not
// Header.Set("Host", ...), which net/http ignores for outgoing requests).
type reqOpts struct {
	token  string
	host   string
	origin string
}

func doReq(t *testing.T, ts *httptest.Server, method, path string, o reqOpts) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	require.NoError(t, err)
	if o.token != "" {
		req.Header.Set("Authorization", "Bearer "+o.token)
	}
	if o.host != "" {
		req.Host = o.host
	}
	if o.origin != "" {
		req.Header.Set("Origin", o.origin)
	}
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decodeErrBody(t *testing.T, resp *http.Response) *errs.Error {
	t.Helper()
	var e errs.Error
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	return &e
}

func TestHealthIsUnauthenticated(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/health", reqOpts{})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body healthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.True(t, body.OK)
	assert.Equal(t, "1.2.3", body.Version)
	assert.NotEmpty(t, body.Workspace)
}

func TestAuthMissingTokenIs401(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/workspace", reqOpts{})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, errs.PermissionDenied, decodeErrBody(t, resp).Code)
}

func TestAuthWrongTokenIs401(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/workspace", reqOpts{token: "not-the-token"})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, errs.PermissionDenied, decodeErrBody(t, resp).Code)
}

func TestAuthCorrectTokenSucceeds(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/workspace", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestBadHostIs403(t *testing.T) {
	_, ts := newTestServer(t, nil)

	// hostOriginMiddleware runs before auth, and even for /v1/health.
	resp := doReq(t, ts, http.MethodGet, "/v1/health", reqOpts{host: "evil.example.com"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, errs.PermissionDenied, decodeErrBody(t, resp).Code)
}

func TestLoopbackHostsAccepted(t *testing.T) {
	_, ts := newTestServer(t, nil)

	for _, host := range []string{"localhost:9999", "127.0.0.1:9999", "[::1]:9999", "localhost"} {
		resp := doReq(t, ts, http.MethodGet, "/v1/health", reqOpts{host: host})
		assert.Equal(t, http.StatusOK, resp.StatusCode, "host %q should be accepted", host)
	}
}

func TestBadOriginIs403(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/health", reqOpts{origin: "https://evil.example.com"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, errs.PermissionDenied, decodeErrBody(t, resp).Code)
}

func TestAllowedOriginsAccepted(t *testing.T) {
	_, ts := newTestServer(t, nil)

	for _, origin := range []string{"tauri://localhost", "http://localhost:5173", "http://127.0.0.1:5173"} {
		resp := doReq(t, ts, http.MethodGet, "/v1/health", reqOpts{origin: origin})
		assert.Equal(t, http.StatusOK, resp.StatusCode, "origin %q should be accepted", origin)
	}
}

func TestNoOriginHeaderAccepted(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/health", reqOpts{})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestOpenAPIListsEveryRoute checks that the generated document parses and
// documents every method+path pair registered in routeTable -- the single
// source of truth both the router and the document are built from.
func TestOpenAPIListsEveryRoute(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/openapi.json", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var doc struct {
		OpenAPI string                            `json:"openapi"`
		Paths   map[string]map[string]interface{} `json:"paths"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	assert.NotEmpty(t, doc.OpenAPI)
	require.NotEmpty(t, doc.Paths)

	for _, rt := range routeTable {
		p := openAPIPath(rt.Pattern)
		methods, ok := doc.Paths[p]
		if !assert.True(t, ok, "openapi.json is missing path %q (from pattern %q)", p, rt.Pattern) {
			continue
		}
		op, ok := methods[strings.ToLower(rt.Method)]
		if !assert.True(t, ok, "openapi.json path %q is missing method %q", p, rt.Method) {
			continue
		}
		opMap, ok := op.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, rt.OperationID, opMap["operationId"])
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/does-not-exist", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp2 := doReq(t, ts, http.MethodPatch, "/v1/health", reqOpts{})
	assert.Equal(t, http.StatusMethodNotAllowed, resp2.StatusCode)
}

// TestListenAndServeReturnsPromptly checks the documented contract: it
// binds, starts serving in the background, and returns immediately with
// the actual address rather than blocking until ctx is cancelled.
func TestListenAndServeReturnsPromptly(t *testing.T) {
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

	done := make(chan struct{})
	var addr string
	var err error
	go func() {
		addr, err = srv.ListenAndServe(ctx, "127.0.0.1:0")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return promptly")
	}
	require.NoError(t, err)
	require.NotEmpty(t, addr)

	resp, err := http.Get("http://" + addr + "/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	// Give the shutdown goroutine a moment, then confirm the port is no
	// longer accepting new connections.
	require.Eventually(t, func() bool {
		_, err := http.Get("http://" + addr + "/v1/health")
		return err != nil
	}, 2*time.Second, 10*time.Millisecond)
}

func TestIdleTimeoutFiresAfterInactivity(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	idle := make(chan struct{}, 1)
	srv := New(Options{
		Engine:      fake,
		Token:       "test-token",
		Version:     "1.0.0",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		IdleTimeout: 60 * time.Millisecond,
		OnIdle:      func() { idle <- struct{}{} },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	go func() {
		addr, _ := srv.ListenAndServe(ctx, "127.0.0.1:0")
		addrCh <- addr
	}()
	<-addrCh

	select {
	case <-idle:
		t.Fatal("idle fired too early")
	case <-time.After(20 * time.Millisecond):
	}

	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Fatal("idle did not fire")
	}
}

func TestIdleTimeoutResetsOnRequests(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	idle := make(chan struct{}, 1)
	srv := New(Options{
		Engine:      fake,
		Token:       "test-token",
		Version:     "1.0.0",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		IdleTimeout: 100 * time.Millisecond,
		OnIdle:      func() { idle <- struct{}{} },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	go func() {
		addr, _ := srv.ListenAndServe(ctx, "127.0.0.1:0")
		addrCh <- addr
	}()
	addr := <-addrCh

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/v1/health")
		require.NoError(t, err)
		_ = resp.Body.Close()
		select {
		case <-idle:
			t.Fatal("idle fired despite ongoing requests")
		case <-time.After(30 * time.Millisecond):
		}
	}

	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Fatal("idle did not fire once requests stopped")
	}
}

func TestIdleTimeoutHeldOpenByWebSocket(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	idle := make(chan struct{}, 1)
	srv := New(Options{
		Engine:      fake,
		Token:       "test-token",
		Version:     "1.0.0",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		IdleTimeout: 60 * time.Millisecond,
		OnIdle:      func() { idle <- struct{}{} },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	go func() {
		addr, _ := srv.ListenAndServe(ctx, "127.0.0.1:0")
		addrCh <- addr
	}()
	addr := <-addrCh

	wsCtx, wsCancel := context.WithCancel(context.Background())
	conn, _, err := websocket.Dial(wsCtx, "ws://"+addr+"/v1/events", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer test-token"}},
	})
	require.NoError(t, err)

	select {
	case <-idle:
		t.Fatal("idle fired while a WebSocket was open")
	case <-time.After(300 * time.Millisecond):
	}

	_ = conn.Close(websocket.StatusNormalClosure, "")
	wsCancel()

	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Fatal("idle did not fire once the WebSocket closed")
	}
}
