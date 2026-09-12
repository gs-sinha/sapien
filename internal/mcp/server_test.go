package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wantToolNames is every tool PLAN §23's table lists.
var wantToolNames = []string{
	"list_services", "get_service", "search_apis", "get_api", "get_dsl_reference",
	"get_context", "execute_api", "list_flows", "get_flow", "search_docs", "get_doc",
	"get_schema", "validate_flow", "create_flow", "update_flow", "patch_flow", "run_flow", "get_run",
	"search_memories", "get_relevant_memories", "create_memory", "get_promotion_target",
	"add_service", "sync_service", "rescope_memory",
	"list_examples", "get_example", "create_example", "rescope_example", "delete_example",
}

func TestServer_ListTools(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)

	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	assert.Len(t, got, len(wantToolNames))
	for _, name := range wantToolNames {
		assert.Contains(t, got, name)
	}
}

func TestServer_Instructions(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	ir := cs.InitializeResult()
	require.NotNil(t, ir)
	assert.Contains(t, ir.Instructions, "get_context")
	assert.Contains(t, ir.Instructions, "sapien://")
	assert.LessOrEqual(t, len(instructions), 1750)
}

func TestPermission_ExecuteMutationDenied(t *testing.T) {
	// Default profile grants execute_read but not execute_mutation.
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{
		"id":  "rider-service.createRider", // POST
		"env": "staging",
	})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
	assert.Contains(t, firstText(res), "execute_mutation")
	assert.Contains(t, firstText(res), `client "claude-code"`)
	assert.Contains(t, firstText(res), "clients.claude-code.execute_mutation: true")

	structured := decodeStructured[map[string]any](t, res.StructuredContent)
	assert.Equal(t, "E_PERMISSION_DENIED", structured["code"])
}

func TestPermission_ExecuteMutationGrantedByClientOverride(t *testing.T) {
	cfg := Config{
		Default: DefaultPermissions(),
		Clients: map[string]Permissions{
			"claude-code": func() Permissions { p := DefaultPermissions(); p.ExecuteMutation = true; return p }(),
		},
	}
	cs := newTestSession(t, cfg, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{
		"id":  "rider-service.createRider",
		"env": "staging",
	})
	assert.False(t, res.IsError, "text: %s", firstText(res))
}

func TestPermission_ProductionBlocked(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{
		"id":  "rider-service.getRider",
		"env": "production",
		"params": map[string]any{
			"riderId": "r1",
		},
	})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_PRODUCTION_BLOCKED")

	structured := decodeStructured[map[string]any](t, res.StructuredContent)
	assert.Equal(t, "E_PRODUCTION_BLOCKED", structured["code"])
}

func TestPermission_ProductionAllowedByConfig(t *testing.T) {
	cfg := Config{
		Default: DefaultPermissions(),
		Clients: map[string]Permissions{
			"claude-code": func() Permissions { p := DefaultPermissions(); p.AllowProduction = true; return p }(),
		},
	}
	cs := newTestSession(t, cfg, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{
		"id":     "rider-service.getRider",
		"env":    "production",
		"params": map[string]any{"riderId": "r1"},
	})
	assert.False(t, res.IsError, "text: %s", firstText(res))
}

func TestPermission_EnvironmentAllowlist(t *testing.T) {
	p := DefaultPermissions()
	p.Environments = []string{"staging"} // qa and production both excluded
	cfg := Config{Default: p}
	cs := newTestSession(t, cfg, "claude-code")

	// qa is not production, but it's not in the allowlist either.
	res := callTool(t, cs, "execute_api", map[string]any{
		"id":     "rider-service.getRider",
		"env":    "qa",
		"params": map[string]any{"riderId": "r1"},
	})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
	assert.Contains(t, firstText(res), "qa")

	// staging is in the allowlist and should succeed.
	res = callTool(t, cs, "execute_api", map[string]any{
		"id":     "rider-service.getRider",
		"env":    "staging",
		"params": map[string]any{"riderId": "r1"},
	})
	assert.False(t, res.IsError, "text: %s", firstText(res))
}

func TestPermission_ClientSpecificConfigOverridesDefault(t *testing.T) {
	restrictive := DefaultPermissions()
	restrictive.ReadFlows = false
	cfg := Config{
		Default: restrictive,
		Clients: map[string]Permissions{
			"claude-code": DefaultPermissions(), // full defaults for this one client
		},
	}

	// codex falls back to the restrictive default and is denied.
	codexSession := newTestSession(t, cfg, "codex")
	res := callTool(t, codexSession, "list_flows", map[string]any{})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "read_flows")

	// claude-code has its own entry and is allowed.
	claudeSession := newTestSession(t, cfg, "claude-code")
	res = callTool(t, claudeSession, "list_flows", map[string]any{})
	assert.False(t, res.IsError, "text: %s", firstText(res))
}

func TestPermission_GetDSLReferenceRequiresNoPermission(t *testing.T) {
	// classless tool: even a Permissions zero-value client can call it.
	cfg := Config{Default: Permissions{}}
	cs := newTestSession(t, cfg, "anything")
	res := callTool(t, cs, "get_dsl_reference", map[string]any{"topic": "flow"})
	assert.False(t, res.IsError, "text: %s", firstText(res))
}
