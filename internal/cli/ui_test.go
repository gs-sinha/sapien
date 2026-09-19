package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
)

func TestUI_NoOpen_Human(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	ws := loadWorkspace(t, wsDir)
	info := startFakeDaemon(t, ws, cli.Version, 0)

	wantURL := fmt.Sprintf("http://sapien.localhost:%d/ui/session?token=%s", info.Port, info.Token)

	stdout, stderr, code := run(t, "--workspace", wsDir, "ui", "--no-open")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, wantURL)
	assert.Empty(t, stderr)
}

func TestUI_NoOpen_JSON(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	ws := loadWorkspace(t, wsDir)
	info := startFakeDaemon(t, ws, cli.Version, 0)

	wantURL := fmt.Sprintf("http://sapien.localhost:%d/ui/session?token=%s", info.Port, info.Token)

	stdout, stderr, code := run(t, "--workspace", wsDir, "ui", "--no-open", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, wantURL, got["url"])
	assert.Equal(t, float64(info.Port), got["port"])
}

// TestUI_MissingOpener_FallsBackToPrintingURL exercises the (default,
// --no-open not given) attempt to open a browser, without actually
// launching one in CI: with PATH pointed at an empty directory, none of
// the platform openers (open/xdg-open/rundll32) can be found, so `sapien
// ui` should still succeed, having already printed the URL, and note the
// failure on stderr instead of failing the command.
func TestUI_MissingOpener_FallsBackToPrintingURL(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	ws := loadWorkspace(t, wsDir)
	info := startFakeDaemon(t, ws, cli.Version, 0)

	t.Setenv("PATH", t.TempDir())

	wantURL := fmt.Sprintf("http://sapien.localhost:%d/ui/session?token=%s", info.Port, info.Token)

	stdout, stderr, code := run(t, "--workspace", wsDir, "ui")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, wantURL)
	assert.Contains(t, stderr, "could not open a browser automatically")
}

// TestUI_OpensViaFakeOpener exercises openURL's success path (PATH
// resolves the opener, exec.Command(...).Start() launches it) without
// actually opening a real browser in CI: PATH is pointed at a directory
// holding a script named like the platform's real opener that just exits
// immediately, so LookPath finds it and Start() succeeds against it.
func TestUI_OpensViaFakeOpener(t *testing.T) {
	var openerName string
	switch runtime.GOOS {
	case "darwin":
		openerName = "open"
	case "linux":
		openerName = "xdg-open"
	default:
		t.Skipf("no scripted opener for GOOS=%s", runtime.GOOS)
	}

	wsDir := setupRealFixtureWorkspace(t)
	ws := loadWorkspace(t, wsDir)
	info := startFakeDaemon(t, ws, cli.Version, 0)

	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, openerName), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", binDir)

	wantURL := fmt.Sprintf("http://sapien.localhost:%d/ui/session?token=%s", info.Port, info.Token)

	stdout, stderr, code := run(t, "--workspace", wsDir, "ui")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, wantURL)
	assert.Empty(t, stderr)
}

// TestUI_LoopbackIP proves --loopback-ip (PLAN §34f item 3) opts back into
// the plain 127.0.0.1 URL, for a browser (Safari, as of this writing) that
// won't resolve *.localhost out of the box.
func TestUI_LoopbackIP(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	ws := loadWorkspace(t, wsDir)
	info := startFakeDaemon(t, ws, cli.Version, 0)

	wantURL := fmt.Sprintf("http://127.0.0.1:%d/ui/session?token=%s", info.Port, info.Token)

	stdout, stderr, code := run(t, "--workspace", wsDir, "ui", "--no-open", "--loopback-ip", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, wantURL, got["url"])
}

func TestUI_NoWorkspace(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := run(t, "--workspace", dir, "ui", "--no-open")
	assert.NotEqual(t, 0, code)
	assert.NotEmpty(t, stderr)
}
