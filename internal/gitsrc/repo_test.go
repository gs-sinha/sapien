package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
)

// commitLocal commits an edit already written to work, using the test's
// fixed identity, without pushing -- a local, unshared commit.
func commitLocal(t *testing.T, work string, env []string, files map[string]string, msg string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(work, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", msg)
}

func TestRepoStatus_NotInGit(t *testing.T) {
	env := hermeticGitEnv(t)
	m := testManager(t, env)

	status, err := m.RepoStatus(context.Background(), t.TempDir())
	require.NoError(t, err)
	assert.False(t, status.InGit)
	assert.Empty(t, status.Root)
	assert.Empty(t, status.Branch)
	assert.Zero(t, status.Behind)
	assert.Zero(t, status.Ahead)
	assert.Zero(t, status.Dirty)
	assert.True(t, status.FetchedAt.IsZero())
}

// TestRepoStatus_Fields covers the basic fields of a fresh clone: in git,
// on its default branch, an origin, an upstream, nothing behind/ahead/dirty
// yet, and a fetch timestamp from the clone's own implicit fetch.
func TestRepoStatus_Fields(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	status, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.True(t, status.InGit)
	assert.NotEmpty(t, status.Root)
	assert.Equal(t, "main", status.Branch)
	assert.Equal(t, bareDir, status.Remote)
	assert.Equal(t, "origin/main", status.Upstream)
	assert.Zero(t, status.Behind)
	assert.Zero(t, status.Ahead)
	assert.Zero(t, status.Dirty)
	assert.True(t, status.FetchedAt.IsZero(), "a fresh clone writes no FETCH_HEAD; only an explicit fetch does")
}

// TestRepoStatus_NoUpstream: a repository with no remote at all reports an
// empty Upstream and zero Behind/Ahead, never an error.
func TestRepoStatus_NoUpstream(t *testing.T) {
	env := hermeticGitEnv(t)
	work := t.TempDir()
	runGit(t, "", env, "init", "-q", "-b", "main", work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644))
	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-q", "-m", "init")

	m := testManager(t, env)
	status, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.True(t, status.InGit)
	assert.Empty(t, status.Remote)
	assert.Empty(t, status.Upstream)
	assert.Zero(t, status.Behind)
	assert.Zero(t, status.Ahead)
}

// TestRepoStatus_BehindAfterFetch: a teammate's push is invisible until
// FetchRepo runs -- Sapien never fetches implicitly to answer Status -- and
// Behind reflects it immediately afterward.
func TestRepoStatus_BehindAfterFetch(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	commitAndPush(t, bareDir, env, map[string]string{"flows/b.flow.yaml": "b: 1\n"}, "teammate change")

	status, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.Zero(t, status.Behind, "never fetched here yet, so the push is invisible")

	require.NoError(t, m.FetchRepo(context.Background(), work))

	status, err = m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Behind)
	assert.Zero(t, status.Ahead)
	assert.False(t, status.FetchedAt.IsZero())
}

// TestRepoStatus_AheadAfterLocalCommit: a local, unpushed commit shows up
// as Ahead, with Behind staying zero.
func TestRepoStatus_AheadAfterLocalCommit(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	commitLocal(t, work, env, map[string]string{"flows/local.flow.yaml": "c: 1\n"}, "local work")

	status, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Ahead)
	assert.Zero(t, status.Behind)
}

// TestRepoStatus_Dirty: an untracked file counts toward Dirty.
func TestRepoStatus_Dirty(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	require.NoError(t, os.WriteFile(filepath.Join(work, "untracked.txt"), []byte("x\n"), 0o644))

	status, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Dirty)
}

// TestPullFastForward_BringsCommitsAndReportsCount: a clean, behind
// checkout fast-forwards and PullFastForward reports exactly how many
// commits arrived.
func TestPullFastForward_BringsCommitsAndReportsCount(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	commitAndPush(t, bareDir, env, map[string]string{"flows/b.flow.yaml": "b: 1\n"}, "second")
	commitAndPush(t, bareDir, env, map[string]string{"flows/c.flow.yaml": "c: 1\n"}, "third")

	require.NoError(t, m.FetchRepo(context.Background(), work))

	n, err := m.PullFastForward(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.FileExists(t, filepath.Join(work, "flows", "b.flow.yaml"))
	assert.FileExists(t, filepath.Join(work, "flows", "c.flow.yaml"))

	status, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.Zero(t, status.Behind)
}

// TestPushRepo_SendsCommitsAndReportsCount: a clean, ahead checkout (with
// an upstream already tracked, the common case after a `git clone`) pushes
// with a plain `git push`, and PushRepo reports exactly how many commits
// went up, computed before the push.
func TestPushRepo_SendsCommitsAndReportsCount(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	commitLocal(t, work, env, map[string]string{"flows/b.flow.yaml": "b: 1\n"}, "second")
	commitLocal(t, work, env, map[string]string{"flows/c.flow.yaml": "c: 1\n"}, "third")

	n, err := m.PushRepo(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	status, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.Zero(t, status.Ahead, "everything just pushed must no longer show as ahead")

	// The bare origin must actually have received it: cloning it again
	// shows the same two commits.
	verify := t.TempDir()
	runGit(t, "", env, "clone", bareDir, verify)
	assert.FileExists(t, filepath.Join(verify, "flows", "b.flow.yaml"))
	assert.FileExists(t, filepath.Join(verify, "flows", "c.flow.yaml"))
}

// TestPushRepo_NoUpstream_SetsUpstreamAndPushes: a branch that has never
// been pushed before (no upstream tracked at all) is pushed with
// `--set-upstream origin <branch>`, and the branch's upstream is
// configured afterward.
func TestPushRepo_NoUpstream_SetsUpstreamAndPushes(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	runGit(t, work, env, "checkout", "-q", "-b", "feature")
	m := testManager(t, env)

	commitLocal(t, work, env, map[string]string{"flows/feature.flow.yaml": "f: 1\n"}, "feature work")

	status, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	require.Empty(t, status.Upstream, "precondition: a brand-new branch has no upstream yet")

	_, err = m.PushRepo(context.Background(), work)
	require.NoError(t, err)

	after, err := m.RepoStatus(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, "origin/feature", after.Upstream)
	assert.Zero(t, after.Ahead)

	verify := t.TempDir()
	runGit(t, "", env, "clone", "--branch", "feature", bareDir, verify)
	assert.FileExists(t, filepath.Join(verify, "flows", "feature.flow.yaml"))
}

// TestPushRepo_NeverForces: PushRepo must never overwrite a diverged
// upstream -- a plain `git push` refuses that on its own (no --force is
// ever passed), and PushRepo must surface the failure rather than retry
// with force.
func TestPushRepo_NeverForces(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	// A teammate pushes to the same branch, diverging it from work's HEAD.
	commitAndPush(t, bareDir, env, map[string]string{"flows/teammate.flow.yaml": "t: 1\n"}, "teammate change")
	// work makes its own local commit without ever fetching the teammate's.
	commitLocal(t, work, env, map[string]string{"flows/local.flow.yaml": "l: 1\n"}, "local work")

	_, err := m.PushRepo(context.Background(), work)
	require.Error(t, err, "a diverged push must be refused, not forced through")

	// The bare origin must still have only the teammate's commit, not a
	// forced overwrite.
	verify := t.TempDir()
	runGit(t, "", env, "clone", bareDir, verify)
	assert.FileExists(t, filepath.Join(verify, "flows", "teammate.flow.yaml"))
	assert.NoFileExists(t, filepath.Join(verify, "flows", "local.flow.yaml"))
}

// TestPullFastForward_DivergedFailsAndLeavesTreeAlone: a local, unpushed
// commit that diverges from a teammate's push cannot fast-forward;
// PullFastForward refuses with Conflict and never moves HEAD.
func TestPullFastForward_DivergedFailsAndLeavesTreeAlone(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	commitAndPush(t, bareDir, env, map[string]string{"flows/b.flow.yaml": "b: 1\n"}, "teammate change")
	commitLocal(t, work, env, map[string]string{"flows/local.flow.yaml": "c: 1\n"}, "local work")
	beforeHead := runGit(t, work, env, "rev-parse", "HEAD")

	require.NoError(t, m.FetchRepo(context.Background(), work))

	n, err := m.PullFastForward(context.Background(), work)
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Zero(t, n)

	afterHead := runGit(t, work, env, "rev-parse", "HEAD")
	assert.Equal(t, beforeHead, afterHead, "a failed ff-only merge must never move HEAD")
}
