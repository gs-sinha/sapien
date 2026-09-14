package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestRepoRoot(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	wantRoot, err := filepath.EvalSymlinks(work)
	require.NoError(t, err)

	root, ok := m.RepoRoot(context.Background(), filepath.Join(work, "flows"))
	require.True(t, ok)
	assert.Equal(t, wantRoot, root)

	_, ok = m.RepoRoot(context.Background(), t.TempDir())
	assert.False(t, ok, "a directory with no enclosing repository")
}

// TestFileStates_UntrackedAndModified: a brand-new file in the flows
// directory reports untracked, and an edit to a file already tracked and
// pushed reports modified -- both from the one `status` call.
func TestFileStates_UntrackedAndModified(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	untrackedPath := filepath.Join(work, "flows", "new.flow.yaml")
	require.NoError(t, os.WriteFile(untrackedPath, []byte("b: 1\n"), 0o644))

	trackedPath := filepath.Join(work, "flows", "a.flow.yaml")
	require.NoError(t, os.WriteFile(trackedPath, []byte("a: 2\n"), 0o644))

	states, err := m.FileStates(context.Background(), work, []string{untrackedPath, trackedPath})
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUntracked, states[untrackedPath])
	assert.Equal(t, domain.ShipModified, states[trackedPath])
}

// TestFileStates_NoUpstream_EveryCleanTrackedPathIsUnpushed: a repository
// with no remote at all (never cloned, never pushed) reports every clean,
// tracked path as unpushed -- nothing has been shared, so it cannot be
// shipped.
func TestFileStates_NoUpstream_EveryCleanTrackedPathIsUnpushed(t *testing.T) {
	env := hermeticGitEnv(t)
	work := t.TempDir()
	runGit(t, "", env, "init", "-b", "main", work)
	require.NoError(t, os.MkdirAll(filepath.Join(work, "flows"), 0o755))
	p := filepath.Join(work, "flows", "a.flow.yaml")
	require.NoError(t, os.WriteFile(p, []byte("a: 1\n"), 0o644))
	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "init")

	m := testManager(t, env)
	states, err := m.FileStates(context.Background(), work, []string{p})
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, states[p], "no upstream at all: nothing has been shared")
}

// TestFileStates_CommittedNotPushedIsUnpushed: a local commit the remote
// does not have yet reports unpushed for the file it touched, while a
// second, untouched file that was already shipped stays shipped.
func TestFileStates_CommittedNotPushedIsUnpushed(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n", "flows/b.flow.yaml": "b: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)

	pathA := filepath.Join(work, "flows", "a.flow.yaml")
	pathB := filepath.Join(work, "flows", "b.flow.yaml")
	require.NoError(t, os.WriteFile(pathA, []byte("a: 2\n"), 0o644))
	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "unpushed change")

	m := testManager(t, env)
	states, err := m.FileStates(context.Background(), work, []string{pathA, pathB})
	require.NoError(t, err)
	assert.Equal(t, domain.ShipUnpushed, states[pathA], "committed here, not yet on the upstream")
	assert.Equal(t, domain.ShipShipped, states[pathB], "untouched by the unpushed commit, and already on the upstream")
}

// TestFileStates_ShippedAfterPush: once the commit reaches the upstream,
// the file it touched reports shipped.
func TestFileStates_ShippedAfterPush(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	pathA := filepath.Join(work, "flows", "a.flow.yaml")
	require.NoError(t, os.WriteFile(pathA, []byte("a: 2\n"), 0o644))
	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "change")
	runGit(t, work, env, "push", "origin", "HEAD")

	m := testManager(t, env)
	states, err := m.FileStates(context.Background(), work, []string{pathA})
	require.NoError(t, err)
	assert.Equal(t, domain.ShipShipped, states[pathA])
}

// TestCommitPaths_CommitsExactlyGivenFile: CommitPaths commits only the
// path it was given, even though another tracked file is also modified and
// sitting in the working tree uncommitted.
func TestCommitPaths_CommitsExactlyGivenFile(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n", "flows/b.flow.yaml": "b: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	pathA := filepath.Join(work, "flows", "a.flow.yaml")
	pathB := filepath.Join(work, "flows", "b.flow.yaml")
	require.NoError(t, os.WriteFile(pathA, []byte("a: promoted\n"), 0o644))
	require.NoError(t, os.WriteFile(pathB, []byte("b: also edited, but not committed\n"), 0o644))

	m := testManager(t, env)
	sha, err := m.CommitPaths(context.Background(), work, []string{pathA}, "Promote flow a")
	require.NoError(t, err)
	assert.NotEmpty(t, sha)

	head := runGit(t, work, env, "rev-parse", "HEAD")
	assert.Equal(t, head, sha)
	assert.Equal(t, "Promote flow a", runGit(t, work, env, "log", "-1", "--format=%s"))

	status := runGit(t, work, env, "status", "--porcelain=v1", "--", "flows/b.flow.yaml")
	assert.Contains(t, status, "b.flow.yaml", "the other modified file must stay uncommitted")

	log := runGit(t, work, env, "log", "-1", "--name-only", "--format=")
	assert.Equal(t, "flows/a.flow.yaml", log, "the commit's tree change must contain only the given file")

	// The commit must never be pushed.
	remoteHead := runGit(t, "", env, "--git-dir="+bareDir, "rev-parse", "main")
	assert.NotEqual(t, sha, remoteHead, "CommitPaths must never push")
}

func TestCommitPaths_NoPaths(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	_, err := m.CommitPaths(context.Background(), work, nil, "message")
	require.Error(t, err)
}
