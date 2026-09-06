package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTool_SearchMemories(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "search_memories", map[string]any{"query": "qcomSkill"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[SearchMemoriesOutput](t, res.StructuredContent)
	require.NotEmpty(t, out.Memories)
	assert.Equal(t, "mem_seed1", out.Memories[0].Memory.ID)
	assert.Contains(t, firstText(res), "mem_seed1")
}

func TestTool_SearchMemories_OperationFilter(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "search_memories", map[string]any{
		"query": "", "operation": "rider-service.getRider",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[SearchMemoriesOutput](t, res.StructuredContent)
	require.Len(t, out.Memories, 1)

	none := callTool(t, cs, "search_memories", map[string]any{"query": "", "operation": "no.such.op"})
	require.False(t, none.IsError, firstText(none))
	noneOut := decodeStructured[SearchMemoriesOutput](t, none.StructuredContent)
	assert.Empty(t, noneOut.Memories)
	assert.Contains(t, firstText(none), "no matches")
}

func TestTool_SearchMemories_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.ReadMemories = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "search_memories", map[string]any{"query": "qcomSkill"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "read_memories")
}

func TestTool_GetRelevantMemories(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_relevant_memories", map[string]any{
		"subjects": []map[string]any{{"operation": "rider-service.getRider"}},
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetRelevantMemoriesOutput](t, res.StructuredContent)
	require.NotEmpty(t, out.Memories)
	assert.Equal(t, "mem_seed1", out.Memories[0].Memory.ID)
}

func TestTool_GetRelevantMemories_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.ReadMemories = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "get_relevant_memories", map[string]any{
		"subjects": []map[string]any{{"operation": "rider-service.getRider"}},
	})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "read_memories")
}

func TestTool_CreateMemory_Provenance(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{
		"text":    "riders in staging never expire.",
		"subject": map[string]any{"operation": "rider-service.getRider"},
		"type":    "gotcha",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, "agent", out.Memory.Source.Kind)
	assert.Equal(t, "claude-code", out.Memory.Source.Client)
	assert.Equal(t, "workspace", string(out.Memory.Scope)) // default scope
	assert.Contains(t, firstText(res), "source agent{claude-code}")

	// The memory is now visible through search.
	found := callTool(t, cs, "search_memories", map[string]any{"query": "never expire"})
	foundOut := decodeStructured[SearchMemoriesOutput](t, found.StructuredContent)
	require.NotEmpty(t, foundOut.Memories)
}

func TestTool_CreateMemory_ExplicitScope(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{
		"text": "personal scratch note.", "scope": "personal",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, "personal", string(out.Memory.Scope))
}

func TestTool_CreateMemory_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteMemories = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{"text": "x"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_memories")
}

func TestTool_GetPromotionTarget(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_promotion_target", map[string]any{"memory_id": "mem_seed1"})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.Contains(t, text, "openapi")
	assert.Contains(t, text, "rider-service/openapi.yaml:42")
	assert.Contains(t, text, "current: Get a rider by id.")
}

func TestTool_GetPromotionTarget_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_promotion_target", map[string]any{"memory_id": "mem_bogus"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_MEMORY_NOT_FOUND")
}

func TestTool_GetPromotionTarget_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.ReadContracts = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "get_promotion_target", map[string]any{"memory_id": "mem_seed1"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "read_contracts")
}

// --- create_memory: hints (feedback items 1-2, 5, 7) ---
//
// newFixtureEngine's workspace Dir ("/workspace") never exists on disk, so
// every workspace-scoped create_memory call in this package hits the
// not-a-git-repo branch; that's exercised directly rather than papered
// over.

func TestTool_CreateMemory_NotGitRepoHint(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{"text": "workspace scope, no git history here"})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.Contains(t, text, "note:")
	assert.Contains(t, text, "is not a git repo, so workspace memories live only on this machine")
}

func TestTool_CreateMemory_PersonalScope_NoGitHint(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{"text": "personal scratch note", "scope": "personal"})
	require.False(t, res.IsError, firstText(res))
	text := firstText(res)
	assert.Contains(t, text, "stored: SQLite only")
	assert.NotContains(t, text, "is not a git repo")
	assert.NotContains(t, text, "concerns")
}

func TestTool_CreateMemory_ServiceSubjectHint(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{
		"text":    "allocate retries internally",
		"subject": map[string]any{"service": "rider-service"},
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateMemoryOutput](t, res.StructuredContent)
	text := firstText(res)
	assert.Contains(t, text, "concerns rider-service")
	assert.Contains(t, text, "rescope_memory(id=\""+out.Memory.ID+"\", scope=\"service\")")
}

func TestTool_CreateMemory_SimilarMemoryHint(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	first := callTool(t, cs, "create_memory", map[string]any{"text": "duplicate detection sentinel text"})
	require.False(t, first.IsError, firstText(first))
	firstOut := decodeStructured[CreateMemoryOutput](t, first.StructuredContent)

	second := callTool(t, cs, "create_memory", map[string]any{"text": "duplicate detection sentinel text"})
	require.False(t, second.IsError, firstText(second))
	text := firstText(second)
	assert.Contains(t, text, "similar: "+firstOut.Memory.ID)
	assert.Contains(t, text, "get_promotion_target(memory_id=\""+firstOut.Memory.ID+"\")")
}

// A similar match with a long, single-line body exercises firstLineOf's
// truncate-to-60-bytes branch (short bodies never hit it).
func TestTool_CreateMemory_SimilarMemoryHint_LongFirstLine_Truncated(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	longText := "duplicate detection sentinel text that goes on for a while past the sixty character cutoff so truncation definitely triggers"
	first := callTool(t, cs, "create_memory", map[string]any{"text": longText})
	require.False(t, first.IsError, firstText(first))
	firstOut := decodeStructured[CreateMemoryOutput](t, first.StructuredContent)

	second := callTool(t, cs, "create_memory", map[string]any{"text": "duplicate detection sentinel text"})
	require.False(t, second.IsError, firstText(second))
	text := firstText(second)
	assert.Contains(t, text, "similar: "+firstOut.Memory.ID)
	assert.Contains(t, text, longText[:60])
	assert.NotContains(t, text, longText[:61])
}

func TestTool_CreateMemory_PromotionReminder(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_memory", map[string]any{"text": "some new memory"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[CreateMemoryOutput](t, res.StructuredContent)
	assert.Contains(t, firstText(res), "when this stabilises, get_promotion_target(memory_id=\""+out.Memory.ID+"\") locates where it belongs")
}

// --- rescope_memory ---

func TestTool_RescopeMemory_ExistingServiceSubject(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	// mem_seed1 already carries subject.service = rider-service.
	res := callTool(t, cs, "rescope_memory", map[string]any{"id": "mem_seed1", "scope": "service"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, "service", string(out.Memory.Scope))
	assert.Equal(t, "rider-service", out.Memory.Subject.Service)
}

func TestTool_RescopeMemory_SetsServiceSubjectViaService(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "no service subject yet"})
	require.False(t, created.IsError, firstText(created))
	id := decodeStructured[CreateMemoryOutput](t, created.StructuredContent).Memory.ID

	res := callTool(t, cs, "rescope_memory", map[string]any{"id": id, "scope": "service", "service": "rider-service"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, "service", string(out.Memory.Scope))
	assert.Equal(t, "rider-service", out.Memory.Subject.Service)
	assert.Contains(t, firstText(res), "rescoped memory "+id+" to service scope")
}

func TestTool_RescopeMemory_ServiceScopeRequiresSubject(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	created := callTool(t, cs, "create_memory", map[string]any{"text": "still no service subject"})
	require.False(t, created.IsError, firstText(created))
	id := decodeStructured[CreateMemoryOutput](t, created.StructuredContent).Memory.ID

	res := callTool(t, cs, "rescope_memory", map[string]any{"id": id, "scope": "service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_INVALID")
}

func TestTool_RescopeMemory_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "rescope_memory", map[string]any{"id": "mem_bogus", "scope": "personal"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_MEMORY_NOT_FOUND")
}

func TestTool_RescopeMemory_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteMemories = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "rescope_memory", map[string]any{"id": "mem_seed1", "scope": "personal"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_memories")
}

func TestTool_RescopeMemory_ToPersonal(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "rescope_memory", map[string]any{"id": "mem_seed1", "scope": "personal"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeMemoryOutput](t, res.StructuredContent)
	assert.Equal(t, "personal", string(out.Memory.Scope))
	assert.Contains(t, firstText(res), "SQLite only -> SQLite only")
}
