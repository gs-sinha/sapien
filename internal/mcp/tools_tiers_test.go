package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// These tests drive the PLAN §7b tier additions to the memory and example
// tools: create_memory/create_example's tier-landed text, rescope_memory/
// rescope_example's optional tier param, the new commit_memory/
// commit_example tools, and the tier/shipped suffix on list/search text
// lines. wantToolNames in server_test.go already proves commit_memory and
// commit_example are registered and that no push tool exists.

// --- create_memory / create_example: tier-landed text ---

func TestTool_CreateMemory_TierLandedLine_Local(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{"text": "lands at the local tier by default"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, domain.TierLocal, out.Memory.Tier)
	assert.Contains(t, firstText(res),
		"local tier: this machine only; move it to the workspace tier with rescope_memory(tier=workspace) when it should reach the team")
}

func TestTool_CreateMemory_TierLandedLine_PersonalHasNone(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{"text": "personal has no tier", "scope": "personal"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, "", out.Memory.Tier)
	assert.NotContains(t, firstText(res), "tier:")
}

func TestTool_CreateExample_TierLandedLine_Local(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_example", map[string]any{
		"id": "tier-landed-example", "operation": "rider-service.getRider",
		"input": map[string]any{"riderId": "r1"},
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateExampleOutput](t, res.StructuredContent)
	assert.Equal(t, domain.TierLocal, out.Example.Tier)
	assert.Contains(t, firstText(res),
		"local tier: this machine only; move it to the workspace tier with rescope_example(tier=workspace) when it should reach the team")
}

// --- rescope_memory / rescope_example: optional tier param ---

func TestTool_RescopeMemory_WithTier_MovesFile(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "moves to the team tier"})
	require.False(t, created.IsError, firstText(created))
	id := decodeStructured[CreateMemoryOutput](t, created.StructuredContent).Memory.ID

	res := callTool(t, cs, "rescope_memory", map[string]any{"id": id, "scope": "workspace", "tier": "workspace"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, domain.TierWorkspace, out.Memory.Tier)
	assert.Equal(t, domain.ShipUntracked, out.Memory.Shipped)
	assert.Contains(t, firstText(res), "moved to the workspace tier")
}

// TestTool_RescopeMemory_WithoutTier_LeavesTierAlone: when tier is omitted,
// rescope_memory never calls Move -- only the scope changes.
func TestTool_RescopeMemory_WithoutTier_LeavesTierAlone(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "scope changes, tier does not"})
	require.False(t, created.IsError, firstText(created))
	id := decodeStructured[CreateMemoryOutput](t, created.StructuredContent).Memory.ID

	res := callTool(t, cs, "rescope_memory", map[string]any{"id": id, "scope": "service", "service": "rider-service"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeMemoryOutput](t, res.StructuredContent)
	// Move was never called, so Tier is whatever create_memory's own
	// default left it at (local): rescope_memory changed scope, not tier.
	assert.Equal(t, domain.TierLocal, out.Memory.Tier)
	assert.NotContains(t, firstText(res), "moved to the")
}

func TestTool_RescopeExample_WithTier_MovesFile(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_example", map[string]any{
		"id": "rescope-tier-example", "operation": "rider-service.getRider",
		"input": map[string]any{"riderId": "r1"},
	})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "rescope_example", map[string]any{
		"id": "rescope-tier-example", "scope": "workspace", "tier": "workspace",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeExampleOutput](t, res.StructuredContent)
	assert.Equal(t, domain.TierWorkspace, out.Example.Tier)
	assert.Equal(t, domain.ShipUntracked, out.Example.Shipped)
	assert.Contains(t, firstText(res), "moved to the workspace tier")
}

// --- commit_memory ---

func TestTool_CommitMemory(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "commit me over mcp"})
	require.False(t, created.IsError, firstText(created))
	createdOut := decodeStructured[CreateMemoryOutput](t, created.StructuredContent)

	res := callTool(t, cs, "commit_memory", map[string]any{"id": createdOut.Memory.ID, "message": "Ship it"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CommitMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, domain.ShipUnpushed, out.Memory.Shipped)
	assert.Contains(t, firstText(res), "committed "+createdOut.Memory.FilePath+"; not pushed")
}

func TestTool_CommitMemory_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "commit_memory", map[string]any{"id": "mem_bogus"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_MEMORY_NOT_FOUND")
}

func TestTool_CommitMemory_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteMemories = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "commit_memory", map[string]any{"id": "mem_seed1"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_memories")
}

// --- commit_example ---

func TestTool_CommitExample(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_example", map[string]any{
		"id": "commit-me-example", "operation": "rider-service.getRider",
		"input": map[string]any{"riderId": "r1"},
	})
	require.False(t, created.IsError, firstText(created))
	createdOut := decodeStructured[CreateExampleOutput](t, created.StructuredContent)

	res := callTool(t, cs, "commit_example", map[string]any{"id": "commit-me-example", "message": "Ship it"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CommitExampleOutput](t, res.StructuredContent)
	assert.Equal(t, domain.ShipUnpushed, out.Example.Shipped)
	assert.Contains(t, firstText(res), "committed "+createdOut.Example.Path+"; not pushed")
}

func TestTool_CommitExample_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "commit_example", map[string]any{"id": "no-such-example"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_EXAMPLE_NOT_FOUND")
}

func TestTool_CommitExample_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteExamples = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "commit_example", map[string]any{"id": "rider-get-example"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_examples")
}

// --- no push tool over MCP ---

func TestTool_NoPushToolRegistered(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	for _, tool := range res.Tools {
		assert.NotContains(t, tool.Name, "push", "pushing the workspace repository is the human's, not exposed over MCP")
	}
}

// --- list_examples / search_memories: tier + shipped suffix ---

func TestTool_ListExamples_ShippedSuffix(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_example", map[string]any{
		"id": "suffix-example", "operation": "rider-service.getRider",
		"input": map[string]any{"riderId": "r1"},
	})
	require.False(t, created.IsError, firstText(created))

	moved := callTool(t, cs, "rescope_example", map[string]any{"id": "suffix-example", "scope": "workspace", "tier": "workspace"})
	require.False(t, moved.IsError, firstText(moved))

	res := callTool(t, cs, "list_examples", map[string]any{"text": "suffix-example"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[ListExamplesOutput](t, res.StructuredContent)
	require.Len(t, out.Examples, 1)
	assert.Equal(t, domain.TierWorkspace, out.Examples[0].Tier)
	assert.Equal(t, domain.ShipUntracked, out.Examples[0].Shipped)
	assert.Contains(t, firstText(res), "[not committed]")
}

// TestTool_ListExamples_NoSuffixForLocalTier: a local-tier example (the
// create default) carries no ship state, so the text line has no bracketed
// suffix at all -- only a workspace-tier item with something to report
// does.
func TestTool_ListExamples_NoSuffixForLocalTier(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_example", map[string]any{
		"id": "no-suffix-example", "operation": "rider-service.getRider",
		"input": map[string]any{"riderId": "r1"},
	})
	require.False(t, created.IsError, firstText(created))

	res := callTool(t, cs, "list_examples", map[string]any{"text": "no-suffix-example"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[ListExamplesOutput](t, res.StructuredContent)
	require.Len(t, out.Examples, 1)
	assert.Equal(t, domain.TierLocal, out.Examples[0].Tier)
	assert.Equal(t, "", out.Examples[0].Shipped)
	// The operation id itself renders in brackets ("[rider-service.getRider]"),
	// so check specifically for the absence of a trailing ship-state suffix
	// rather than any "[" at all.
	assert.Contains(t, firstText(res), "- no-suffix-example [rider-service.getRider] workspace scope (unverified): \n")
}

func TestTool_SearchMemories_ShippedSuffix(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "search suffix sentinel text"})
	require.False(t, created.IsError, firstText(created))
	id := decodeStructured[CreateMemoryOutput](t, created.StructuredContent).Memory.ID

	moved := callTool(t, cs, "rescope_memory", map[string]any{"id": id, "scope": "workspace", "tier": "workspace"})
	require.False(t, moved.IsError, firstText(moved))
	committed := callTool(t, cs, "commit_memory", map[string]any{"id": id})
	require.False(t, committed.IsError, firstText(committed))

	res := callTool(t, cs, "search_memories", map[string]any{"query": "search suffix sentinel"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[SearchMemoriesOutput](t, res.StructuredContent)
	require.NotEmpty(t, out.Memories)
	assert.Equal(t, domain.ShipUnpushed, out.Memories[0].Memory.Shipped)
	assert.Contains(t, firstText(res), "[committed, not pushed]")
}
