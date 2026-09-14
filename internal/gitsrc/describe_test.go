package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescribe_Checkout(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	sha := commitAndPush(t, bareDir, env, map[string]string{
		"api/openapi.yaml": "v1\n",
		"README.md":        "root\n",
	}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	co, err := m.Describe(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, work, co.Path)
	assert.Equal(t, "main", co.Branch)
	assert.Equal(t, sha, co.Commit)
	assert.Equal(t, bareDir, co.Remote)
	assert.Equal(t, 0, co.Dirty)

	// Dirty counts modified and untracked files under the described
	// directory only: an edit outside api/ is invisible from api/.
	require.NoError(t, os.WriteFile(filepath.Join(work, "api", "openapi.yaml"), []byte("v2\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(work, "api", "new.md"), []byte("new\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(work, "README.md"), []byte("edited\n"), 0o644))

	pkg, err := m.Describe(context.Background(), filepath.Join(work, "api"))
	require.NoError(t, err)
	assert.Equal(t, 2, pkg.Dirty)
	assert.Equal(t, "main", pkg.Branch, "a subdirectory describes the enclosing repository")

	root, err := m.Describe(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, 3, root.Dirty)
}

func TestDescribe_DetachedHead(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	sha := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	runGit(t, work, env, "checkout", "--detach", sha)

	co, err := testManager(t, env).Describe(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, "HEAD", co.Branch)
	assert.Equal(t, sha, co.Commit)
}

func TestDescribe_NoRemoteAndNoCommits(t *testing.T) {
	env := hermeticGitEnv(t)
	work := t.TempDir()
	runGit(t, "", env, "init", "-b", "main", work)

	co, err := testManager(t, env).Describe(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, work, co.Path)
	assert.Empty(t, co.Remote)
	assert.Empty(t, co.Commit)
	assert.Empty(t, co.Branch)
}

func TestDescribe_NotARepository(t *testing.T) {
	env := hermeticGitEnv(t)
	dir := t.TempDir()

	co, err := testManager(t, env).Describe(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, dir, co.Path)
	assert.Empty(t, co.Branch)
	assert.Empty(t, co.Commit)
	assert.Empty(t, co.Remote)
	assert.Equal(t, 0, co.Dirty)
}

func TestDescribe_MissingDirectoryIsAnError(t *testing.T) {
	env := hermeticGitEnv(t)
	_, err := testManager(t, env).Describe(context.Background(), filepath.Join(t.TempDir(), "nope"))
	require.Error(t, err)
}

func TestDescribeAgainst_CommittedAt(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	wantStamp := runGit(t, work, env, "log", "-1", "--format=%cI")

	co, err := testManager(t, env).DescribeAgainst(context.Background(), work, "")
	require.NoError(t, err)
	require.False(t, co.CommittedAt.IsZero())
	wantTime, err := time.Parse(time.RFC3339, wantStamp)
	require.NoError(t, err)
	assert.True(t, wantTime.Equal(co.CommittedAt), "want %s, got %s", wantTime, co.CommittedAt)
}

func TestDescribeAgainst_NoCommits_CommittedAtStaysZero(t *testing.T) {
	env := hermeticGitEnv(t)
	work := t.TempDir()
	runGit(t, "", env, "init", "-b", "main", work)

	co, err := testManager(t, env).DescribeAgainst(context.Background(), work, "")
	require.NoError(t, err)
	assert.True(t, co.CommittedAt.IsZero())
}

func TestDescribeAgainst_AheadBehind(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	// Freshly cloned: even with origin/<ref>, nothing to be ahead or behind.
	co, err := m.DescribeAgainst(context.Background(), work, "main")
	require.NoError(t, err)
	assert.Equal(t, 0, co.Ahead)
	assert.Equal(t, 0, co.Behind)

	// A local commit, never pushed: ahead by one, still behind by nothing.
	require.NoError(t, os.WriteFile(filepath.Join(work, "api", "openapi.yaml"), []byte("v2\n"), 0o644))
	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "local change")

	co, err = m.DescribeAgainst(context.Background(), work, "main")
	require.NoError(t, err)
	assert.Equal(t, 1, co.Ahead)
	assert.Equal(t, 0, co.Behind)

	// The bare repo moves on from a second clone; DescribeAgainst must
	// never fetch to notice, so the test fetches on work's behalf.
	other := t.TempDir()
	runGit(t, "", env, "clone", bareDir, other)
	require.NoError(t, os.WriteFile(filepath.Join(other, "api", "openapi.yaml"), []byte("v3\n"), 0o644))
	runGit(t, other, env, "add", "-A")
	runGit(t, other, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "upstream change")
	runGit(t, other, env, "push", "origin", "HEAD:main")

	stillStale, err := m.DescribeAgainst(context.Background(), work, "main")
	require.NoError(t, err)
	assert.Equal(t, 1, stillStale.Ahead)
	assert.Equal(t, 0, stillStale.Behind, "must not fetch, so origin/main is still where it was at clone time")

	runGit(t, work, env, "fetch", "origin")
	co, err = m.DescribeAgainst(context.Background(), work, "main")
	require.NoError(t, err)
	assert.Equal(t, 1, co.Ahead)
	assert.Equal(t, 1, co.Behind)
}

func TestDescribeAgainst_UnknownRefIsZeroNotError(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)

	co, err := testManager(t, env).DescribeAgainst(context.Background(), work, "no-such-branch")
	require.NoError(t, err)
	assert.Equal(t, 0, co.Ahead)
	assert.Equal(t, 0, co.Behind)
}

func TestDescribeAgainst_Worktree(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	main, err := m.DescribeAgainst(context.Background(), work, "")
	require.NoError(t, err)
	assert.False(t, main.Worktree, "the primary clone is not a worktree")

	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, work, env, "worktree", "add", "-b", "wt-branch", wt)

	described, err := m.DescribeAgainst(context.Background(), wt, "")
	require.NoError(t, err)
	assert.True(t, described.Worktree)
	assert.Equal(t, "wt-branch", described.Branch)
}

func TestManager_Toplevel(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	m := testManager(t, env)

	// EvalSymlinks: on macOS t.TempDir() is under /tmp, itself a symlink to
	// /private/tmp, which `git rev-parse --show-toplevel` always resolves.
	wantTop, err := filepath.EvalSymlinks(work)
	require.NoError(t, err)

	top, err := m.Toplevel(context.Background(), filepath.Join(work, "api"))
	require.NoError(t, err)
	assert.Equal(t, wantTop, top, "the toplevel of a subdirectory is the repository root")

	top, err = m.Toplevel(context.Background(), work)
	require.NoError(t, err)
	assert.Equal(t, wantTop, top)

	_, err = m.Toplevel(context.Background(), t.TempDir())
	require.Error(t, err, "not a git repository at all")
}

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Org/Repo.git":              "github.com/org/repo",
		"https://github.com/org/repo":              "github.com/org/repo",
		"https://github.com/org/repo/":             "github.com/org/repo",
		"https://github.com/org/repo.git":          "github.com/org/repo",
		"ssh://git@github.com/org/repo.git":        "github.com/org/repo",
		"ssh://git@github.com:22/org/repo.git":     "github.com/org/repo",
		"https://user:token@github.com/org/repo":   "github.com/org/repo",
		"git@github.com:/org/repo.git":             "github.com/org/repo",
		"GIT@GitHub.com:org/repo":                  "github.com/org/repo",
		"git://github.com/org/repo.git":            "github.com/org/repo",
		"file:///tmp/repos/repo.git":               "/tmp/repos/repo",
		"/tmp/repos/repo.git":                      "/tmp/repos/repo",
		"/tmp/repos/repo":                          "/tmp/repos/repo",
		"  https://gitlab.example.com/a/b/c.git  ": "gitlab.example.com/a/b/c",
		"": "",
	}
	for in, want := range cases {
		assert.Equalf(t, want, NormalizeRemote(in), "NormalizeRemote(%q)", in)
	}
}
