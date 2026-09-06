package cli_test

import (
	"context"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/cli"
	"github.com/growsimplee/sapien/internal/daemon"
)

// After `make build`, the MCP entry still points at the same binary path
// but a daemon from the previous build may still be alive (an open agent
// session keeps it busy). `sapien mcp` must replace it rather than fail
// with a hint nobody will read.
func TestMCP_ReplaceStaleDaemon_StopsOldBuild(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)

	pid := spawnKillableProcess(t)
	startFakeDaemon(t, ws, "not-"+cli.Version, pid)

	require.NoError(t, cli.ReplaceStaleDaemon(context.Background(), ws, cli.Version))
	assert.NoFileExists(t, daemon.Path(ws))
	assert.Error(t, syscall.Kill(pid, 0), "old daemon process should be gone")
}

func TestMCP_ReplaceStaleDaemon_NoDaemonIsNoop(t *testing.T) {
	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)
	require.NoError(t, cli.ReplaceStaleDaemon(context.Background(), ws, cli.Version))
}
