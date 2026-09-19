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

func TestCloneInto(t *testing.T) {
	env := hermeticGitEnv(t)
	m := testManager(t, env)

	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"README.md": "hello\n"}, "init")

	dir := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, m.CloneInto(context.Background(), url, dir))

	assert.FileExists(t, filepath.Join(dir, "README.md"))
	assert.DirExists(t, filepath.Join(dir, ".git"))
}

func TestCloneInto_EmptyExistingDir(t *testing.T) {
	env := hermeticGitEnv(t)
	m := testManager(t, env)

	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"README.md": "hello\n"}, "init")

	dir := t.TempDir()
	require.NoError(t, m.CloneInto(context.Background(), url, dir))
	assert.FileExists(t, filepath.Join(dir, "README.md"))
}

func TestCloneInto_NonEmptyDirIsConflict(t *testing.T) {
	env := hermeticGitEnv(t)
	m := testManager(t, env)

	_, url := newBareRepo(t, env)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644))

	err := m.CloneInto(context.Background(), url, dir)
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}

func TestCloneInto_DestinationIsFileIsInvalid(t *testing.T) {
	env := hermeticGitEnv(t)
	m := testManager(t, env)

	_, url := newBareRepo(t, env)

	dir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(dir, []byte("x"), 0o644))

	err := m.CloneInto(context.Background(), url, dir)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestCloneInto_EmptyURLIsInvalid(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))

	err := m.CloneInto(context.Background(), "", filepath.Join(t.TempDir(), "clone"))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// A url beginning with "-" is refused before git sees it: without the check
// git would parse it as an option (option injection), not a repository.
func TestCloneInto_LeadingDashURLIsInvalid(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))

	err := m.CloneInto(context.Background(), "-u@host:path", filepath.Join(t.TempDir(), "clone"))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestCloneInto_BogusFileURLDoesNotTouchNetwork(t *testing.T) {
	env := hermeticGitEnv(t)
	m := testManager(t, env)

	missing := filepath.Join(t.TempDir(), "does-not-exist.git")
	err := m.CloneInto(context.Background(), "file://"+missing, filepath.Join(t.TempDir(), "clone"))
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
	assert.Equal(t, "file://"+missing, errs.As(err).Details["url"])
}

func TestInitRepo(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))

	dir := t.TempDir()
	require.NoError(t, m.InitRepo(context.Background(), dir))
	assert.DirExists(t, filepath.Join(dir, ".git"))
}
