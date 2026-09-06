package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

func TestTool_AddService_LocalPath(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "add_service", map[string]any{"path": "/code/order-service"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[AddServiceOutput](t, res.StructuredContent)
	assert.Equal(t, "order-service", out.Service.Name, "name derived from the path when none is given")
	assert.Equal(t, domain.SourceLocal, out.Service.Source.Kind)
	assert.Equal(t, "/code/order-service", out.Service.Source.Path)

	text := firstText(res)
	assert.Contains(t, text, "registered service order-service: 3 operations, status ok")
	assert.Contains(t, text, "0 warnings accepted (reviewed)")
	assert.Contains(t, text, "next: get_service")
	assert.NotContains(t, text, "warning [")
	assert.NotContains(t, text, warningAcceptanceGuidance)

	// It is now listed like any other service.
	list := callTool(t, cs, "list_services", map[string]any{})
	require.False(t, list.IsError, firstText(list))
	assert.Contains(t, firstText(list), "order-service")
}

func TestTool_AddService_ExplicitNameAndContract(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "add_service", map[string]any{
		"path": "/code/svc", "name": "billing", "contract": "spec/billing.yaml",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[AddServiceOutput](t, res.StructuredContent)
	assert.Equal(t, "billing", out.Service.Name)
	assert.Equal(t, "spec/billing.yaml", out.Service.Source.Contract)
}

func TestTool_AddService_GitURL(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "add_service", map[string]any{
		"url": "git@github.com:org/rider-api.git", "ref": "main", "subdir": "api",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[AddServiceOutput](t, res.StructuredContent)
	assert.Equal(t, domain.SourceGit, out.Service.Source.Kind)
	assert.Equal(t, "git@github.com:org/rider-api.git", out.Service.Source.URL)
	assert.Equal(t, "main", out.Service.Source.Ref)
	assert.Equal(t, "api", out.Service.Source.Subdir)
	assert.Equal(t, "rider-api", out.Service.Name)
}

func TestTool_AddService_GitURLPassedAsPathIsAccepted(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "add_service", map[string]any{"path": "https://github.com/org/pay-api.git"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[AddServiceOutput](t, res.StructuredContent)
	assert.Equal(t, domain.SourceGit, out.Service.Source.Kind)
	assert.Equal(t, "https://github.com/org/pay-api.git", out.Service.Source.URL)
	assert.Empty(t, out.Service.Source.Path)
}

func TestTool_AddService_RendersWarnings(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "add_service", map[string]any{"path": "/code/svc-warn"})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.Contains(t, text, "warning [W_NO_OPERATION_ID] GET /v1/stats has no operationId (/code/svc-warn/api/openapi.yaml:42)")
	assert.Contains(t, text, "0 warnings accepted (reviewed)")
	assert.Contains(t, text, warningAcceptanceGuidance)
	out := decodeStructured[AddServiceOutput](t, res.StructuredContent)
	require.Len(t, out.Service.Warnings, 1)
	assert.Empty(t, out.Service.AcceptedWarnings)
}

func TestTool_AddService_InvalidArguments(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"neither path nor url", map[string]any{}, "needs path or url"},
		{"both path and url", map[string]any{"path": "/x", "url": "git@h:o/r.git"}, "not both"},
		{"relative path", map[string]any{"path": "./order-service"}, "not absolute"},
		{"bare name", map[string]any{"path": "order-service"}, "not absolute"},
		{"ref with local path", map[string]any{"path": "/x", "ref": "main"}, "git sources only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := callTool(t, cs, "add_service", tc.args)
			require.True(t, res.IsError, "expected a tool error, got: %s", firstText(res))
			assert.Contains(t, firstText(res), "E_INVALID")
			assert.Contains(t, firstText(res), tc.want)
		})
	}
	// Nothing was registered by any of the failed calls.
	list := callTool(t, cs, "list_services", map[string]any{})
	assert.Equal(t, 1, strings.Count(firstText(list), "-service"), firstText(list))
}

func TestTool_AddService_HomeRelativePathIsAllowed(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "add_service", map[string]any{"path": "~/code/home-svc"})
	require.False(t, res.IsError, firstText(res))
}

// TestTool_AddService_Conflict exercises PLAN §23's "add_service conflicts
// on an already-registered service, and the resync path is CLI-only with
// no MCP equivalent" gap: add_service on a name that already exists
// re-syncs it instead of merely failing, and reports that plainly.
func TestTool_AddService_Conflict(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "add_service", map[string]any{"path": "/code/rider-service"})
	require.False(t, res.IsError, firstText(res))

	text := firstText(res)
	assert.Contains(t, text, "already registered as rider-service; re-synced")
	assert.Contains(t, text, "rider-service: 2 operations, status ok")

	out := decodeStructured[AddServiceOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service", out.Service.Name)
}

// --- sync_service -----------------------------------------------------

func TestTool_SyncService_ByName(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "sync_service", map[string]any{"name": "rider-service"})
	require.False(t, res.IsError, firstText(res))

	text := firstText(res)
	assert.Contains(t, text, "rider-service: 2 operations, status ok")
	assert.Contains(t, text, "0 warnings accepted (reviewed)")

	out := decodeStructured[SyncServiceOutput](t, res.StructuredContent)
	require.Len(t, out.Services, 1)
	assert.Equal(t, "rider-service", out.Services[0].Name)
}

// TestTool_SyncService_RendersWarningsLikeAddService checks that
// sync_service and add_service share one renderer: syncing a service whose
// last add_service reported a warning renders that warning the same way.
func TestTool_SyncService_RendersWarningsLikeAddService(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	added := callTool(t, cs, "add_service", map[string]any{"path": "/code/svc-warn"})
	require.False(t, added.IsError, firstText(added))

	res := callTool(t, cs, "sync_service", map[string]any{"name": "svc-warn"})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.Contains(t, text, "warning [W_NO_OPERATION_ID] GET /v1/stats has no operationId (/code/svc-warn/api/openapi.yaml:42)")
	assert.Contains(t, text, warningAcceptanceGuidance)
}

func TestTool_SyncService_All(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "sync_service", map[string]any{})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[SyncServiceOutput](t, res.StructuredContent)
	require.Len(t, out.Services, 1)
	assert.Equal(t, "rider-service", out.Services[0].Name)
	assert.Contains(t, firstText(res), "rider-service: 2 operations, status ok")
}

func TestTool_SyncService_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "sync_service", map[string]any{"name": "no-such-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_SERVICE_NOT_FOUND")
}

func TestTool_SyncService_PermissionDenied(t *testing.T) {
	cfg := Config{Default: DefaultPermissions()}
	cfg.Default.WriteServices = false
	cs := newTestSession(t, cfg, "codex")
	res := callTool(t, cs, "sync_service", map[string]any{"name": "rider-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
	assert.Contains(t, firstText(res), "write_services")
}

func TestTool_AddService_PermissionDenied(t *testing.T) {
	cfg := Config{Default: DefaultPermissions()}
	cfg.Default.WriteServices = false
	cs := newTestSession(t, cfg, "codex")
	res := callTool(t, cs, "add_service", map[string]any{"path": "/code/order-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
	assert.Contains(t, firstText(res), "write_services")
}

func TestTool_AddService_IsListedWithSchema(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	tools, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	for _, tool := range tools.Tools {
		if tool.Name != "add_service" {
			continue
		}
		assert.Contains(t, tool.Description, `get_dsl_reference("service")`)
		schema, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		for _, prop := range []string{`"path"`, `"url"`, `"name"`, `"ref"`, `"subdir"`, `"contract"`} {
			assert.Contains(t, string(schema), prop)
		}
		return
	}
	t.Fatal("add_service not listed")
}

func TestTool_GetDSLReference_ServiceTopic(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_dsl_reference", map[string]any{"topic": "service"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetDSLReferenceOutput](t, res.StructuredContent)
	assert.Equal(t, "service", out.Topic)
	assert.Contains(t, out.Text, "Service package reference")
}

func TestServerInstructions_MentionOnboarding(t *testing.T) {
	assert.Contains(t, instructions, `get_dsl_reference("service")`)
	assert.Contains(t, instructions, "add_service")
	assert.Contains(t, instructions, "reference/{flow-dsl|memory|expressions|service|flow.schema.json}")
}

func TestTool_AddService_SuggestsAgentsFileSection(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "add_service", map[string]any{"path": "/code/billing-service"})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.Contains(t, text, "CLAUDE.md or AGENTS.md")
	assert.Contains(t, text, AgentsFileSection("billing-service"))
}

func TestAgentsFileSection(t *testing.T) {
	sec := AgentsFileSection("rider-service")
	assert.True(t, strings.HasPrefix(sec, "## Sapien\n"))
	assert.Contains(t, sec, "indexed by Sapien as `rider-service`")
	assert.Contains(t, sec, "in the same change")
	assert.Contains(t, sec, `get_service("rider-service")`)
	assert.Contains(t, sec, "`sapien service sync rider-service`")
}

func TestServerInstructions_MentionAgentsFile(t *testing.T) {
	assert.Contains(t, instructions, "CLAUDE.md/AGENTS.md")
}

func TestTool_SyncService_NamesMissingEnvironments(t *testing.T) {
	// The fixture engine's rider-service declares a "staging" environment
	// and its workspace dir (/workspace) has no environment files at all.
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "sync_service", map[string]any{"name": "rider-service"})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.Contains(t, text, "environments declared by rider-service but not defined in this workspace: staging")
	assert.Contains(t, text, "sapien env scaffold --workspace /workspace")
}
