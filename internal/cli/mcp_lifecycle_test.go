package cli_test

import (
	"context"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/cli"
	"github.com/growsimplee/sapien/internal/daemon"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine/enginetest"
	"github.com/growsimplee/sapien/internal/server"
)

// stdioMCPClient swaps os.Stdin/os.Stdout for pipes, runs `sapien
// <args...>` (expected to be an `mcp` invocation) on a goroutine, and
// connects a real MCP client to it over those pipes, so a test can drive
// the actual stdio bridge `sapien mcp` builds -- not a hand-rolled stand-in
// for it. Restores the real os.Stdin/os.Stdout on cleanup.
func stdioMCPClient(t *testing.T, args ...string) (*sdkmcp.ClientSession, <-chan int) {
	t.Helper()
	origIn, origOut := os.Stdin, os.Stdout
	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() {
		os.Stdin, os.Stdout = origIn, origOut
	})

	doneCh := make(chan int, 1)
	go func() {
		doneCh <- cli.Execute(args, io.Discard, io.Discard)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, &sdkmcp.IOTransport{Reader: outR, Writer: inW}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	return cs, doneCh
}

func callToolCLI(t *testing.T, cs *sdkmcp.ClientSession, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, "tool call %s transport error", name)
	return res
}

// TestMCP_NoDaemon_PermissionHotReload proves the CLI wiring for the
// SAPIEN_NO_DAEMON=1 branch (an isolated, in-process engine, no daemon)
// passes Options.ConfigPaths -- not just a one-time Options.Config
// snapshot -- so editing the workspace's .sapien/mcp.yaml takes effect on
// the running `sapien mcp`'s very next tool call, with no restart. This
// is the exact bug from the feedback session, reproduced through the real
// CLI command rather than the mcp package's server directly.
func TestMCP_NoDaemon_PermissionHotReload(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	mcpYAML := filepath.Join(wsDir, ".sapien", "mcp.yaml")
	require.NoError(t, os.WriteFile(mcpYAML, []byte(`
default:
  read_flows: false
`), 0o644))

	t.Setenv("SAPIEN_NO_DAEMON", "1")
	cs, doneCh := stdioMCPClient(t, "--workspace", wsDir, "mcp")

	res := callToolCLI(t, cs, "list_flows", map[string]any{})
	require.True(t, res.IsError, "expected denied before the file grants read_flows")

	require.NoError(t, os.WriteFile(mcpYAML, []byte(`
default:
  read_flows: true
`), 0o644))
	// Some filesystems only resolve mtime to the second; force it forward
	// so the loader's size+mtime stamp reliably differs even on a fast
	// test run.
	future := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(mcpYAML, future, future))

	res = callToolCLI(t, cs, "list_flows", map[string]any{})
	assert.False(t, res.IsError, "expected allowed on the next call, no restart: %v", res)

	require.NoError(t, cs.Close())
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Log("sapien mcp did not exit promptly after client disconnect (not asserted on)")
	}
}

// newFakeDaemon starts a real internal/server (backed by a seeded
// enginetest.Fake) over httptest -- the same shape phase4_test.go's
// startFakeDaemon uses, but returning the *httptest.Server itself (not
// just a *daemon.Info) so this file's tests can Close it mid-test to
// simulate a daemon going away.
func newFakeDaemon(t *testing.T, version, token string) *httptest.Server {
	t.Helper()
	fake := enginetest.New(&domain.Workspace{Dir: t.TempDir(), Name: "fake-daemon-backing"})
	enginetest.Seed(fake)
	srv := server.New(server.Options{Engine: fake, Token: token, Version: version})
	return httptest.NewServer(srv.Handler())
}

// writeDaemonInfoFor points ws's daemon.json at ts, as if `sapien serve`
// (or `serve --restart`) had just started listening there.
func writeDaemonInfoFor(t *testing.T, ws *domain.Workspace, ts *httptest.Server, version, token string) *daemon.Info {
	t.Helper()
	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	info := &daemon.Info{
		PID: os.Getpid(), Port: port, Token: token, Version: version,
		Started: time.Now(), Workspace: ws.Dir,
	}
	require.NoError(t, daemon.Write(ws, info))
	return info
}

// TestMCP_DaemonRecycled_ReconnectsTransparently proves the CLI wiring for
// the daemon-backed branch: the resolver passed to remote.New is
// findOrStartDaemon, so when the daemon `sapien mcp` originally attached
// to disappears (`serve --restart`, or the 30-minute idle timeout, PLAN
// §4) and a new one takes over daemon.json, the already-connected agent
// host's session keeps working -- it is not orphaned the way a bridge
// with a pinned port+token would be.
func TestMCP_DaemonRecycled_ReconnectsTransparently(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	httpA := newFakeDaemon(t, cli.Version, "token-a")
	writeDaemonInfoFor(t, ws, httpA, cli.Version, "token-a")

	cs, doneCh := stdioMCPClient(t, "--workspace", wsDir, "mcp")

	res := callToolCLI(t, cs, "list_flows", map[string]any{})
	require.False(t, res.IsError, "expected success against the first daemon: %v", res)

	// The daemon behind daemon.json is replaced: A goes away (as
	// `serve --restart` or the idle timeout would do) and B takes over.
	httpA.Close()
	httpB := newFakeDaemon(t, cli.Version, "token-b")
	t.Cleanup(httpB.Close)
	writeDaemonInfoFor(t, ws, httpB, cli.Version, "token-b")

	res = callToolCLI(t, cs, "list_flows", map[string]any{})
	assert.False(t, res.IsError, "expected the bridge to reconnect to the replacement daemon: %v", res)

	require.NoError(t, cs.Close())
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Log("sapien mcp did not exit promptly after client disconnect (not asserted on)")
	}
}
