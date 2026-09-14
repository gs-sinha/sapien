package cli_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// These tests drive `example move` and `example commit` against the
// enginetest fake (setupFakeEngine), seeding via the fake's own Create
// then exercising Move/Commit; mirrors memory_tiers_test.go.

// --- example move ---

func TestExampleMove_ToTeam(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	ex, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "move-team", Operation: "order-service.createOrder", Scope: domain.ExampleScopeWorkspace,
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "move", ex.ID, "--to", "team", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, ex.ID, got["id"])
	assert.Equal(t, "workspace", got["tier"])

	updated, err := fake.Examples().Get(ctx, ex.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, updated.Tier)
	assert.Equal(t, domain.ShipUntracked, updated.Shipped)

	args := lastCall(fake, "Examples.Move").Args.(map[string]string)
	assert.Equal(t, ex.ID, args["id"])
	assert.Equal(t, "workspace", args["tier"])
}

func TestExampleMove_ToLocal_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	ex, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "move-local", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "move", ex.ID, "--to", "local")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "->")

	args := lastCall(fake, "Examples.Move").Args.(map[string]string)
	assert.Equal(t, "local", args["tier"])
}

func TestExampleMove_MissingTo(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "example", "move", "ex_x")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "--to")
}

func TestExampleMove_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "example", "move", "nope", "--to", "team", "--json")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "E_EXAMPLE_NOT_FOUND")
}

// --- example commit ---

func TestExampleCommit_ByID(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	ex, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "commit-example", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "commit", ex.ID, "-m", "Ship it")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "committed")
	assert.Contains(t, stdout, "not pushed")

	args := lastCall(fake, "Examples.Commit").Args.(map[string]string)
	assert.Equal(t, ex.ID, args["id"])
	assert.Equal(t, "Ship it", args["message"])
}

func TestExampleCommit_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	ex, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "commit-json", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "commit", ex.ID, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, ex.ID, got["id"])
	assert.Equal(t, "unpushed", got["shipped"])
}

func TestExampleCommit_RefusedForServiceTier(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	ex, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "svc-tier-example", Operation: "order-service.createOrder", Scope: domain.ExampleScopeService,
	})
	require.NoError(t, err)
	svc := *ex
	svc.Tier = domain.TierService
	_, err = fake.Examples().Update(ctx, svc)
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "example", "commit", "svc-tier-example")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "workspace-tier example can be committed")
}

func TestExampleCommit_RequiresIDOrAll(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "example", "commit")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "--all")

	_, stderr, code = run(t, "--workspace", dir, "example", "commit", "ex_x", "--all")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "not both")
}

func TestExampleCommit_All_CommitsUntrackedAndModifiedOnly(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	untracked, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "untracked-example", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)
	_, err = fake.Examples().Move(ctx, untracked.ID, domain.TierWorkspace)
	require.NoError(t, err)

	local, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "local-example", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "commit", "--all")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "committed")

	updated, err := fake.Examples().Get(ctx, untracked.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, updated.Shipped)

	for _, c := range fake.Calls {
		if c.Method != "Examples.Commit" {
			continue
		}
		args := c.Args.(map[string]string)
		assert.NotEqual(t, local.ID, args["id"], "a local-tier example must never be committed by --all")
	}
}

func TestExampleCommit_All_NothingToCommit(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	before := len(fake.Calls)
	stdout, stderr, code := run(t, "--workspace", dir, "example", "commit", "--all")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "nothing to commit")
	for _, c := range fake.Calls[before:] {
		assert.NotEqual(t, "Examples.Commit", c.Method)
	}
}

// --- example list / show: TIER and SHIPPED ---

func TestExampleList_ShowsTierAndShippedColumns(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	ex, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "tiered-example", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)
	_, err = fake.Examples().Move(ctx, ex.ID, domain.TierWorkspace)
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "TIER")
	assert.Contains(t, stdout, "SHIPPED")
	assert.Contains(t, stdout, "team")
	assert.Contains(t, stdout, "not committed")
}

func TestExampleShow_PrintsTierComment(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	ex, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "show-with-tier", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)
	_, err = fake.Examples().Move(ctx, ex.ID, domain.TierLocal)
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "show", ex.ID)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, splitLines(stdout)[0], "# tier: local")
}
