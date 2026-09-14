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

// These tests cover PLAN §7b's other addition: fetching the workspace's own
// git repository on the daemon's tick (read-only), and pulling it only on
// request. setupGitWorkspace (flows_ship_test.go) gives every test here a
// real git repository, with a bare "origin" a second clone can push to, so
// "a teammate pushed a flow" is exactly what it looks like in practice.

// pushTeammateFlow simulates a teammate pushing a new workspace-tier flow
// straight to bareDir -- never through l, which is the point: the change
// must become visible only once l's own Fetch/Pull/Sync says so.
func pushTeammateFlow(t *testing.T, bareDir string, env []string, id string) string {
	t.Helper()
	return commitAndPush(t, bareDir, env,
		map[string]string{"flows/" + id + ".flow.yaml": tierFlowYAML(id)},
		"add "+id+" flow")
}

func TestRepo_Fetch_ShowsBehindAndEmits(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	pushTeammateFlow(t, bareDir, env, "teammate-fetch")

	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	status, err := l.Repo().Fetch(ctx)
	require.NoError(t, err)
	assert.True(t, status.InGit)
	assert.Equal(t, 1, status.Behind)
	assert.Zero(t, status.Ahead)
	assert.Empty(t, status.FetchError)
	assert.False(t, status.FetchedAt.IsZero())

	select {
	case ev := <-ch:
		require.Equal(t, domain.EventWorkspaceRepo, ev.Type)
		payload, ok := ev.Payload.(*domain.RepoStatus)
		require.True(t, ok, "payload should be a *domain.RepoStatus")
		assert.Equal(t, 1, payload.Behind)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a workspace.repo event")
	}

	// Fetch never touches the working tree: the pushed flow is known about
	// (Behind) but not yet on disk here.
	row := findFlow(t, l, "teammate-fetch")
	assert.Nil(t, row, "Fetch must not pull")
}

// TestRepo_Pull_BringsFileAndReindexesFlows: Pull fast-forwards and the
// pulled file is immediately visible through Flows().List, without a
// separate reindex call.
func TestRepo_Pull_BringsFileAndReindexesFlows(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	pushTeammateFlow(t, bareDir, env, "teammate-flow")

	_, err = l.Repo().Fetch(ctx)
	require.NoError(t, err)

	status, err := l.Repo().Pull(ctx)
	require.NoError(t, err)
	assert.True(t, status.Pulled)
	assert.Equal(t, 1, status.PulledCount)
	assert.Zero(t, status.Behind)

	row := findFlow(t, l, "teammate-flow")
	require.NotNil(t, row, "the pulled flow should be visible in the workspace tier")
	assert.Equal(t, domain.FlowOwnerWorkspace, row.OwnerKind)
}

// TestRepo_Pull_NothingToDo: Behind == 0 is success, not an error or a
// skip -- Pulled stays false and there is nothing to report.
func TestRepo_Pull_NothingToDo(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	status, err := l.Repo().Pull(ctx)
	require.NoError(t, err)
	assert.False(t, status.Pulled)
	assert.Empty(t, status.Skipped)
}

// TestRepo_Pull_UntrackedFileRefusedConflictTreeUntouched: an untracked
// file blocks Pull with errs.Conflict, and the checkout -- HEAD and the
// untracked file both -- is left exactly as it was.
func TestRepo_Pull_UntrackedFileRefusedConflictTreeUntouched(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	pushTeammateFlow(t, bareDir, env, "teammate-flow")
	_, err = l.Repo().Fetch(ctx)
	require.NoError(t, err)

	untracked := filepath.Join(ws.Dir, "scratch.txt")
	require.NoError(t, os.WriteFile(untracked, []byte("wip\n"), 0o644))
	beforeHead := runGit(t, ws.Dir, env, "rev-parse", "HEAD")

	_, err = l.Repo().Pull(ctx)
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))

	afterHead := runGit(t, ws.Dir, env, "rev-parse", "HEAD")
	assert.Equal(t, beforeHead, afterHead, "a refused pull must never move HEAD")
	assert.FileExists(t, untracked, "the untracked file must be left alone")

	row := findFlow(t, l, "teammate-flow")
	assert.Nil(t, row, "not pulled, so still invisible")
}

// TestRepo_Pull_NoUpstreamRefused: a branch with no upstream at all is
// refused with the exact "branch has no upstream" message the UI shows.
func TestRepo_Pull_NoUpstreamRefused(t *testing.T) {
	ws, env, _ := setupGitWorkspace(t)
	runGit(t, ws.Dir, env, "checkout", "-q", "-b", "no-upstream")

	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Repo().Pull(ctx)
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "branch has no upstream")
}

// TestRepo_NotInGit_StatusSyncPull covers all three of RepoAPI's reactions
// to a workspace that was never made into a git repository at all.
func TestRepo_NotInGit_StatusSyncPull(t *testing.T) {
	ws, _ := setupWorkspace(t) // a plain temp dir, never `git init`ed
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	status, err := l.Repo().Status(ctx)
	require.NoError(t, err)
	assert.False(t, status.InGit)

	syncStatus, err := l.Repo().Sync(ctx)
	require.NoError(t, err)
	assert.Equal(t, "not a git repository", syncStatus.Skipped)

	_, err = l.Repo().Pull(ctx)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestRepo_Sync_DirtyTreeSkippedWithoutError: "Sync all" never fails just
// because the developer has uncommitted work; it reports why in Skipped.
func TestRepo_Sync_DirtyTreeSkippedWithoutError(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(ws.Dir, "scratch.txt"), []byte("wip\n"), 0o644))

	status, err := l.Repo().Sync(ctx)
	require.NoError(t, err)
	assert.Equal(t, "uncommitted changes (1 files)", status.Skipped)
	assert.False(t, status.Pulled)
}

// TestRepo_Sync_CleanBehindTreePulls: a clean tree that is behind gets
// pulled by Sync, same as an explicit Pull would, with Skipped left empty.
func TestRepo_Sync_CleanBehindTreePulls(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	pushTeammateFlow(t, bareDir, env, "teammate-flow")

	status, err := l.Repo().Sync(ctx)
	require.NoError(t, err)
	assert.Empty(t, status.Skipped)
	assert.True(t, status.Pulled)
	assert.Equal(t, 1, status.PulledCount)

	row := findFlow(t, l, "teammate-flow")
	require.NotNil(t, row)
}

// TestRepo_Sync_DivergedSkipped: a local, unpushed commit that diverges
// from a teammate's push cannot be fast-forwarded; Sync reports it as
// Skipped "diverged" rather than failing.
func TestRepo_Sync_DivergedSkipped(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	pushTeammateFlow(t, bareDir, env, "teammate-flow")
	require.NoError(t, os.MkdirAll(filepath.Join(ws.Dir, "flows"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws.Dir, "flows", "local.flow.yaml"), []byte(tierFlowYAML("local-flow")), 0o644))
	runGit(t, ws.Dir, env, "add", "-A")
	runGit(t, ws.Dir, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-q", "-m", "local work")

	status, err := l.Repo().Sync(ctx)
	require.NoError(t, err)
	assert.Equal(t, "diverged", status.Skipped)
}

// TestServicesSync_All_PullsCleanBehindWorkspaceRepo: "Sync all" (empty
// name) pulls the workspace repository as one of its steps, without
// changing Services().Sync's return type or failing it.
func TestServicesSync_All_PullsCleanBehindWorkspaceRepo(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	pushTeammateFlow(t, bareDir, env, "teammate-flow")

	svcs, err := l.Services().Sync(ctx, "")
	require.NoError(t, err)
	assert.NotEmpty(t, svcs)

	row := findFlow(t, l, "teammate-flow")
	require.NotNil(t, row, "Sync all should have pulled the workspace repository too")
}

// TestRepo_Push_SendsCommitAndReportsPushed: after a local commit, Push
// reports Pushed with the right count, and the bare origin actually has
// the commit.
func TestRepo_Push_SendsCommitAndReportsPushed(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)
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

	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	status, err := l.Repo().Push(ctx)
	require.NoError(t, err)
	assert.True(t, status.Pushed)
	assert.Equal(t, 1, status.PushedCount)
	assert.Zero(t, status.Ahead)

	select {
	case ev := <-ch:
		require.Equal(t, domain.EventWorkspaceRepo, ev.Type)
		payload, ok := ev.Payload.(*domain.RepoStatus)
		require.True(t, ok, "payload should be a *domain.RepoStatus")
		assert.True(t, payload.Pushed)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a workspace.repo event")
	}

	// The bare origin actually received it.
	verify := t.TempDir()
	runGit(t, "", env, "clone", bareDir, verify)
	assert.Equal(t, runGit(t, ws.Dir, env, "rev-parse", "HEAD"), runGit(t, verify, env, "rev-parse", "HEAD"))
}

// TestRepo_Push_BehindRefused: a branch behind its upstream cannot be
// pushed -- a pull must come first -- and the message names how far behind.
func TestRepo_Push_BehindRefused(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	pushTeammateFlow(t, bareDir, env, "teammate-flow")
	_, err = l.Repo().Fetch(ctx)
	require.NoError(t, err)

	_, err = l.Repo().Push(ctx)
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "behind its upstream by 1 commits")
}

// TestRepo_Push_NothingAheadIsNoOp: nothing to push is success, not an
// error -- Pushed stays false and no git push is even attempted (nothing
// here checks that directly, but a no-op push must never fail against a
// repository with no commits to send).
func TestRepo_Push_NothingAheadIsNoOp(t *testing.T) {
	ws, _, _ := setupGitWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	status, err := l.Repo().Push(ctx)
	require.NoError(t, err)
	assert.False(t, status.Pushed)
	assert.Zero(t, status.PushedCount)
}

// TestRepo_Push_NotInGitRefused: a workspace that is not a git repository
// at all cannot be pushed.
func TestRepo_Push_NotInGitRefused(t *testing.T) {
	ws, _ := setupWorkspace(t) // a plain temp dir, never `git init`ed
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Repo().Push(ctx)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestRepo_Watch_PeriodicFetchShowsBehind: the daemon's git tick fetches
// the workspace repository on its own, same as it already does for
// git-sourced services (see git_test.go's TestGitService_Watch_...), and
// never pulls -- Behind becomes visible from Status alone.
func TestRepo_Watch_PeriodicFetchShowsBehind(t *testing.T) {
	ws, env, bareDir := setupGitWorkspace(t)

	l, err := Open(ws, Options{Watch: true, GitSyncInterval: 100 * time.Millisecond})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	pushTeammateFlow(t, bareDir, env, "teammate-tick")

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != domain.EventWorkspaceRepo {
				continue
			}
			status, ok := ev.Payload.(*domain.RepoStatus)
			if ok && status.Behind == 1 {
				// Read-only: the flow the tick learned about must not have
				// been pulled onto disk.
				assert.Nil(t, findFlow(t, l, "teammate-tick"))
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for workspace.repo event with Behind=1")
		}
	}
}
