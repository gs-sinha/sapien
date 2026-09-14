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

// These tests cover PLAN §7b's two additions to flow tiers: List reporting
// a workspace-tier flow's Shipped state from the workspace's own git
// repository, and RescopeWith{Commit: true} recording a promotion with one
// commit. Both need a real git repository, unlike every other flow tier
// test's plain temp directory, so this file drives git directly (never
// through Sapien) to set that up and to check what Sapien's own commit
// produced -- reusing hermeticGitEnv/runGit from git_test.go rather than
// redefining them.

// setupGitWorkspace is setupWorkspace with ws.Dir turned into a real git
// repository that has a bare "origin" tests can push to: RepoRoot,
// FileStates and CommitPaths only mean something inside a real repository.
// It returns the workspace, the hermetic git env used to set it up (reused
// by a test's own follow-up git commands), and the bare origin's path.
func setupGitWorkspace(t *testing.T) (*domain.Workspace, []string, string) {
	t.Helper()
	ws, _ := setupWorkspace(t)
	env := hermeticGitEnv(t)
	runGit(t, ws.Dir, env, "init", "-q", "-b", "main")
	runGit(t, ws.Dir, env, "add", "-A")
	runGit(t, ws.Dir, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-q", "-m", "init")

	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", env, "init", "-q", "--bare", "-b", "main", bareDir)
	runGit(t, ws.Dir, env, "remote", "add", "origin", bareDir)
	runGit(t, ws.Dir, env, "push", "-q", "-u", "origin", "main")
	return ws, env, bareDir
}

// TestFlows_List_ShippedTracksGitState covers the whole ladder List's
// Shipped is meant to show for a workspace-tier flow -- untracked right
// after promotion, unpushed once committed, shipped once pushed -- while a
// local-tier flow never carries a ship state at all.
func TestFlows_List_ShippedTracksGitState(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("shippable"), engine.CreateFlowOptions{})
	require.NoError(t, err)
	moved, err := l.Flows().Rescope(ctx, "shippable", domain.FlowOwnerWorkspace, "")
	require.NoError(t, err)

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("stays-local"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	row := findFlow(t, l, "shippable")
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUntracked, row.Shipped, "just moved into flows/, nothing added to git yet")

	localRow := findFlow(t, l, "stays-local")
	require.NotNil(t, localRow)
	assert.Empty(t, localRow.Shipped, "the local tier never reports a ship state")

	runGit(t, ws.Dir, env, "add", moved.Path)
	runGit(t, ws.Dir, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-q", "-m", "promote shippable")
	row = findFlow(t, l, "shippable")
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUnpushed, row.Shipped, "committed here, not yet on the upstream")

	runGit(t, ws.Dir, env, "push", "-q", "origin", "main")
	row = findFlow(t, l, "shippable")
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipShipped, row.Shipped)
}

// TestFlows_Rescope_LeavesPromotedFileUntracked pins that a plain Rescope
// (no Commit option) never touches git: the promoted file just sits in
// flows/ as an untracked addition until a human commits it.
func TestFlows_Rescope_LeavesPromotedFileUntracked(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("plain-promo"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	moved, err := l.Flows().Rescope(ctx, "plain-promo", domain.FlowOwnerWorkspace, "")
	require.NoError(t, err)
	assert.Equal(t, domain.FlowOwnerWorkspace, moved.OwnerKind)

	row := findFlow(t, l, "plain-promo")
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUntracked, row.Shipped, "Rescope alone never commits")
}

// TestFlows_RescopeWith_CommitsAndListsUnpushed: RescopeWith{Commit: true}
// to the workspace tier produces one commit whose tree contains only the
// promoted file, and the flow then lists as unpushed (it has not been
// pushed by anyone yet).
func TestFlows_RescopeWith_CommitsAndListsUnpushed(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("committed-promo"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	moved, err := l.Flows().RescopeWith(ctx, "committed-promo", domain.FlowOwnerWorkspace, "", engine.RescopeOptions{Commit: true})
	require.NoError(t, err)
	assert.Equal(t, domain.FlowOwnerWorkspace, moved.OwnerKind)

	changed := runGit(t, ws.Dir, env, "show", "--name-only", "--format=", "HEAD")
	assert.Equal(t, "flows/committed-promo.flow.yaml", changed, "the commit's tree must contain only the promoted file")
	assert.Equal(t, "Promote flow committed-promo to the team workspace", runGit(t, ws.Dir, env, "log", "-1", "--format=%s"))

	row := findFlow(t, l, "committed-promo")
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUnpushed, row.Shipped, "committed but nobody has pushed it yet")
}

// TestFlows_RescopeWith_CommitCustomMessage: opts.Message overrides the
// default commit message.
func TestFlows_RescopeWith_CommitCustomMessage(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("custom-msg"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	_, err = l.Flows().RescopeWith(ctx, "custom-msg", domain.FlowOwnerWorkspace, "", engine.RescopeOptions{Commit: true, Message: "Ship the custom-msg flow"})
	require.NoError(t, err)
	assert.Equal(t, "Ship the custom-msg flow", runGit(t, ws.Dir, env, "log", "-1", "--format=%s"))
}

// TestFlows_RescopeWith_CommitRefusedForNonWorkspaceTiers: Commit only
// makes sense for a promotion to the workspace tier -- a service
// repository is the developer's own, not Sapien's to commit into.
func TestFlows_RescopeWith_CommitRefusedForNonWorkspaceTiers(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().Create(ctx, tierFlowYAML("team-flow"), "")
	require.NoError(t, err)

	_, err = l.Flows().RescopeWith(ctx, "team-flow", domain.FlowOwnerLocal, "", engine.RescopeOptions{Commit: true})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	_, err = l.Flows().RescopeWith(ctx, "team-flow", domain.FlowOwnerService, "order-service", engine.RescopeOptions{Commit: true})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	// Neither refusal moved the file.
	row := findFlow(t, l, "team-flow")
	require.NotNil(t, row)
	assert.Equal(t, domain.FlowOwnerWorkspace, row.OwnerKind)
}

// TestFlows_RescopeWith_CommitRefusedWithoutGitRepo: a workspace that is
// not inside a git repository at all cannot have anything committed into
// it, and the refusal happens before the file moves.
func TestFlows_RescopeWith_CommitRefusedWithoutGitRepo(t *testing.T) {
	ws, _ := setupWorkspace(t) // a plain temp dir, never `git init`ed
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("no-repo"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	_, err = l.Flows().RescopeWith(ctx, "no-repo", domain.FlowOwnerWorkspace, "", engine.RescopeOptions{Commit: true})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	row := findFlow(t, l, "no-repo")
	require.NotNil(t, row)
	assert.Equal(t, domain.FlowOwnerLocal, row.OwnerKind, "refused before anything moved")
}
