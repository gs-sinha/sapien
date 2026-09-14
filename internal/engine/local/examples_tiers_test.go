package local

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// These tests are examples_test.go's counterpart to memories_tiers_test.go:
// PLAN §7b's tier ladder, but for saved examples. They reuse
// setupGitWorkspace (flows_ship_test.go) for a real git repository with a
// bare "origin", and validCreateOrderExample (examples_test.go) for a
// valid workspace-scope example.

// findExample returns id's row from a List result, or nil.
func findExample(t *testing.T, list []domain.SavedExample, id string) *domain.SavedExample {
	t.Helper()
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// TestExamples_Move_LocalToWorkspaceAndBack walks the whole ladder an
// example can climb: created local (no Shipped), moved to workspace
// (untracked), moved back to local (Shipped empty again, file back under
// local/).
func TestExamples_Move_LocalToWorkspaceAndBack(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("qcom-order"))
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, created.Tier)
	assert.Equal(t, filepath.Join(ws.Dir, domain.LocalDir, "examples"), filepath.Dir(created.Path))
	assert.Empty(t, created.Shipped, "the local tier never reports a ship state")
	localPath := created.Path

	moved, err := l.Examples().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, moved.Tier)
	assert.Equal(t, filepath.Join(ws.Dir, "examples"), filepath.Dir(moved.Path))
	assert.Equal(t, domain.ShipUntracked, moved.Shipped, "just moved into examples/, nothing added to git yet")
	assert.FileExists(t, moved.Path)
	assert.NoFileExists(t, localPath, "the local copy must be gone after the move")

	back, err := l.Examples().Move(ctx, created.ID, domain.TierLocal)
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, back.Tier)
	assert.Equal(t, localPath, back.Path)
	assert.Empty(t, back.Shipped)
	assert.FileExists(t, back.Path)
	assert.NoFileExists(t, moved.Path, "the workspace-tier copy must be gone after moving back")
}

// Moving to the tier an example is already in is a no-op that still
// returns the current item.
func TestExamples_Move_SameTierIsNoOp(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("no-op-move"))
	require.NoError(t, err)

	same, err := l.Examples().Move(ctx, created.ID, domain.TierLocal)
	require.NoError(t, err)
	assert.Equal(t, created.Path, same.Path)
	assert.FileExists(t, same.Path)
}

// Move is refused for service scope, naming the scope.
func TestExamples_Move_RefusedForServiceScope(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	ex := validCreateOrderExample("svc-scoped-move")
	ex.Scope = domain.ExampleScopeService
	created, err := l.Examples().Create(ctx, ex)
	require.NoError(t, err)

	_, err = l.Examples().Move(ctx, created.ID, domain.TierWorkspace)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// An unknown tier is refused.
func TestExamples_Move_UnknownTierRefused(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("bogus-tier-move"))
	require.NoError(t, err)

	_, err = l.Examples().Move(ctx, created.ID, "bogus")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestExamples_Commit_UntrackedFileGetsDefaultMessage: a workspace-tier
// example moved but never committed is committed standalone by Commit,
// with the default "Add example ..." message, and the returned example
// carries Shipped = unpushed.
func TestExamples_Commit_UntrackedFileGetsDefaultMessage(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("standalone-example"))
	require.NoError(t, err)
	moved, err := l.Examples().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)

	committed, err := l.Examples().Commit(ctx, created.ID, "")
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, committed.Shipped)

	assert.Equal(t, "Add example "+created.ID+" to the team workspace", runGit(t, ws.Dir, env, "log", "-1", "--format=%s"))
	wantRel, relErr := filepath.Rel(ws.Dir, moved.Path)
	require.NoError(t, relErr)
	assert.Equal(t, filepath.ToSlash(wantRel), runGit(t, ws.Dir, env, "log", "-1", "--name-only", "--format="),
		"the commit's tree must contain only the promoted file")

	got, err := l.Examples().Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, got.Shipped)
}

// A caller-supplied message overrides the default, and a second Commit on
// an edited (tracked, uncommitted) file uses "Update example ...".
func TestExamples_Commit_CustomMessageAndModifiedFile(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("editable-example"))
	require.NoError(t, err)
	_, err = l.Examples().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)

	_, err = l.Examples().Commit(ctx, created.ID, "Ship the editable example")
	require.NoError(t, err)
	assert.Equal(t, "Ship the editable example", runGit(t, ws.Dir, env, "log", "-1", "--format=%s"))

	toUpdate, err := l.Examples().Get(ctx, created.ID)
	require.NoError(t, err)
	toUpdate.Description = "updated description"
	_, err = l.Examples().Update(ctx, *toUpdate)
	require.NoError(t, err)

	sum, err := l.Examples().Commit(ctx, created.ID, "")
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, sum.Shipped)
	assert.Equal(t, "Update example "+created.ID, runGit(t, ws.Dir, env, "log", "-1", "--format=%s"))
}

// Once a commit leaves the file unpushed, Commit refuses a second call.
func TestExamples_Commit_AlreadyCommittedRefused(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("once-example"))
	require.NoError(t, err)
	_, err = l.Examples().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)
	_, err = l.Examples().Commit(ctx, created.ID, "")
	require.NoError(t, err)

	_, err = l.Examples().Commit(ctx, created.ID, "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// Commit only ever applies to the workspace tier.
func TestExamples_Commit_LocalTierRefused(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("local-tier-example"))
	require.NoError(t, err)

	_, err = l.Examples().Commit(ctx, created.ID, "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// A workspace that is not inside a git repository at all has nothing
// Commit can do.
func TestExamples_Commit_NonGitWorkspaceRefused(t *testing.T) {
	l := openExamplesEngine(t) // setupWorkspace, never `git init`ed
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("no-repo-example"))
	require.NoError(t, err)

	_, err = l.Examples().Commit(ctx, created.ID, "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestExamples_List_ShippedTracksGitState covers the whole ladder List's
// Shipped shows for a workspace-tier example -- untracked right after
// Move, unpushed once committed, shipped once pushed -- while a
// local-tier example never carries a ship state.
func TestExamples_List_ShippedTracksGitState(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("listed-example"))
	require.NoError(t, err)
	moved, err := l.Examples().Move(ctx, created.ID, domain.TierWorkspace)
	require.NoError(t, err)

	list, err := l.Examples().List(ctx, domain.ExampleQuery{})
	require.NoError(t, err)
	row := findExample(t, list, created.ID)
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUntracked, row.Shipped)

	runGit(t, ws.Dir, env, "add", moved.Path)
	runGit(t, ws.Dir, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-q", "-m", "promote the example")
	list, err = l.Examples().List(ctx, domain.ExampleQuery{})
	require.NoError(t, err)
	row = findExample(t, list, created.ID)
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUnpushed, row.Shipped)

	runGit(t, ws.Dir, env, "push", "-q", "origin", "main")
	list, err = l.Examples().List(ctx, domain.ExampleQuery{})
	require.NoError(t, err)
	row = findExample(t, list, created.ID)
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipShipped, row.Shipped)

	got, err := l.Examples().Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShipShipped, got.Shipped, "Get reports Shipped the same way List does")
}
