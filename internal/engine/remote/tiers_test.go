package remote_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// TestMemoryTiersRoundTrip proves the wire format of Memories().Move and
// Memories().Commit (PLAN §7b) matches what internal/server's handlers
// (handlers_memories.go) produce and consume: POST /v1/memories/{id}/move
// {tier} and POST /v1/memories/{id}/commit {message}.
func TestMemoryTiersRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	mem, err := h.rc.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "move and commit me",
	})
	require.NoError(t, err)

	t.Run("Move", func(t *testing.T) {
		got, err := h.rc.Memories().Move(ctx, mem.ID, domain.TierWorkspace)
		require.NoError(t, err)
		want, err := h.fake.Memories().Get(ctx, mem.ID)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, domain.TierWorkspace, got.Tier)
		assert.Equal(t, domain.ShipUntracked, got.Shipped)
	})

	t.Run("MoveNotFound", func(t *testing.T) {
		_, err := h.rc.Memories().Move(ctx, "mem_does_not_exist", domain.TierLocal)
		require.Error(t, err)
		assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))
	})

	t.Run("MoveBlankTierIsInvalid", func(t *testing.T) {
		_, err := h.rc.Memories().Move(ctx, mem.ID, "")
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("Commit", func(t *testing.T) {
		got, err := h.rc.Memories().Commit(ctx, mem.ID, "Ship it")
		require.NoError(t, err)
		want, err := h.fake.Memories().Get(ctx, mem.ID)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, domain.ShipUnpushed, got.Shipped)
	})

	t.Run("CommitNotFound", func(t *testing.T) {
		_, err := h.rc.Memories().Commit(ctx, "mem_does_not_exist", "")
		require.Error(t, err)
		assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))
	})
}

// TestExampleTiersRoundTrip mirrors TestMemoryTiersRoundTrip for
// Examples().Move and Examples().Commit, matching handlers_examples.go's
// POST /v1/examples/{id}/move and /commit.
func TestExampleTiersRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	ex, err := h.rc.Examples().Create(ctx, domain.SavedExample{
		ID: "tiers-round-trip-example", Operation: "order-service.createOrder", Scope: domain.ExampleScopeWorkspace,
	})
	require.NoError(t, err)

	t.Run("Move", func(t *testing.T) {
		got, err := h.rc.Examples().Move(ctx, ex.ID, domain.TierWorkspace)
		require.NoError(t, err)
		want, err := h.fake.Examples().Get(ctx, ex.ID)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, domain.TierWorkspace, got.Tier)
		assert.Equal(t, domain.ShipUntracked, got.Shipped)
	})

	t.Run("MoveNotFound", func(t *testing.T) {
		_, err := h.rc.Examples().Move(ctx, "no-such-example", domain.TierLocal)
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
	})

	t.Run("MoveBlankTierIsInvalid", func(t *testing.T) {
		_, err := h.rc.Examples().Move(ctx, ex.ID, "")
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("Commit", func(t *testing.T) {
		got, err := h.rc.Examples().Commit(ctx, ex.ID, "Ship the example")
		require.NoError(t, err)
		want, err := h.fake.Examples().Get(ctx, ex.ID)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, domain.ShipUnpushed, got.Shipped)
	})

	t.Run("CommitNotFound", func(t *testing.T) {
		_, err := h.rc.Examples().Commit(ctx, "no-such-example", "")
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
	})
}

// TestRepoPushRoundTrip proves the wire format of Repo().Push (PLAN §7b)
// matches handlers_repo.go's POST /v1/workspace/repo/push: a behind
// branch is refused with errs.Conflict, an ahead branch pushes and
// reports PushedCount.
func TestRepoPushRoundTrip(t *testing.T) {
	ctx := context.Background()

	t.Run("Behind", func(t *testing.T) {
		h := newHarness(t)
		h.fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 2})

		_, err := h.rc.Repo().Push(ctx)
		require.Error(t, err)
		assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	})

	t.Run("Ahead", func(t *testing.T) {
		h := newHarness(t)
		h.fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Ahead: 4})

		got, err := h.rc.Repo().Push(ctx)
		require.NoError(t, err)
		want, err := h.fake.Repo().Status(ctx)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.True(t, got.Pushed)
		assert.Equal(t, 4, got.PushedCount)
		assert.Equal(t, 0, got.Ahead)
	})

	t.Run("NothingToPush", func(t *testing.T) {
		h := newHarness(t)
		h.fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main"})

		got, err := h.rc.Repo().Push(ctx)
		require.NoError(t, err)
		assert.False(t, got.Pushed)
		assert.Equal(t, 0, got.PushedCount)
	})
}
