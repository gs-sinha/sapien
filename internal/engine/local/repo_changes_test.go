package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// These tests cover PLAN §34f item 1 (the Changes page): Repo().Changes,
// Repo().Diff and Repo().Commit, against a real git repository
// (setupGitWorkspace, from flows_ship_test.go).

// TestRepoChanges_ClassifiesFlowAndWorkspaceFiles: a new workspace-tier
// flow is classified kind=flow with its id/title; an edit to
// sapien.workspace.yaml (already committed by setupGitWorkspace's initial
// commit) is classified kind=workspace.
func TestRepoChanges_ClassifiesFlowAndWorkspaceFiles(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	fl, err := l.Flows().Create(ctx, tierFlowYAML("order-allocation"), "")
	require.NoError(t, err)
	require.Equal(t, "order-allocation", fl.ID)

	require.NoError(t, os.WriteFile(ws.File, append(mustReadFile(t, ws.File), []byte("\n")...), 0o644))

	out, err := l.Repo().Changes(ctx)
	require.NoError(t, err)
	require.True(t, out.Status.InGit)

	flowChange := findRepoChange(t, out.Files, "flows/order-allocation.flow.yaml")
	assert.Equal(t, domain.ChangeUntracked, flowChange.State)
	assert.Equal(t, domain.RepoKindFlow, flowChange.Kind)
	assert.Equal(t, "order-allocation", flowChange.ID)
	assert.Equal(t, "Tier test", flowChange.Title)

	wsChange := findRepoChange(t, out.Files, "sapien.workspace.yaml")
	assert.Equal(t, domain.ChangeModified, wsChange.State)
	assert.Equal(t, domain.RepoKindWorkspace, wsChange.Kind)
}

// TestRepoChanges_ClassifiesEnvironment: environments/local.yaml (written
// by workspace.Init, already committed) edited shows kind=environment.
func TestRepoChanges_ClassifiesEnvironment(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	envPath := filepath.Join(ws.Dir, "environments", "local.yaml")
	require.NoError(t, os.WriteFile(envPath, append(mustReadFile(t, envPath), []byte("\n")...), 0o644))

	out, err := l.Repo().Changes(ctx)
	require.NoError(t, err)
	change := findRepoChange(t, out.Files, "environments/local.yaml")
	assert.Equal(t, domain.RepoKindEnvironment, change.Kind)
}

// TestRepoChanges_ServicesArray_LocalNonGitSources: setupWorkspace
// registers three local-source services whose directories are not git
// checkouts at all -- Changes must still list them (mode local, no files,
// no error) rather than fail the whole listing.
func TestRepoChanges_ServicesArray_LocalNonGitSources(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	out, err := l.Repo().Changes(ctx)
	require.NoError(t, err)
	require.Len(t, out.Services, 3)
	names := map[string]domain.RepoServiceChanges{}
	for _, s := range out.Services {
		names[s.Name] = s
	}
	for _, n := range []string{"order-service", "allocation-service", "rider-service"} {
		s, ok := names[n]
		require.True(t, ok, "expected %s in the services array", n)
		assert.Equal(t, domain.BindingLocal, s.Mode)
		assert.Empty(t, s.Files, "a non-git checkout has nothing gitsrc can list")
	}
}

// TestRepoChanges_NotInGit: a plain (non-git) workspace reports an empty
// Changes, not an error.
func TestRepoChanges_NotInGit(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	out, err := l.Repo().Changes(context.Background())
	require.NoError(t, err)
	assert.False(t, out.Status.InGit)
	assert.Empty(t, out.Files)
}

// TestRepoDiff_UntrackedFlow_ReturnsContent.
func TestRepoDiff_UntrackedFlow_ReturnsContent(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().Create(ctx, tierFlowYAML("order-allocation"), "")
	require.NoError(t, err)

	diff, err := l.Repo().Diff(ctx, "flows/order-allocation.flow.yaml")
	require.NoError(t, err)
	assert.Equal(t, domain.ChangeUntracked, diff.State)
	assert.Contains(t, diff.Content, "id: order-allocation")
}

// TestRepoDiff_RejectsEscapingPath: the engine forwards gitsrc's own path
// safety refusal unchanged.
func TestRepoDiff_RejectsEscapingPath(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	_, err = l.Repo().Diff(context.Background(), "../../etc/passwd")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestRepoCommit_CommitsAndUpdatesShippedState_EmitsEvent: after a commit,
// Flows().List reports the new Shipped state immediately (computed live
// from git on every read, never cached), and EventWorkspaceRepo fires so
// the status bar refreshes.
func TestRepoCommit_CommitsAndUpdatesShippedState_EmitsEvent(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().Create(ctx, tierFlowYAML("order-allocation"), "")
	require.NoError(t, err)

	row := findFlow(t, l, "order-allocation")
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUntracked, row.Shipped)

	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	result, err := l.Repo().Commit(ctx, []string{"flows/order-allocation.flow.yaml"}, "Add flow order-allocation")
	require.NoError(t, err)
	assert.NotEmpty(t, result.Commit)
	assert.Equal(t, []string{"flows/order-allocation.flow.yaml"}, result.Committed)

	select {
	case ev := <-ch:
		assert.Equal(t, domain.EventWorkspaceRepo, ev.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a workspace.repo event")
	}

	row = findFlow(t, l, "order-allocation")
	require.NotNil(t, row)
	assert.Equal(t, domain.ShipUnpushed, row.Shipped, "committed here, not yet pushed -- computed live, not stale")
}

// TestRepoCommit_NoPathsIsInvalid.
func TestRepoCommit_NoPathsIsInvalid(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	_, err = l.Repo().Commit(context.Background(), nil, "message")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestRepoCommit_EmptyMessageIsInvalid.
func TestRepoCommit_EmptyMessageIsInvalid(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().Create(ctx, tierFlowYAML("order-allocation"), "")
	require.NoError(t, err)

	_, err = l.Repo().Commit(ctx, []string{"flows/order-allocation.flow.yaml"}, "  ")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestRepoCommit_IgnoredPathRefused.
func TestRepoCommit_IgnoredPathRefused(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(ws.Dir, ".sapien", "junk"), []byte("x"), 0o644))
	_ = env

	_, err = l.Repo().Commit(ctx, []string{".sapien/junk"}, "message")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// findRepoChange returns the entry in files whose Path == path, failing the
// test if there is none.
func findRepoChange(t *testing.T, files []domain.RepoFileChange, path string) domain.RepoFileChange {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no change for %q in %v", path, files)
	return domain.RepoFileChange{}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
