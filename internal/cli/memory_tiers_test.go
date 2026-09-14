package cli_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// These tests drive `memory move` and `memory commit` against the
// enginetest fake (setupFakeEngine), seeding via the fake's own Create
// then exercising Move/Commit -- the fake re-stamps Tier/Shipped exactly
// as internal/engine/enginetest/memories.go documents, so these tests
// prove what the CLI sends and how it reports the answer, not the
// on-disk tier layout (internal/engine/local's own tests cover that).

// --- memory move ---

func TestMemoryMove_ToTeam(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "move to team",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "move", mem.ID, "--to", "team", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, mem.ID, got["id"])
	assert.Equal(t, "workspace", got["tier"])

	updated, err := fake.Memories().Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, updated.Tier)
	assert.Equal(t, domain.ShipUntracked, updated.Shipped)

	args := lastCall(fake, "Memories.Move").Args.(map[string]string)
	assert.Equal(t, mem.ID, args["id"])
	assert.Equal(t, "workspace", args["tier"])
}

func TestMemoryMove_ToLocal_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "move to local",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "move", mem.ID, "--to", "local")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "->")

	args := lastCall(fake, "Memories.Move").Args.(map[string]string)
	assert.Equal(t, "local", args["tier"])
}

func TestMemoryMove_MissingTo(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "memory", "move", "mem_x")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "--to")
}

func TestMemoryMove_UnknownTier(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "memory", "move", "mem_x", "--to", "cloud")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, `unknown tier "cloud"`)
}

func TestMemoryMove_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "memory", "move", "mem_nope", "--to", "team", "--json")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "E_MEMORY_NOT_FOUND")
}

// --- memory commit ---

func TestMemoryCommit_ByID(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "commit this one",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "commit", mem.ID, "-m", "Ship it")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "committed")
	assert.Contains(t, stdout, "not pushed")

	args := lastCall(fake, "Memories.Commit").Args.(map[string]string)
	assert.Equal(t, mem.ID, args["id"])
	assert.Equal(t, "Ship it", args["message"])
}

func TestMemoryCommit_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "commit as json",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "commit", mem.ID, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, mem.ID, got["id"])
	assert.Equal(t, "unpushed", got["shipped"])
}

func TestMemoryCommit_RefusedForNonWorkspaceTier(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeService, Subject: domain.Subject{Service: "order-service"},
		Source: domain.MemorySource{Kind: "user"}, Text: "service scope",
	})
	require.NoError(t, err)
	svc := *mem
	svc.Tier = domain.TierService
	moved, err := fake.Memories().Update(ctx, svc)
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "memory", "commit", moved.ID)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "workspace-tier memory can be committed")
}

func TestMemoryCommit_RequiresIDOrAll(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "memory", "commit")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "--all")

	_, stderr, code = run(t, "--workspace", dir, "memory", "commit", "mem_x", "--all")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "not both")
}

func TestMemoryCommit_All_CommitsUntrackedAndModifiedOnly(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	untracked, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "untracked memory",
	})
	require.NoError(t, err)
	_, err = fake.Memories().Move(ctx, untracked.ID, domain.TierWorkspace)
	require.NoError(t, err)

	local, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "still local, not eligible",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "commit", "--all")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "committed")

	updated, err := fake.Memories().Get(ctx, untracked.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, updated.Shipped)

	for _, c := range fake.Calls {
		if c.Method != "Memories.Commit" {
			continue
		}
		args := c.Args.(map[string]string)
		assert.NotEqual(t, local.ID, args["id"], "a local-tier memory must never be committed by --all")
	}
}

func TestMemoryCommit_All_NothingToCommit(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	before := len(fake.Calls)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "commit", "--all")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "nothing to commit")
	for _, c := range fake.Calls[before:] {
		assert.NotEqual(t, "Memories.Commit", c.Method)
	}
}

// --- memory list / show: TIER and SHIPPED ---

func TestMemoryList_ShowsTierAndShippedColumns(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "tiered memory",
	})
	require.NoError(t, err)
	_, err = fake.Memories().Move(ctx, mem.ID, domain.TierWorkspace)
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "TIER")
	assert.Contains(t, stdout, "SHIPPED")
	assert.Contains(t, stdout, "team")
	assert.Contains(t, stdout, "not committed")
}

func TestMemoryShow_PrintsTierComment(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "shown with tier",
	})
	require.NoError(t, err)
	_, err = fake.Memories().Move(ctx, mem.ID, domain.TierLocal)
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "show", mem.ID)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, splitLines(stdout)[0], "# tier: local")
}
