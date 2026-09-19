package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// TestRenderFlowOutline_ShowsWhen confirms a step's `when` (PLAN §34f.7)
// shows up compactly in get_flow(detail=outline).
func TestRenderFlowOutline_ShowsWhen(t *testing.T) {
	f := &domain.Flow{
		ID: "f",
		Steps: []domain.Step{
			{ID: "a", Call: "svc.op", When: "inputs.releaseNow"},
			{ID: "b", Call: "svc.op2"},
		},
	}
	out := renderFlowOutline(f)
	assert.Contains(t, out, "a: svc.op when:inputs.releaseNow")
	assert.NotContains(t, out, "b: svc.op2 when:")
}

// TestRenderFlowOutline_IndentsBlocks confirms a loop block and its nested
// steps show up compactly and indented one level deeper (PLAN §34f.8).
func TestRenderFlowOutline_IndentsBlocks(t *testing.T) {
	f := &domain.Flow{
		ID: "f",
		Steps: []domain.Step{
			{
				ID:      "each",
				Foreach: "inputs.ids",
				Max:     50,
				Steps: []domain.Step{
					{ID: "create", Call: "svc.op"},
				},
			},
			{
				ID:     "page",
				Repeat: &domain.Repeat{Until: "steps.fetch.out.done", Max: 10},
				Steps: []domain.Step{
					{ID: "fetch", Call: "svc.op2"},
				},
			},
		},
	}
	out := renderFlowOutline(f)
	assert.Contains(t, out, "  - each: foreach:inputs.ids max:50\n")
	assert.Contains(t, out, "    - create: svc.op\n", "a nested step is indented one level deeper than its block")
	assert.Contains(t, out, "  - page: repeat: until:steps.fetch.out.done max:10\n")
	assert.Contains(t, out, "    - fetch: svc.op2\n")
}

func TestTool_ListFlows(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "list_flows", map[string]any{})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[ListFlowsOutput](t, res.StructuredContent)
	require.Len(t, out.Flows, 1)
	assert.Equal(t, "rider-flow", out.Flows[0].ID)
	// The tier rides in both forms: structured (owner_kind) and the text line.
	assert.Equal(t, domain.FlowOwnerWorkspace, out.Flows[0].OwnerKind)
	assert.Contains(t, firstText(res), "rider-flow (1 steps, workspace)")
}

func TestTool_ListFlows_ShowsServiceTier(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: svc-flow\nsteps:\n  - id: a\n    call: rider-service.getRider\n",
		"scope":     "service", "service": "rider-service",
	})
	require.False(t, res.IsError, firstText(res))

	list := callTool(t, cs, "list_flows", map[string]any{})
	out := decodeStructured[ListFlowsOutput](t, list.StructuredContent)
	var svcFlow *domain.FlowSummary
	for i := range out.Flows {
		if out.Flows[i].ID == "svc-flow" {
			svcFlow = &out.Flows[i]
		}
	}
	require.NotNil(t, svcFlow)
	assert.Equal(t, domain.FlowOwnerService, svcFlow.OwnerKind)
	assert.Equal(t, "rider-service", svcFlow.OwnerID)
	assert.Contains(t, firstText(list), "svc-flow (1 steps, service:rider-service)")
}

func TestTool_GetFlow(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_flow", map[string]any{"id": "rider-flow"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetFlowOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-flow", out.Flow.ID)
	assert.Contains(t, out.YAML, "rider-flow")
}

func TestTool_ValidateFlow_Valid(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "validate_flow", map[string]any{"flow_yaml": "version: 1\nid: x\nsteps: []\n"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[map[string]any](t, res.StructuredContent)
	assert.Equal(t, true, out["valid"])
}

func TestTool_ValidateFlow_Invalid(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "validate_flow", map[string]any{"flow_yaml": "version: 1\nid: x\nINVALID\n"})
	require.False(t, res.IsError) // validate_flow itself never errors; it reports diagnostics
	out := decodeStructured[map[string]any](t, res.StructuredContent)
	assert.Equal(t, false, out["valid"])
	assert.Contains(t, firstText(res), "did you mean")
}

func TestTool_CreateFlow(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: new-flow\nsteps:\n  - id: a\n    call: rider-service.getRider\n  - id: b\n    call: rider-service.getRider\n",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, "new-flow", out.ID)
	// No scope: the local tier, this machine only, reported by the path an
	// agent can paste straight back into update_flow.
	assert.Equal(t, "local/flows/new-flow.flow.yaml", out.Path)
	assert.Equal(t, domain.FlowOwnerLocal, out.Tier)
	assert.Empty(t, out.Service)
	assert.Equal(t, 2, out.Steps)
	assert.Equal(t, 0, out.SetupSteps)
	assert.Equal(t, 0, out.TeardownSteps)
	assert.Equal(t, []string{"rider-service.getRider"}, out.Operations)
	assert.Positive(t, out.Bytes)
	assert.Empty(t, out.Diagnostics)

	text := firstText(res)
	assert.Contains(t, text, "created flow new-flow at local/flows/new-flow.flow.yaml (local tier; promote with rescope_flow when it works) in workspace test-workspace, 2 steps")
	// Item 4 (docs/feedback/2026-09-05-41-step-flow-session.md): the flow
	// document itself must never be echoed back.
	assert.NotContains(t, text, "call: rider-service.getRider")

	list := callTool(t, cs, "list_flows", map[string]any{})
	listOut := decodeStructured[ListFlowsOutput](t, list.StructuredContent)
	assert.Len(t, listOut.Flows, 2)
}

func TestTool_CreateFlow_SetupAndTeardownCounted(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: with-phases\n" +
			"setup:\n  - id: s1\n    call: rider-service.getRider\n" +
			"steps:\n  - id: m1\n    call: rider-service.getRider\n" +
			"teardown:\n  - id: t1\n    call: rider-service.getRider\n",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, 1, out.Steps)
	assert.Equal(t, 1, out.SetupSteps)
	assert.Equal(t, 1, out.TeardownSteps)
}

func TestTool_CreateFlow_CustomPath(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: nested\nsteps:\n  - id: a\n    call: rider-service.getRider\n",
		"path":      "sub/dir/nested.flow.yaml",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, "local/flows/sub/dir/nested.flow.yaml", out.Path, "path is relative to the tier's flows directory")
}

func TestTool_CreateFlow_WorkspaceScope(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: team-flow\nsteps:\n  - id: a\n    call: rider-service.getRider\n",
		"scope":     "workspace",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, domain.FlowOwnerWorkspace, out.Tier)
	assert.Equal(t, "flows/team-flow.flow.yaml", out.Path)
	assert.Contains(t, firstText(res), "(workspace tier: the team's repo)")
}

func TestTool_CreateFlow_ServiceScope(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: svc-flow\nsteps:\n  - id: a\n    call: rider-service.getRider\n",
		"scope":     "service", "service": "rider-service",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, domain.FlowOwnerService, out.Tier)
	assert.Equal(t, "rider-service", out.Service)
	assert.Contains(t, firstText(res), "service tier: rider-service/api/flows")
}

func TestTool_CreateFlow_ScopeArgsRejected(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	yaml := "version: 1\nid: x\nsteps:\n  - id: a\n    call: rider-service.getRider\n"

	res := callTool(t, cs, "create_flow", map[string]any{"flow_yaml": yaml, "scope": "service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "service scope requires service")

	res = callTool(t, cs, "create_flow", map[string]any{"flow_yaml": yaml, "scope": "global"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), `unknown scope "global"`)

	res = callTool(t, cs, "create_flow", map[string]any{"flow_yaml": yaml, "scope": "local", "service": "rider-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "service applies to scope service only")

	// None of those reached the engine.
	list := callTool(t, cs, "list_flows", map[string]any{})
	assert.Len(t, decodeStructured[ListFlowsOutput](t, list.StructuredContent).Flows, 1)
}

// --- rescope_flow ---

func TestTool_RescopeFlow_LocalToWorkspaceToService(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: ladder\nsteps:\n  - id: a\n    call: rider-service.getRider\n",
	})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "ladder", "scope": "workspace"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeFlowOutput](t, res.StructuredContent)
	assert.Equal(t, "ladder", out.ID)
	assert.Equal(t, domain.FlowOwnerWorkspace, out.Tier)
	assert.Equal(t, "local/flows/ladder.flow.yaml", out.OldPath)
	assert.Equal(t, "flows/ladder.flow.yaml", out.NewPath)
	assert.Equal(t, out.NewPath, out.Path)
	assert.Equal(t, 1, out.Steps)
	assert.Equal(t, []string{"rider-service.getRider"}, out.Operations)
	assert.Contains(t, firstText(res), "rescoped flow ladder to workspace tier: the team's repo (local/flows/ladder.flow.yaml -> flows/ladder.flow.yaml)")

	res = callTool(t, cs, "rescope_flow", map[string]any{"id": "ladder", "scope": "service", "service": "rider-service"})
	require.False(t, res.IsError, firstText(res))
	out = decodeStructured[RescopeFlowOutput](t, res.StructuredContent)
	assert.Equal(t, domain.FlowOwnerService, out.Tier)
	assert.Equal(t, "rider-service", out.Service)
	assert.Equal(t, "flows/ladder.flow.yaml", out.OldPath)
	assert.Equal(t, "rider-service/api/flows/ladder.flow.yaml", out.NewPath)

	got := callTool(t, cs, "get_flow", map[string]any{"id": "ladder"})
	gotOut := decodeStructured[GetFlowOutput](t, got.StructuredContent)
	assert.Equal(t, domain.FlowOwnerService, gotOut.Flow.OwnerKind)
	assert.Equal(t, "rider-service", gotOut.Flow.OwnerID)
}

func TestTool_RescopeFlow_Rejections(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	// scope is required in the schema, so the SDK refuses a call without
	// it before the handler runs; an explicit empty string gets past the
	// schema and is refused by the handler with the ladder in the hint.
	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "rider-flow"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "scope")

	res = callTool(t, cs, "rescope_flow", map[string]any{"id": "rider-flow", "scope": ""})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "requires scope")

	res = callTool(t, cs, "rescope_flow", map[string]any{"id": "rider-flow", "scope": "service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "service scope requires service")

	res = callTool(t, cs, "rescope_flow", map[string]any{"id": "no-such-flow", "scope": "local"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_FLOW_NOT_FOUND")

	// Nothing above moved the fixture flow.
	got := callTool(t, cs, "get_flow", map[string]any{"id": "rider-flow"})
	assert.Equal(t, domain.FlowOwnerWorkspace, decodeStructured[GetFlowOutput](t, got.StructuredContent).Flow.OwnerKind)
}

func TestTool_RescopeFlow_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteFlows = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "rider-flow", "scope": "local"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_flows")
}

func TestTool_CreateFlow_InvalidRejected(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{"flow_yaml": "INVALID"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_FLOW_INVALID")
}

func TestTool_CreateFlow_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteFlows = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{"flow_yaml": "version: 1\nid: x\nsteps: []\n"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_flows")
}

func TestTool_UpdateFlow(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "update_flow", map[string]any{
		"id": "rider-flow", "flow_yaml": "version: 1\nid: rider-flow\nname: Renamed\nsteps:\n  - id: a\n    call: rider-service.getRider\n",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, "rider-flow", out.ID)
	assert.Equal(t, 1, out.Steps)

	got := callTool(t, cs, "get_flow", map[string]any{"id": "rider-flow"})
	gotOut := decodeStructured[GetFlowOutput](t, got.StructuredContent)
	assert.Equal(t, "Renamed", gotOut.Flow.Name)
}

func TestTool_UpdateFlow_RequiresExactlyOneSource(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	res := callTool(t, cs, "update_flow", map[string]any{"id": "rider-flow"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "requires flow_yaml or path")

	res = callTool(t, cs, "update_flow", map[string]any{
		"id": "rider-flow", "flow_yaml": "version: 1\nid: rider-flow\nsteps: []\n", "path": "rider-flow.flow.yaml",
	})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "not both")
}

// withTempWorkspaceDir points eng's workspace at a real temp directory
// (newFixtureEngine's default "/workspace" is a fixture label, not a real
// path -- update_flow's path form needs an actual filesystem to read from).
func withTempWorkspaceDir(t *testing.T, eng *fakeEngine) string {
	t.Helper()
	tmpDir := t.TempDir()
	eng.st.mu.Lock()
	eng.st.ws.Dir = tmpDir
	eng.st.mu.Unlock()
	return tmpDir
}

func TestTool_UpdateFlow_FromPath(t *testing.T) {
	cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
	tmpDir := withTempWorkspaceDir(t, eng)

	flowsDir := filepath.Join(tmpDir, domain.FlowsDir)
	require.NoError(t, os.MkdirAll(flowsDir, 0o755))
	editedPath := filepath.Join(flowsDir, "rider-flow.flow.yaml")
	edited := "version: 1\nid: rider-flow\nname: Edited on disk\nsteps:\n  - id: a\n    call: rider-service.getRider\n"
	require.NoError(t, os.WriteFile(editedPath, []byte(edited), 0o644))

	res := callTool(t, cs, "update_flow", map[string]any{"id": "rider-flow", "path": "rider-flow.flow.yaml"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, "rider-flow", out.ID)

	got := callTool(t, cs, "get_flow", map[string]any{"id": "rider-flow"})
	gotOut := decodeStructured[GetFlowOutput](t, got.StructuredContent)
	assert.Equal(t, "Edited on disk", gotOut.Flow.Name)
}

// TestTool_UpdateFlow_FromTierPaths covers the two root-relative forms
// readWorkspaceFlowFile accepts on top of the flows-relative one: the path
// create_flow reports for a local-tier flow (local/flows/...) and the
// workspace one with its flows/ prefix, so an agent can paste either back
// without stripping a prefix.
func TestTool_UpdateFlow_FromTierPaths(t *testing.T) {
	cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
	tmpDir := withTempWorkspaceDir(t, eng)

	localDir := filepath.Join(tmpDir, domain.LocalDir, domain.FlowsDir)
	require.NoError(t, os.MkdirAll(localDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "rider-flow.flow.yaml"),
		[]byte("version: 1\nid: rider-flow\nname: Edited in local tier\nsteps:\n  - id: a\n    call: rider-service.getRider\n"), 0o644))

	res := callTool(t, cs, "update_flow", map[string]any{"id": "rider-flow", "path": "local/flows/rider-flow.flow.yaml"})
	require.False(t, res.IsError, firstText(res))
	got := callTool(t, cs, "get_flow", map[string]any{"id": "rider-flow"})
	assert.Equal(t, "Edited in local tier", decodeStructured[GetFlowOutput](t, got.StructuredContent).Flow.Name)

	flowsDir := filepath.Join(tmpDir, domain.FlowsDir)
	require.NoError(t, os.MkdirAll(flowsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(flowsDir, "rider-flow.flow.yaml"),
		[]byte("version: 1\nid: rider-flow\nname: Edited in workspace tier\nsteps:\n  - id: a\n    call: rider-service.getRider\n"), 0o644))

	res = callTool(t, cs, "update_flow", map[string]any{"id": "rider-flow", "path": "flows/rider-flow.flow.yaml"})
	require.False(t, res.IsError, firstText(res))
	got = callTool(t, cs, "get_flow", map[string]any{"id": "rider-flow"})
	assert.Equal(t, "Edited in workspace tier", decodeStructured[GetFlowOutput](t, got.StructuredContent).Flow.Name)

	// validate_flow shares the resolver.
	res = callTool(t, cs, "validate_flow", map[string]any{"path": "local/flows/rider-flow.flow.yaml"})
	require.False(t, res.IsError, firstText(res))
	assert.Equal(t, true, decodeStructured[map[string]any](t, res.StructuredContent)["valid"])
}

// TestTool_UpdateFlow_RootRelativeOutsideFlowsRejected: a file that exists
// at the workspace root but outside flows/ and local/flows/ is not readable
// through the root-relative form, so the tools stay confined to the two
// flow tiers.
func TestTool_UpdateFlow_RootRelativeOutsideFlowsRejected(t *testing.T) {
	cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
	tmpDir := withTempWorkspaceDir(t, eng)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "memories"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "memories", "rider-flow.flow.yaml"),
		[]byte("version: 1\nid: rider-flow\nsteps: []\n"), 0o644))

	res := callTool(t, cs, "update_flow", map[string]any{"id": "rider-flow", "path": "memories/rider-flow.flow.yaml"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "reading")

	// A flows-relative path that happens to start with "local/" still tries
	// <ws>/flows/local/... first and must not fall through to the root
	// form when that prefix is only "local", not "local/flows".
	res = callTool(t, cs, "update_flow", map[string]any{"id": "rider-flow", "path": "local/rider-flow.flow.yaml"})
	require.True(t, res.IsError)
}

func TestTool_UpdateFlow_PathEscapeRejected(t *testing.T) {
	cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
	tmpDir := withTempWorkspaceDir(t, eng)
	outside := filepath.Join(tmpDir, "outside.flow.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("version: 1\nid: rider-flow\nsteps: []\n"), 0o644))

	res := callTool(t, cs, "update_flow", map[string]any{"id": "rider-flow", "path": "../outside.flow.yaml"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), `must not contain ".."`)

	res = callTool(t, cs, "update_flow", map[string]any{"id": "rider-flow", "path": outside})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "absolute")
}

func TestTool_PatchFlow(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "patch_flow", map[string]any{
		"id": "rider-flow",
		"ops": []map[string]any{
			{"kind": "merge_step", "id": "get", "fields": map[string]any{"until": "status == 200"}},
		},
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, "rider-flow", out.ID)
	assert.Equal(t, 1, out.Steps)

	got := callTool(t, cs, "get_flow", map[string]any{"id": "rider-flow"})
	gotOut := decodeStructured[GetFlowOutput](t, got.StructuredContent)
	assert.Contains(t, gotOut.YAML, "until: status == 200")
	assert.Contains(t, gotOut.YAML, "call: rider-service.getRider", "untouched fields must survive the patch")
}

func TestTool_PatchFlow_NoOps(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "patch_flow", map[string]any{"id": "rider-flow", "ops": []map[string]any{}})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "at least one op")
}

func TestTool_PatchFlow_UnknownStepID(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "patch_flow", map[string]any{
		"id": "rider-flow",
		"ops": []map[string]any{
			{"kind": "remove_step", "id": "does-not-exist"},
		},
	})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "unknown step id")
}

func TestTool_PatchFlow_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteFlows = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "patch_flow", map[string]any{
		"id": "rider-flow", "ops": []map[string]any{{"kind": "remove_step", "id": "get"}},
	})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_flows")
}

func TestTool_RunFlow_ResumeOptionsReachRunner(t *testing.T) {
	cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "run_flow", map[string]any{
		"id": "rider-flow", "env": "staging",
		"resume_from": "run_1", "from_step": "get", "until_step": "get",
	})
	require.False(t, res.IsError, firstText(res))

	eng.st.mu.Lock()
	opts := eng.st.lastRunOptions
	eng.st.mu.Unlock()
	assert.Equal(t, "run_1", opts.ResumeFrom)
	assert.Equal(t, "get", opts.FromStep)
	assert.Equal(t, "get", opts.UntilStep)
}

func TestTool_RunFlow_GetOnly_NeedsOnlyExecuteRead(t *testing.T) {
	p := DefaultPermissions() // execute_read granted, execute_mutation not
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "run_flow", map[string]any{"id": "rider-flow", "env": "staging"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[RunView](t, res.StructuredContent)
	assert.Equal(t, "passed", out.Status)
	require.Len(t, out.Steps, 1)
	assert.Equal(t, "rider-service.getRider", out.Steps[0].Operation)
	assert.NotNil(t, out.Steps[0].Response)
}

func TestTool_GetRun_IncludeBodiesToggle(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	run := callTool(t, cs, "run_flow", map[string]any{"id": "rider-flow", "env": "staging"})
	require.False(t, run.IsError, firstText(run))
	runOut := decodeStructured[RunView](t, run.StructuredContent)

	withoutBodies := callTool(t, cs, "get_run", map[string]any{"id": runOut.ID})
	require.False(t, withoutBodies.IsError, firstText(withoutBodies))
	woOut := decodeStructured[RunView](t, withoutBodies.StructuredContent)
	require.Len(t, woOut.Steps, 1)
	assert.Nil(t, woOut.Steps[0].Response)
	assert.Nil(t, woOut.Steps[0].Request)

	withBodies := callTool(t, cs, "get_run", map[string]any{"id": runOut.ID, "include_bodies": true})
	require.False(t, withBodies.IsError, firstText(withBodies))
	wOut := decodeStructured[RunView](t, withBodies.StructuredContent)
	require.Len(t, wOut.Steps, 1)
	require.NotNil(t, wOut.Steps[0].Response)
	assert.Equal(t, 200, wOut.Steps[0].Response.Status)
}

func TestTool_GetRun_StepFilter(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	run := callTool(t, cs, "run_flow", map[string]any{"id": "rider-flow", "env": "staging"})
	runOut := decodeStructured[RunView](t, run.StructuredContent)

	res := callTool(t, cs, "get_run", map[string]any{"id": runOut.ID, "step": "get", "include_bodies": true})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RunView](t, res.StructuredContent)
	require.Len(t, out.Steps, 1)
	assert.Equal(t, "get", out.Steps[0].StepID)
}

func TestTool_GetRun_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_run", map[string]any{"id": "run_bogus"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_RUN_NOT_FOUND")
}

func TestTool_ExecuteAPI_GetOp(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{
		"id": "rider-service.getRider", "env": "staging", "params": map[string]any{"riderId": "r1"},
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RunView](t, res.StructuredContent)
	assert.Equal(t, "staging", out.Environment)
	require.Len(t, out.Steps, 1)
	assert.Equal(t, "rider-service.getRider", out.Steps[0].Operation)
}

func TestTool_GetContext(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_context", map[string]any{"intent": "a QCOM allocation test"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[map[string]any](t, res.StructuredContent)
	ops, _ := out["operations"].([]any)
	assert.NotEmpty(t, ops)
	memories, _ := out["memories"].([]any)
	assert.NotEmpty(t, memories)
}

func TestTool_GetContext_MemoriesStrippedWithoutPermission(t *testing.T) {
	p := DefaultPermissions()
	p.ReadMemories = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "get_context", map[string]any{"intent": "a QCOM allocation test"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[map[string]any](t, res.StructuredContent)
	assert.Nil(t, out["memories"])
}

// --- ship state (PLAN §7b) ---

func TestShipStateText(t *testing.T) {
	cases := map[string]string{
		domain.ShipUntracked: "not committed",
		domain.ShipModified:  "modified",
		domain.ShipUnpushed:  "committed, not pushed",
		domain.ShipShipped:   "shipped",
		"":                   "",
		"something-unknown":  "",
	}
	for in, want := range cases {
		assert.Equalf(t, want, shipStateText(in), "shipStateText(%q)", in)
	}
}

// TestTool_RescopeFlow_CommitNoteNotCommittedByDefault: a rescope to the
// workspace tier without commit says so, plainly, so an agent does not
// mistake a promotion for a share.
func TestTool_RescopeFlow_CommitNoteNotCommittedByDefault(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "rider-flow", "scope": "workspace"})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "; not committed")
}

// TestTool_RescopeFlow_CommitNoteCommitted: with commit=true the text says
// "committed" -- with a sha appended when one can be read from the
// workspace directory (the fake's default workspace dir, "/workspace", is
// not a real directory, so no sha is available and the note is bare).
func TestTool_RescopeFlow_CommitNoteCommitted(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "rider-flow", "scope": "workspace", "commit": true})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.Contains(t, text, "; committed")
	assert.NotContains(t, text, "; not committed")
}

// TestTool_RescopeFlow_CommitNoteReadsRealSHA: against a real git
// repository at the workspace directory, the commit note carries the
// actual HEAD short sha (commitShortSHA's one job) -- the rescope itself
// is still faked, so this only proves the text renders whatever HEAD is at
// the time, not that RescopeWith made that commit.
func TestTool_RescopeFlow_CommitNoteReadsRealSHA(t *testing.T) {
	cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
	tmpDir := withTempWorkspaceDir(t, eng)

	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "seed.txt"), []byte("x"), 0o644))
	runGit("add", "-A")
	runGit("-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-q", "-m", "seed")
	wantSHA := runGit("rev-parse", "--short", "HEAD")
	require.NotEmpty(t, wantSHA)

	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "rider-flow", "scope": "workspace", "commit": true})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "; committed "+wantSHA)
}

// TestTool_RescopeFlow_CommitNoteOnlyForWorkspaceTarget: the commit note is
// specific to a promotion to the workspace tier (the only target Commit
// applies to); moving to local or service never gets one, even if the
// caller passed commit=true.
func TestTool_RescopeFlow_CommitNoteOnlyForWorkspaceTarget(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "rider-flow", "scope": "local", "commit": true})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.NotContains(t, text, "committed")
	assert.NotContains(t, text, "not committed")
}

// TestTool_RescopeFlow_MessageInput: message rides through to
// RescopeOptions.Message without validation (the engine owns any further
// meaning); this only proves the input field exists and reaches a
// successful call.
func TestTool_RescopeFlow_MessageInput(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "rescope_flow", map[string]any{
		"id": "rider-flow", "scope": "workspace", "commit": true, "message": "Ship the rider flow",
	})
	require.False(t, res.IsError, firstText(res))
}

// --- commit_flow ---

// TestTool_CommitFlow_Success: committing the fixture's workspace-tier
// flow returns a lean FlowSaveResult with Shipped set to the fake's
// modelled post-commit state (unpushed), and the text says so.
func TestTool_CommitFlow_Success(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "commit_flow", map[string]any{"id": "rider-flow", "message": "Ship it"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, "rider-flow", out.ID)
	assert.Equal(t, domain.FlowOwnerWorkspace, out.Tier)
	assert.Equal(t, domain.ShipUnpushed, out.Shipped)

	text := firstText(res)
	assert.Contains(t, text, "committed")
	assert.Contains(t, text, "not pushed")
}

// TestTool_CommitFlow_NotFound.
func TestTool_CommitFlow_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "commit_flow", map[string]any{"id": "no-such-flow"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_FLOW_NOT_FOUND")
}

// TestTool_CommitFlow_RefusedForNonWorkspaceTier: commit_flow only ever
// applies to the workspace tier; a flow left at the (default) local tier
// is refused.
func TestTool_CommitFlow_RefusedForNonWorkspaceTier(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: local-only\nsteps:\n  - id: a\n    call: rider-service.getRider\n",
	})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "commit_flow", map[string]any{"id": "local-only"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "workspace-tier flow can be committed")
}

// TestTool_CommitFlow_PermissionDenied.
func TestTool_CommitFlow_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteFlows = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "commit_flow", map[string]any{"id": "rider-flow"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_flows")
}

// TestTool_CreateFlow_ShippedFieldPresent: FlowSaveResult carries a Shipped
// field (empty here: the fake engine never populates it, matching the real
// engine's own "empty for local/service, and for a workspace not in git"
// rule) -- this pins the field's presence and JSON tag rather than its
// value, which internal/engine/local's own tests cover against real git.
func TestTool_CreateFlow_ShippedFieldPresent(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: ship-check\nsteps:\n  - id: a\n    call: rider-service.getRider\n",
		"scope":     "workspace",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Empty(t, out.Shipped)
}
