package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// silentLogger discards server log output so `go test -v` stays readable.
var silentLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// newTestSession spins up a Sapien MCP server backed by a fresh fixture
// engine and connects an in-memory client to it, reporting clientImplName as
// the client's name (what Config.For keys permissions on).
func newTestSession(t *testing.T, cfg Config, clientImplName string) *sdkmcp.ClientSession {
	t.Helper()
	cs, _ := newTestSessionAndEngine(t, cfg, clientImplName)
	return cs
}

// newTestSessionAndEngine is newTestSession, additionally returning the
// backing *fakeEngine so a test can inspect state a tool call doesn't
// surface directly (e.g. the options run_flow passed to Runner().RunFlow),
// or point its workspace at a real temp directory for a path-based tool
// input.
func newTestSessionAndEngine(t *testing.T, cfg Config, clientImplName string) (*sdkmcp.ClientSession, *fakeEngine) {
	t.Helper()
	eng := newFixtureEngine()
	srv := NewServer(Options{Engine: eng, Config: cfg, Version: "test", Logger: silentLogger})

	c1, c2 := sdkmcp.NewInMemoryTransports()
	ctx := context.Background()

	if _, err := srv.Connect(ctx, c1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: clientImplName, Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, c2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, eng
}

// callTool calls a tool and fails the test on a transport-level error (but
// not a tool-level IsError, which callers assert on directly).
func callTool(t *testing.T, cs *sdkmcp.ClientSession, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, "tool call %s transport error", name)
	return res
}

// decodeStructured re-marshals a CallToolResult's StructuredContent (or any
// other any-typed value) into T.
func decodeStructured[T any](t *testing.T, v any) T {
	t.Helper()
	var out T
	b, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// mustMarshal JSON-marshals v, failing the test on error.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// firstText returns the text of the first TextContent block, if any.
func firstText(res *sdkmcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
