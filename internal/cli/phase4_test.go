package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/cli"
	"github.com/growsimplee/sapien/internal/daemon"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine/enginetest"
	"github.com/growsimplee/sapien/internal/engine/local"
	"github.com/growsimplee/sapien/internal/engine/remote"
	"github.com/growsimplee/sapien/internal/server"
	"github.com/growsimplee/sapien/internal/workspace"
)

// loadWorkspace loads the workspace already initialized at dir (by `sapien
// init`, or workspace.Init directly), for tests that need the
// *domain.Workspace itself -- e.g. to compute daemon.Path(ws) -- rather
// than just its directory.
func loadWorkspace(t *testing.T, dir string) *domain.Workspace {
	t.Helper()
	ws, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
	require.NoError(t, err)
	return ws
}

// startFakeDaemon writes daemon.json for ws so it names a genuinely live
// daemon: an httptest.Server fronting a real server.New/internal/server
// stack (backed by enginetest.Fake, exactly as internal/daemon's own tests
// back their "live daemon" fixture), so both daemon.Alive's health check
// and remote.New's construction-time GET /v1/workspace get real answers
// rather than a hand-rolled stub. pid defaults to os.Getpid() (so
// daemon.Alive's process-liveness probe finds the current test process,
// which is always alive) when a caller doesn't need a specific one --
// which must be true for every caller that never signals the process
// (stopDaemon/`daemon stop`/`serve --restart` send a real SIGTERM, so
// os.Getpid() must never reach those; see spawnKillableProcess).
func startFakeDaemon(t *testing.T, ws *domain.Workspace, version string, pid int) *daemon.Info {
	t.Helper()

	fake := enginetest.New(&domain.Workspace{Dir: t.TempDir(), Name: "fake-daemon-backing"})
	enginetest.Seed(fake)

	token := "test-token-" + version
	srv := server.New(server.Options{Engine: fake, Token: token, Version: version})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	u, err := url.Parse(httpSrv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	if pid == 0 {
		pid = os.Getpid()
	}

	info := &daemon.Info{
		PID:       pid,
		Port:      port,
		Token:     token,
		Version:   version,
		Started:   time.Now(),
		Workspace: ws.Dir,
	}
	require.NoError(t, daemon.Write(ws, info))
	return info
}

// spawnKillableProcess starts a long-lived, harmless child process and
// returns its PID, for the one test (daemon stop) that needs a
// daemon.Info naming a PID it is safe to send a real SIGTERM to.
// os.Getpid() must never be used for that: it would terminate the test
// binary itself.
func spawnKillableProcess(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	// Reap it as soon as it exits (from our SIGTERM, or the Kill above):
	// otherwise it lingers as a zombie, and a zombie's PID still answers a
	// signal-0 probe, so stopDaemon's "did it exit" poll would never
	// observe the exit and always burn its full 5s deadline.
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid
}

// --- mcp config ---

func TestPhase4_MCPConfig_AllClients(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	cases := []struct {
		client string
		want   string
	}{
		{"claude-code", "claude mcp add --scope user sapien --"},
		{"codex", "[mcp_servers.sapien]"},
		{"cursor", `"mcpServers"`},
		{"cowork", `"mcpServers"`},
		{"generic", `"mcpServers"`},
	}
	for _, tc := range cases {
		t.Run(tc.client, func(t *testing.T) {
			stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", tc.client)
			require.Equal(t, 0, code, "stderr: %s", stderr)
			assert.Contains(t, stdout, tc.want)
			assert.Contains(t, stdout, wsDir)
		})
	}
}

func TestPhase4_MCPConfig_JSON(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "claude-code", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "claude-code", got["client"])
	assert.Contains(t, got["config"], "claude mcp add --scope user sapien --")
}

func TestPhase4_MCPConfig_UnknownClient(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	_, stderr, code = run(t, "--workspace", wsDir, "mcp", "config", "--client", "bogus")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "unknown MCP host client")
}

func TestPhase4_MCPConfigWrite_ClaudeCode_NotOnPATH(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	t.Setenv("PATH", t.TempDir()) // guarantee `claude` is not found on PATH

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "claude-code", "--write")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "claude CLI not found on PATH")
	assert.Contains(t, stdout, "claude mcp add --scope user sapien --")
}

func TestPhase4_MCPConfigWrite_Codex(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	t.Run("config.toml absent prints the entry instead", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "codex", "--write")
		require.Equal(t, 0, code, "stderr: %s", stderr)
		assert.Contains(t, stdout, "config.toml not found")
		assert.Contains(t, stdout, "[mcp_servers.sapien]")
	})

	t.Run("config.toml present gets appended", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexDir := filepath.Join(home, ".codex")
		require.NoError(t, os.MkdirAll(codexDir, 0o755))
		cfgPath := filepath.Join(codexDir, "config.toml")
		require.NoError(t, os.WriteFile(cfgPath, []byte("# existing config\n"), 0o644))

		stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", "codex", "--write")
		require.Equal(t, 0, code, "stderr: %s", stderr)
		assert.Contains(t, stdout, "appended sapien's MCP entry to")

		data, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		assert.Contains(t, string(data), "[mcp_servers.sapien]")
		assert.Contains(t, string(data), "# existing config")
	})
}

func TestPhase4_MCPConfigWrite_Generic(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// cowork now installs into Claude Desktop's config file and is covered
	// by TestMCPConfigWrite_Cowork_MergesIntoClaudeDesktopConfig with an
	// isolated HOME; generic is the only client that just prints.
	for _, client := range []string{"generic"} {
		t.Run(client, func(t *testing.T) {
			stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "config", "--client", client, "--write", "--json")
			require.Equal(t, 0, code, "stderr: %s", stderr)
			var got map[string]string
			require.NoError(t, json.Unmarshal([]byte(stdout), &got))
			assert.Equal(t, client, got["client"])
			assert.Contains(t, got["config"], "mcpServers")
		})
	}
}

// --- daemon status/stop ---

func TestPhase4_DaemonStatus_NoDaemon(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", wsDir, "daemon", "status")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "no daemon running")

	stdout, stderr, code = run(t, "--workspace", wsDir, "daemon", "status", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, false, got["running"])
}

func TestPhase4_DaemonStatus_And_Stop_LiveDaemon(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)
	pid := spawnKillableProcess(t)
	info := startFakeDaemon(t, ws, cli.Version, pid)

	stdout, stderr, code := run(t, "--workspace", wsDir, "daemon", "status", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, true, got["running"])
	assert.Equal(t, float64(info.Port), got["port"])

	stdout, stderr, code = run(t, "--workspace", wsDir, "daemon", "stop", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var stopGot map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &stopGot))
	assert.Equal(t, true, stopGot["stopped"])
	assert.NoFileExists(t, daemon.Path(ws))
}

// --- serve ---

func TestPhase4_Serve_RefusesWhenDaemonAlive(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	ws := loadWorkspace(t, wsDir)
	startFakeDaemon(t, ws, cli.Version, 0)

	stdout, stderr, code := run(t, "--workspace", wsDir, "serve", "--port", "0", "--json")
	assert.Equal(t, 2, code, "stdout: %s stderr: %s", stdout, stderr)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_CONFLICT", got["code"])
	hint, _ := got["hint"].(string)
	assert.Contains(t, hint, "--restart")
}

// handshakeWriter is an io.Writer that watches everything written to it
// for the first complete JSON value (serve --json's handshake, a
// daemon.Info), decoding it and closing ready as soon as one appears --
// while still forwarding every byte to an internal buffer, so a test can
// both synchronize on the handshake and inspect the full output
// afterward. Safe for concurrent use: `sapien serve`'s RunE writes to it
// from a goroutine while the test goroutine waits on ready.
type handshakeWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	ready  chan struct{}
	closed bool
	info   daemon.Info
}

func newHandshakeWriter() *handshakeWriter { return &handshakeWriter{ready: make(chan struct{})} }

func (w *handshakeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if !w.closed {
		var info daemon.Info
		if jerr := json.Unmarshal(w.buf.Bytes(), &info); jerr == nil && info.Port != 0 {
			w.info = info
			w.closed = true
			close(w.ready)
		}
	}
	return n, err
}

// TestPhase4_Serve_HappyPath drives `sapien serve` for real: it binds a
// free port, writes daemon.json, serves the plain HTTP API unauthenticated
// health check and the rest of the API behind the bearer token, mounts the
// MCP endpoint at /mcp guarded by that same token (bearerGuard), and shuts
// down cleanly (removing daemon.json) once idle -- all without ever
// sending the process a signal.
func TestPhase4_Serve_HappyPath(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	hw := newHandshakeWriter()
	var stderrBuf bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- cli.Execute([]string{
			"--workspace", wsDir, "serve", "--port", "0", "--idle-timeout", "300ms", "--json",
		}, hw, &stderrBuf)
	}()

	select {
	case <-hw.ready:
	case exitCode := <-done:
		t.Fatalf("serve exited early (code %d) before printing its handshake; stderr: %s", exitCode, stderrBuf.String())
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for serve's handshake")
	}

	info := hw.info
	require.NotZero(t, info.Port)
	require.NotEmpty(t, info.Token)

	ws := loadWorkspace(t, wsDir)
	onDisk, err := daemon.Read(ws)
	require.NoError(t, err)
	assert.Equal(t, info.Port, onDisk.Port)
	assert.Equal(t, info.Token, onDisk.Token)

	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)

	// GET /v1/health needs no token.
	healthResp, err := http.Get(base + "/v1/health")
	require.NoError(t, err)
	_ = healthResp.Body.Close()
	assert.Equal(t, http.StatusOK, healthResp.StatusCode)

	// /mcp is mounted and guarded by the same bearer token, checked
	// locally (bearerGuard) since server.Server exposes only Handler().
	mcpClient := &http.Client{Timeout: 5 * time.Second}

	noAuthReq, _ := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader("{}"))
	noAuthResp, err := mcpClient.Do(noAuthReq)
	require.NoError(t, err)
	_ = noAuthResp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, noAuthResp.StatusCode, "no token")

	wrongAuthReq, _ := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader("{}"))
	wrongAuthReq.Header.Set("Authorization", "Bearer wrong-token")
	wrongAuthResp, err := mcpClient.Do(wrongAuthReq)
	require.NoError(t, err)
	_ = wrongAuthResp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, wrongAuthResp.StatusCode, "wrong token")

	rightAuthReq, _ := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader("{}"))
	rightAuthReq.Header.Set("Authorization", "Bearer "+info.Token)
	rightAuthReq.Header.Set("Content-Type", "application/json")
	rightAuthResp, err := mcpClient.Do(rightAuthReq)
	require.NoError(t, err)
	_ = rightAuthResp.Body.Close()
	assert.NotEqual(t, http.StatusUnauthorized, rightAuthResp.StatusCode, "valid token should pass bearerGuard")

	// No requests in flight -> shuts down on its own once idle, cleanly.
	select {
	case exitCode := <-done:
		require.Equal(t, 0, exitCode, "stderr: %s", stderrBuf.String())
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down on idle within 10s")
	}
	assert.NoFileExists(t, daemon.Path(ws))
}

// --- mcp --http ---

func TestPhase4_MCPHTTP_PrintsURLAndToken(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	ws := loadWorkspace(t, wsDir)
	info := startFakeDaemon(t, ws, cli.Version, 0)

	wantURL := fmt.Sprintf("http://127.0.0.1:%d/mcp", info.Port)

	stdout, stderr, code := run(t, "--workspace", wsDir, "mcp", "--http")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, wantURL)
	assert.Contains(t, stdout, info.Token)

	stdout, stderr, code = run(t, "--workspace", wsDir, "mcp", "--http", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, wantURL, got["url"])
	assert.Contains(t, got["authorization"], info.Token)
}

// --- NewEngine selection (PLAN §4) ---

func TestPhase4_NewEngine_NoDaemon_IsLocal(t *testing.T) {
	ws, err := workspace.Init(t.TempDir(), "sel-local")
	require.NoError(t, err)

	eng, err := cli.NewEngine(ws)
	require.NoError(t, err)
	defer eng.Close()

	_, ok := eng.(*local.Local)
	assert.True(t, ok, "expected *local.Local, got %T", eng)
}

func TestPhase4_NewEngine_LiveDaemon_IsRemote(t *testing.T) {
	ws, err := workspace.Init(t.TempDir(), "sel-remote")
	require.NoError(t, err)
	startFakeDaemon(t, ws, cli.Version, 0)

	eng, err := cli.NewEngine(ws)
	require.NoError(t, err)
	defer eng.Close()

	_, ok := eng.(*remote.Remote)
	assert.True(t, ok, "expected *remote.Remote, got %T", eng)
}

func TestPhase4_NewEngine_NoDaemonEnv_ForcesLocalEvenWithLiveDaemon(t *testing.T) {
	ws, err := workspace.Init(t.TempDir(), "sel-forced-local")
	require.NoError(t, err)
	startFakeDaemon(t, ws, cli.Version, 0)
	t.Setenv("SAPIEN_NO_DAEMON", "1")

	eng, err := cli.NewEngine(ws)
	require.NoError(t, err)
	defer eng.Close()

	_, ok := eng.(*local.Local)
	assert.True(t, ok, "expected *local.Local, got %T", eng)
}

func TestPhase4_NewEngine_VersionMismatch_StopsStaleDaemonAndRunsLocal(t *testing.T) {
	ws, err := workspace.Init(t.TempDir(), "sel-mismatch")
	require.NoError(t, err)
	pid := spawnKillableProcess(t)
	startFakeDaemon(t, ws, "not-"+cli.Version, pid)

	eng, err := cli.NewEngine(ws)
	require.NoError(t, err)
	defer eng.Close()
	_, ok := eng.(*local.Local)
	assert.True(t, ok, "expected *local.Local after stopping the stale daemon, got %T", eng)
	assert.NoFileExists(t, daemon.Path(ws), "daemon.json of the stale daemon is removed")
	assert.Error(t, syscall.Kill(pid, 0), "the stale daemon process is gone")
}
