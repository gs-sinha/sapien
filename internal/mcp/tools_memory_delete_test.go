package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every write names the workspace it landed in, and delete_memory exists
// so a memory in the wrong place can go rather than be parked at personal
// scope (github.com/gs-sinha/sapien/discussions/1).
func TestCreateThenDeleteMemory_NamesWorkspace(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	res := callTool(t, cs, "create_memory", map[string]any{
		"text":  "riders with qcomSkill=false are never offered quick-commerce orders",
		"scope": "workspace",
		"type":  "invariant",
	})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "in workspace ")
	out := decodeStructured[CreateMemoryOutput](t, res.StructuredContent)
	require.NotEmpty(t, out.Memory.ID)

	res = callTool(t, cs, "delete_memory", map[string]any{"id": out.Memory.ID})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "deleted memory "+out.Memory.ID)

	res = callTool(t, cs, "search_memories", map[string]any{"query": "qcomSkill"})
	require.False(t, res.IsError, firstText(res))
	assert.NotContains(t, firstText(res), out.Memory.ID, "a deleted memory must not come back from search")
}

// delete_memory sits in the write-memories class like create_memory.
func TestDeleteMemory_RequiresWritePermission(t *testing.T) {
	perms := DefaultPermissions()
	perms.WriteMemories = false
	cs := newTestSession(t, Config{Default: perms}, "claude-code")
	res := callTool(t, cs, "delete_memory", map[string]any{"id": "mem_x"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
}
