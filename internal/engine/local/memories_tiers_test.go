package local

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// These tests cover PLAN §7b's tier ladder for memories: a workspace-scope
// memory starts in the local tier, Move carries it to the workspace tier
// (and back), Commit records a workspace-tier memory's file on its own, and
// List/Get/Search report Shipped for workspace-tier results. They reuse
// setupGitWorkspace (flows_ship_test.go) for a real git repository with a
// bare "origin", exactly like the flow-tier ship tests, and qcomMemory
// (memories_test.go) for a valid workspace-scope memory.

// TestMemories_Move_LocalToWorkspaceAndBack walks the whole ladder a
// memory can climb: created local (no Shipped), moved to workspace
// (untracked), moved back to local (Shipped empty again, file back under
// local/).
func TestMemories_Move_LocalToWorkspaceAndBack(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, created.Tier)
	assert.Equal(t, filepath.Join(ws.Dir, domain.LocalDir, domain.MemoriesDir), filepath.Dir(created.FilePath))
	assert.Empty(t, created.Shipped, "the local tier never reports a ship state")
	localPath := created.FilePath

	moved, err := l.Memories().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, moved.Tier)
	assert.Equal(t, filepath.Join(ws.Dir, domain.MemoriesDir), filepath.Dir(moved.FilePath))
	assert.Equal(t, domain.ShipUntracked, moved.Shipped, "just moved into memories/, nothing added to git yet")
	assert.FileExists(t, moved.FilePath)
	assert.NoFileExists(t, localPath, "the local copy must be gone after the move")

	back, err := l.Memories().Move(ctx, created.ID, domain.TierLocal)
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, back.Tier)
	assert.Equal(t, localPath, back.FilePath)
	assert.Empty(t, back.Shipped)
	assert.FileExists(t, back.FilePath)
	assert.NoFileExists(t, moved.FilePath, "the workspace-tier copy must be gone after moving back")
}

// Moving to the tier a memory is already in is a no-op that still returns
// the current item.
func TestMemories_Move_SameTierIsNoOp(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)

	same, err := l.Memories().Move(ctx, created.ID, domain.TierLocal)
	require.NoError(t, err)
	assert.Equal(t, created.FilePath, same.FilePath)
	assert.FileExists(t, same.FilePath)
}

// Move is refused for every scope but workspace, naming the scope.
func TestMemories_Move_RefusedForNonWorkspaceScope(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	personal, err := l.Memories().Create(ctx, domain.Memory{Scope: domain.ScopePersonal, Text: "just for me"})
	require.NoError(t, err)
	_, err = l.Memories().Move(ctx, personal.ID, domain.TierWorkspace)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	svc, err := l.Memories().Create(ctx, domain.Memory{Scope: domain.ScopeService, Subject: domain.Subject{Service: "order-service"}, Text: "service fact"})
	require.NoError(t, err)
	_, err = l.Memories().Move(ctx, svc.ID, domain.TierWorkspace)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// A flow-scoped memory is unaffected by the tier work: it keeps following
// its flow's own tier (memory.Locator.flowDir, untouched by this change)
// and Move refuses it outright, since "move" only ever applies to a
// workspace-scope memory.
func TestMemories_FlowScopedMemory_UnaffectedByMove(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("owns-a-memory"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	mem, err := l.Memories().Create(ctx, domain.Memory{
		Scope:   domain.ScopeFlow,
		Subject: domain.Subject{Flow: "owns-a-memory"},
		Text:    "flow-scoped fact",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, mem.Tier, "its local-tier flow's memory lives in the local tier, same as before")

	_, err = l.Memories().Move(ctx, mem.ID, domain.TierWorkspace)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	// Still exactly where it was: Move's refusal must not have touched it.
	got, err := l.Memories().Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, mem.FilePath, got.FilePath)
}

// An unknown tier is refused.
func TestMemories_Move_UnknownTierRefused(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)

	_, err = l.Memories().Move(ctx, created.ID, "bogus")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestMemories_Commit_UntrackedFileGetsDefaultMessage: a workspace-tier
// memory moved but never committed is committed standalone by Commit, with
// the default "Add memory ..." message, and the returned memory carries
// Shipped = unpushed.
func TestMemories_Commit_UntrackedFileGetsDefaultMessage(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)
	moved, err := l.Memories().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)

	committed, err := l.Memories().Commit(ctx, created.ID, "")
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, committed.Shipped)

	assert.Equal(t, "Add memory "+created.ID+" to the team workspace", runGit(t, ws.Dir, env, "log", "-1", "--format=%s"))
	wantRel, relErr := filepath.Rel(ws.Dir, moved.FilePath)
	require.NoError(t, relErr)
	assert.Equal(t, filepath.ToSlash(wantRel), runGit(t, ws.Dir, env, "log", "-1", "--name-only", "--format="),
		"the commit's tree must contain only the promoted file")

	got, err := l.Memories().Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, got.Shipped)
}

// A caller-supplied message overrides the default, and a second Commit on
// an edited (tracked, uncommitted) file uses "Update memory ...".
func TestMemories_Commit_CustomMessageAndModifiedFile(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)
	_, err = l.Memories().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)

	_, err = l.Memories().Commit(ctx, created.ID, "Ship the qcom memory")
	require.NoError(t, err)
	assert.Equal(t, "Ship the qcom memory", runGit(t, ws.Dir, env, "log", "-1", "--format=%s"))

	// Edit on disk (a plain Update), producing a tracked, uncommitted change.
	toUpdate, err := l.Memories().Get(ctx, created.ID)
	require.NoError(t, err)
	toUpdate.Text = "Updated: " + toUpdate.Text
	_, err = l.Memories().Update(ctx, *toUpdate)
	require.NoError(t, err)

	sum, err := l.Memories().Commit(ctx, created.ID, "")
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, sum.Shipped)
	assert.Equal(t, "Update memory "+created.ID, runGit(t, ws.Dir, env, "log", "-1", "--format=%s"))
}

// Once a commit leaves the file unpushed, Commit refuses a second call:
// there is nothing left for it to do, and Sapien never pushes.
func TestMemories_Commit_AlreadyCommittedRefused(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)
	_, err = l.Memories().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)
	_, err = l.Memories().Commit(ctx, created.ID, "")
	require.NoError(t, err)

	_, err = l.Memories().Commit(ctx, created.ID, "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// Commit only ever applies to the workspace tier.
func TestMemories_Commit_LocalTierRefused(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)

	_, err = l.Memories().Commit(ctx, created.ID, "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// A workspace that is not inside a git repository at all has nothing
// Commit can do.
func TestMemories_Commit_NonGitWorkspaceRefused(t *testing.T) {
	ws, _ := setupWorkspace(t) // a plain temp dir, never `git init`ed
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)

	_, err = l.Memories().Commit(ctx, created.ID, "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestMemories_List_ShippedTracksGitState covers the whole ladder List's
// Shipped shows for a workspace-tier memory -- untracked right after Move,
// unpushed once committed, shipped once pushed -- while a local-tier
// memory never carries a ship state.
func TestMemories_List_ShippedTracksGitState(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)
	moved, err := l.Memories().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)

	list, err := l.Memories().List(ctx, domain.MemoryQuery{})
	require.NoError(t, err)
	row := findMemory(t, list, created.ID)
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUntracked, row.Shipped)

	runGit(t, ws.Dir, env, "add", moved.FilePath)
	runGit(t, ws.Dir, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-q", "-m", "promote the memory")
	list, err = l.Memories().List(ctx, domain.MemoryQuery{})
	require.NoError(t, err)
	row = findMemory(t, list, created.ID)
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUnpushed, row.Shipped)

	runGit(t, ws.Dir, env, "push", "-q", "origin", "main")
	list, err = l.Memories().List(ctx, domain.MemoryQuery{})
	require.NoError(t, err)
	row = findMemory(t, list, created.ID)
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipShipped, row.Shipped)

	got, err := l.Memories().Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipShipped, got.Shipped, "Get reports Shipped the same way List does")
}

// findMemory returns id's row from a List result, or nil.
func findMemory(t *testing.T, list []domain.Memory, id string) *domain.Memory {
	t.Helper()
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}
