package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
