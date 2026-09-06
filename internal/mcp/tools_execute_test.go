package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTool_ExecuteAPI_BodyCap exercises PLAN §23.3's 16 KB response-body cap:
// a response body over the cap is replaced with a truncated placeholder
// string pointing the agent at get_run for the full body.
func TestTool_ExecuteAPI_BodyCap(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{
		"id": "rider-service.bigResponse", "env": "staging",
	})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[RunView](t, res.StructuredContent)
	require.Len(t, out.Steps, 1)
	require.NotNil(t, out.Steps[0].Response)
	assert.True(t, out.Steps[0].Response.Truncated)

	body, ok := out.Steps[0].Response.Body.(string)
	require.True(t, ok, "capped body should be rendered as a placeholder string, got %T", out.Steps[0].Response.Body)
	assert.Contains(t, body, "truncated")
	assert.Contains(t, body, "get_run")
}
