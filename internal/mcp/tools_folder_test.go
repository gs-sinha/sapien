package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive PLAN §34f item 6's folder additions to the flow,
// memory, and example tools: `folder` on create_flow/create_memory/
// create_example (next to path), `folder` on rescope_flow/rescope_memory/
// rescope_example (changing folder while leaving scope/tier alone when
// they're omitted), and `folder` as a list filter on list_flows,
// list_examples, and search_memories.

// --- create_* : folder -------------------------------------------------

func TestTool_CreateFlow_WithFolder(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: folder-flow\nsteps:\n  - id: a\n    call: rider-service.getRider\n    input: { riderId: r1 }\n",
		"folder":    "team/onboarding",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, "team/onboarding", out.Folder)
}

func TestTool_CreateMemory_WithFolder(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{"text": "goes in a folder", "folder": "incidents/2026-09"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, "incidents/2026-09", out.Memory.Folder)
}

func TestTool_CreateExample_WithFolder(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_example", map[string]any{
		"id": "folder-example", "operation": "rider-service.getRider",
		"input": map[string]any{"riderId": "r1"}, "folder": "checkout",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateExampleOutput](t, res.StructuredContent)
	assert.Equal(t, "checkout", out.Example.Folder)
}

// create_example with both run_id and folder is refused: FromRun does not
// support folder placement yet.
func TestTool_CreateExample_RunIDWithFolder_Refused(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	run := callTool(t, cs, "run_flow", map[string]any{"id": "rider-flow", "env": "staging"})
	require.False(t, run.IsError, firstText(run))
	runID := decodeStructured[RunViewWithHints](t, run.StructuredContent).RunView.ID

	res := callTool(t, cs, "create_example", map[string]any{
		"id": "run-folder-example", "run_id": runID, "folder": "checkout",
	})
	assert.True(t, res.IsError, "expected create_example(run_id, folder) to be refused")
}

// --- rescope_* : folder, with scope/tier omitted ------------------------

func TestTool_RescopeFlow_FolderOnly_KeepsTier(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: rescope-folder-flow\nsteps:\n  - id: a\n    call: rider-service.getRider\n    input: { riderId: r1 }\n",
		"scope":     "workspace",
	})
	require.False(t, created.IsError, firstText(created))
	before := decodeStructured[FlowSaveResult](t, created.StructuredContent)
	require.Equal(t, "workspace", before.Tier)

	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "rescope-folder-flow", "folder": "promotions"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeFlowOutput](t, res.StructuredContent)
	assert.Equal(t, "promotions", out.Folder)
	assert.Equal(t, "workspace", out.Tier, "rescope_flow with folder only must leave the tier alone")
}

func TestTool_RescopeFlow_NeitherScopeNorFolder_Refused(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: rescope-nothing-flow\nsteps:\n  - id: a\n    call: rider-service.getRider\n    input: { riderId: r1 }\n",
	})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "rescope_flow", map[string]any{"id": "rescope-nothing-flow"})
	assert.True(t, res.IsError, "expected rescope_flow with neither scope nor folder to be refused")
}

func TestTool_RescopeMemory_FolderOnly_KeepsScopeAndTier(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "folder-only rescope"})
	require.False(t, created.IsError, firstText(created))
	id := decodeStructured[CreateMemoryOutput](t, created.StructuredContent).Memory.ID

	res := callTool(t, cs, "rescope_memory", map[string]any{"id": id, "folder": "notes"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, "notes", out.Memory.Folder)
	assert.Equal(t, "workspace", string(out.Memory.Scope))
}

func TestTool_RescopeMemory_NeitherScopeNorTierNorFolder_Refused(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "nothing to change"})
	require.False(t, created.IsError, firstText(created))
	id := decodeStructured[CreateMemoryOutput](t, created.StructuredContent).Memory.ID

	res := callTool(t, cs, "rescope_memory", map[string]any{"id": id})
	assert.True(t, res.IsError, "expected rescope_memory with nothing set to be refused")
}

func TestTool_RescopeExample_FolderOnly_KeepsScopeAndTier(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_example", map[string]any{
		"id": "rescope-folder-only-example", "operation": "rider-service.getRider",
		"input": map[string]any{"riderId": "r1"},
	})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "rescope_example", map[string]any{"id": "rescope-folder-only-example", "folder": "checkout"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeExampleOutput](t, res.StructuredContent)
	assert.Equal(t, "checkout", out.Example.Folder)
	assert.Equal(t, "workspace", string(out.Example.Scope))
}

// --- list_* / search_memories : folder filter ---------------------------

func TestTool_ListFlows_FolderFilter(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_flow", map[string]any{
		"flow_yaml": "version: 1\nid: list-folder-flow\nsteps:\n  - id: a\n    call: rider-service.getRider\n    input: { riderId: r1 }\n",
		"folder":    "a",
	})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "list_flows", map[string]any{"folder": "a"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[ListFlowsOutput](t, res.StructuredContent)
	require.Len(t, out.Flows, 1)
	assert.Equal(t, "list-folder-flow", out.Flows[0].ID)
	assert.Equal(t, "a", out.Flows[0].Folder)
}

func TestTool_ListExamples_FolderFilter(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_example", map[string]any{
		"id": "list-folder-example", "operation": "rider-service.getRider",
		"input": map[string]any{"riderId": "r1"}, "folder": "checkout",
	})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "list_examples", map[string]any{"folder": "checkout"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[ListExamplesOutput](t, res.StructuredContent)
	require.Len(t, out.Examples, 1)
	assert.Equal(t, "list-folder-example", out.Examples[0].ID)
	assert.Equal(t, "checkout", out.Examples[0].Folder)
}

func TestTool_SearchMemories_FolderFilter(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "no matching words here", "folder": "qcomallocation"})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "search_memories", map[string]any{"query": "here", "folder": "qcomallocation"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[SearchMemoriesOutput](t, res.StructuredContent)
	require.Len(t, out.Memories, 1)
	assert.Equal(t, "qcomallocation", out.Memories[0].Memory.Folder)
}
