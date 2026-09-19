package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/daemon"
)

// TestPhase4_Serve_TokenPersistsAcrossRestarts drives two independent
// `sapien serve` runs (each idling out on its own, never sent a signal, so
// this proves persistence rather than in-memory reuse across one process)
// against the same isolated $HOME, and checks that the bearer token both
// print is the same one -- PLAN §34f item 3's whole point: a restart must
// not invalidate every open browser tab and MCP bridge that is already
// holding the old token.
func TestPhase4_Serve_TokenPersistsAcrossRestarts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SAPIEN_CONFIG", filepath.Join(home, ".sapien", "config.yaml"))

	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	first := runServeOnce(t, wsDir)
	second := runServeOnce(t, wsDir)

	require.NotEmpty(t, first.Token)
	assert.Equal(t, first.Token, second.Token)

	tokenOnDisk, err := daemon.LoadOrCreateToken()
	require.NoError(t, err)
	assert.Equal(t, first.Token, tokenOnDisk)
}

// TestPhase4_Serve_GetDaemon drives `sapien serve` for real and checks
// GET /v1/daemon's shape end to end (PLAN §34f item 3), beyond what
// internal/server's own handler tests already cover against a fake
// engine: this proves the fields serve.go itself is responsible for
// injecting (Port, Started, Commit) actually reach the wire.
func TestPhase4_Serve_GetDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SAPIEN_CONFIG", filepath.Join(home, ".sapien", "config.yaml"))

	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	info, done := startServe(t, wsDir)
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("serve did not shut down on idle within 10s")
		}
	})

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/daemon", info.Port), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Version        string `json:"version"`
		Port           int    `json:"port"`
		PID            int    `json:"pid"`
		InstallMethod  string `json:"install_method"`
		WorkspacesOpen int    `json:"workspaces_open"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, cli.Version, got.Version)
	assert.Equal(t, info.Port, got.Port)
	assert.Equal(t, info.PID, got.PID)
	assert.NotEmpty(t, got.InstallMethod)
	assert.Equal(t, 1, got.WorkspacesOpen)
}

// startServe starts `sapien serve --idle-timeout 200ms` for wsDir and waits
// for its handshake, returning immediately after -- the server is still
// running, and done reports its eventual exit code (a well-behaved
// idle-exit shutdown, since nothing here ever signals it).
func startServe(t *testing.T, wsDir string) (info daemon.Info, done <-chan int) {
	t.Helper()
	hw := newHandshakeWriter()
	var stderrBuf bytes.Buffer
	doneCh := make(chan int, 1)
	go func() {
		doneCh <- cli.Execute([]string{
			"--workspace", wsDir, "serve", "--port", "0", "--idle-timeout", "200ms", "--json",
		}, hw, &stderrBuf)
	}()

	select {
	case <-hw.ready:
	case exitCode := <-doneCh:
		t.Fatalf("serve exited early (code %d) before printing its handshake; stderr: %s", exitCode, stderrBuf.String())
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for serve's handshake")
	}
	return hw.info, doneCh
}

// runServeOnce is startServe, but also waits for the idle-exit shutdown
// before returning -- for a test (like token persistence, above) that
// needs a fully independent second run rather than a still-live server.
func runServeOnce(t *testing.T, wsDir string) daemon.Info {
	t.Helper()
	info, done := startServe(t, wsDir)
	select {
	case exitCode := <-done:
		require.Equal(t, 0, exitCode)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down on idle within 10s")
	}
	return info
}
