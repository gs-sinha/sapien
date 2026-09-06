package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

func TestTool_ListFlows(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "list_flows", map[string]any{})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[ListFlowsOutput](t, res.StructuredContent)
	require.Len(t, out.Flows, 1)
	assert.Equal(t, "rider-flow", out.Flows[0].ID)
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
	assert.Equal(t, "new-flow.flow.yaml", out.Path)
	assert.Equal(t, 2, out.Steps)
	assert.Equal(t, 0, out.SetupSteps)
	assert.Equal(t, 0, out.TeardownSteps)
	assert.Equal(t, []string{"rider-service.getRider"}, out.Operations)
	assert.Positive(t, out.Bytes)
	assert.Empty(t, out.Diagnostics)

	text := firstText(res)
	assert.Contains(t, text, "created flow new-flow at new-flow.flow.yaml, 2 steps")
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
	assert.Equal(t, "sub/dir/nested.flow.yaml", out.Path)
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
