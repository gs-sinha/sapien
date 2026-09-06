package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServeStdio_ContextCancel exercises the ServeStdio entry point (`sapien
// mcp`, PLAN §23): it swaps os.Stdin/os.Stdout for pipes so the run doesn't
// touch the test process' real stdio, starts serving, then cancels the
// context and expects Run's context-cancellation error back.
func TestServeStdio_ContextCancel(t *testing.T) {
	origIn, origOut := os.Stdin, os.Stdout
	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() {
		os.Stdin, os.Stdout = origIn, origOut
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
	})
	// Drain stdout so the server never blocks on a full pipe buffer.
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	eng := newFixtureEngine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeStdio(ctx, Options{Engine: eng, Config: Config{Default: DefaultPermissions()}, Logger: silentLogger})
	}()

	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio did not return after context cancellation")
	}
}

// TestHTTPHandler_Initialize exercises the streamable-HTTP entry point (PLAN
// §23: "streamable HTTP on the daemon for hosts that prefer a URL") with a
// real HTTP round trip carrying an MCP initialize request.
func TestHTTPHandler_Initialize(t *testing.T) {
	eng := newFixtureEngine()
	handler := HTTPHandler(Options{Engine: eng, Config: Config{Default: DefaultPermissions()}, Logger: silentLogger})
	httpSrv := httptest.NewServer(handler)
	defer httpSrv.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"generic","version":"1.0"}}}`
	req, err := http.NewRequest(http.MethodPost, httpSrv.URL, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode, string(out))
	assert.Contains(t, string(out), "sapien")
	assert.Contains(t, string(out), "Start with get_context")
}
