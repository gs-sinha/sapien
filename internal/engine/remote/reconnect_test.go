package remote_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/engine/remote"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/server"
)

// newTestDaemon spins up a real internal/server (backed by a seeded
// enginetest.Fake) over httptest, guarded by token -- a stand-in for one
// incarnation of a workspace's daemon. Tests use two or more of these to
// play the role of "the daemon before a restart" and "the daemon after".
func newTestDaemon(t *testing.T, ws *domain.Workspace, token string) *httptest.Server {
	t.Helper()
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	srv := server.New(server.Options{
		Engine:  fake,
		Token:   token,
		Version: "1.2.3",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func testWorkspace(t *testing.T) *domain.Workspace {
	t.Helper()
	return &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
}

// TestReconnect_ConnectionRefused_FallsOverToResolvedEndpoint is the core
// scenario from the feedback: the daemon Remote was talking to disappears
// (a `serve --restart`, or the idle timeout), and a resolver that finds
// (or respawns) the workspace's current daemon lets the call succeed
// instead of failing for the rest of the process's life.
func TestReconnect_ConnectionRefused_FallsOverToResolvedEndpoint(t *testing.T) {
	ws := testWorkspace(t)
	a := newTestDaemon(t, ws, "token-a")
	b := newTestDaemon(t, ws, "token-b")

	var resolveCalls int32
	rc, err := remote.New(a.URL, "token-a", remote.WithEndpointResolver(func(context.Context) (string, string, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return b.URL, "token-b", nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	// Baseline: talking to A.
	got, err := rc.Services().List(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.Equal(t, int32(0), atomic.LoadInt32(&resolveCalls))

	// A is gone now, exactly as if `serve --restart` or the idle timeout
	// recycled the daemon Remote was originally constructed against.
	a.Close()

	got, err = rc.Services().List(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.Equal(t, int32(1), atomic.LoadInt32(&resolveCalls))

	// The swap is sticky: once reconnected to B, further calls go
	// straight there -- no repeated resolver calls.
	_, err = rc.Services().List(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&resolveCalls))
}

// TestReconnect_401AtConstruction_ResolvesNewToken exercises the 401 path
// (a token the daemon no longer accepts) through New itself, which issues
// its own request (GET /v1/workspace) via the same do().
func TestReconnect_401AtConstruction_ResolvesNewToken(t *testing.T) {
	ws := testWorkspace(t)
	srv := newTestDaemon(t, ws, "correct-token")

	rc, err := remote.New(srv.URL, "stale-token", remote.WithEndpointResolver(func(context.Context) (string, string, error) {
		return srv.URL, "correct-token", nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	assert.Equal(t, ws.Dir, rc.Workspace().Dir)

	// Already swapped to the correct token; no further 401 needed.
	_, err = rc.Services().List(context.Background())
	require.NoError(t, err)
}

// TestReconnect_401MidSession_TokenRotatedSameAddress simulates a daemon
// that restarts and rotates its bearer token but happens to come back on
// the same address (the connection itself succeeds; only the token is
// stale), mid-session, well after a successful initial call.
func TestReconnect_401MidSession_TokenRotatedSameAddress(t *testing.T) {
	ws := testWorkspace(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	startAt := func(token string) *httptest.Server {
		fake := enginetest.New(ws)
		enginetest.Seed(fake)
		srv := server.New(server.Options{
			Engine: fake, Token: token, Version: "1.2.3",
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		ts := httptest.NewUnstartedServer(srv.Handler())
		l, err := net.Listen("tcp", addr)
		require.NoError(t, err)
		ts.Listener = l
		ts.Start()
		return ts
	}

	srv1 := startAt("token-1")

	var resolveCalls int32
	rc, err := remote.New(srv1.URL, "token-1", remote.WithEndpointResolver(func(context.Context) (string, string, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return srv1.URL, "token-2", nil // same address; only the token rotated
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	_, err = rc.Services().List(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(0), atomic.LoadInt32(&resolveCalls))

	srv1.Close()
	srv2 := startAt("token-2")
	t.Cleanup(srv2.Close)

	_, err = rc.Services().List(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&resolveCalls), "the 401 must trigger exactly one reconnect")
}

// TestReconnect_ResolverError_SurfacesAsOriginalErrorWithHint checks that
// when the resolver itself can't find a live daemon, do() returns the
// original failure (not a generic resolver error), enriched with the
// resolver's error as a hint and a detail -- so a caller who doesn't
// inspect Details still sees, in the message/hint, that reconnecting was
// also attempted and why it failed.
func TestReconnect_ResolverError_SurfacesAsOriginalErrorWithHint(t *testing.T) {
	ws := testWorkspace(t)
	a := newTestDaemon(t, ws, "token-a")

	resolverErr := errors.New("no live daemon for workspace")
	rc, err := remote.New(a.URL, "token-a", remote.WithEndpointResolver(func(context.Context) (string, string, error) {
		return "", "", resolverErr
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	a.Close()

	_, err = rc.Services().List(context.Background())
	require.Error(t, err)
	assert.Equal(t, errs.HTTPTransport, errs.CodeOf(err))

	e := errs.As(err)
	assert.Contains(t, e.Message, "GET", "the original request's error is preserved, not replaced")
	assert.Contains(t, e.Hint, resolverErr.Error())
	assert.Equal(t, resolverErr.Error(), e.Details["reconnect_error"])
	require.NotNil(t, e.Cause, "the original connection failure is still the error's cause")
}

// TestReconnect_NoResolverConfigured_FailsAsBefore is the pre-existing
// behaviour with no resolver: a connection failure is a plain
// errs.HTTPTransport, with no reconnect attempted.
func TestReconnect_NoResolverConfigured_FailsAsBefore(t *testing.T) {
	ws := testWorkspace(t)
	a := newTestDaemon(t, ws, "token-a")

	rc, err := remote.New(a.URL, "token-a")
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	a.Close()

	_, err = rc.Services().List(context.Background())
	require.Error(t, err)
	assert.Equal(t, errs.HTTPTransport, errs.CodeOf(err))
}

// TestReconnect_RetryableBody_POSTSurvivesFailover proves a request
// carrying a JSON body is buffered so the retry against the resolved
// endpoint sends the exact same payload, not an empty or partially
// consumed one.
func TestReconnect_RetryableBody_POSTSurvivesFailover(t *testing.T) {
	ws := testWorkspace(t)
	a := newTestDaemon(t, ws, "token-a")
	b := newTestDaemon(t, ws, "token-b")

	rc, err := remote.New(a.URL, "token-a", remote.WithEndpointResolver(func(context.Context) (string, string, error) {
		return b.URL, "token-b", nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	a.Close()

	src := domain.Source{Kind: domain.SourceLocal, Path: "services/new-service"}
	got, err := rc.Services().Add(context.Background(), "new-service", src)
	require.NoError(t, err)
	assert.Equal(t, "new-service", got.Name)
	assert.Equal(t, src, got.Source)
}

// TestReconnect_EOF_TreatedAsConnError proves isConnError's io.EOF check:
// a server that accepts the connection and then closes it without writing
// a response (as opposed to refusing the connection outright) must also
// be treated as a connection-level failure worth a reconnect.
func TestReconnect_EOF_TreatedAsConnError(t *testing.T) {
	ws := testWorkspace(t)

	closesConn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		require.True(t, ok)
		conn, _, err := hj.Hijack()
		require.NoError(t, err)
		_ = conn.Close()
	}))
	t.Cleanup(closesConn.Close)

	b := newTestDaemon(t, ws, "token-b")

	var resolveCalls int32
	rc, err := remote.New(closesConn.URL, "token-a", remote.WithEndpointResolver(func(context.Context) (string, string, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return b.URL, "token-b", nil
	}))
	require.NoError(t, err, "New's own request should have recovered via the resolver")
	t.Cleanup(func() { _ = rc.Close() })
	assert.Equal(t, int32(1), atomic.LoadInt32(&resolveCalls))

	got, err := rc.Services().List(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

// TestReconnect_ContextCancelled_NotRetried proves a cancelled/expired
// context is never treated as a connection failure worth a reconnect --
// the task explicitly excludes it ("but NOT a context cancellation").
func TestReconnect_ContextCancelled_NotRetried(t *testing.T) {
	ws := testWorkspace(t)
	a := newTestDaemon(t, ws, "token-a")

	var resolveCalls int32
	rc, err := remote.New(a.URL, "token-a", remote.WithEndpointResolver(func(context.Context) (string, string, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return a.URL, "token-a", nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = rc.Services().List(ctx)
	require.Error(t, err)
	assert.Equal(t, int32(0), atomic.LoadInt32(&resolveCalls), "a cancelled context must never trigger a reconnect")
}
