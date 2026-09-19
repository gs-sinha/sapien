package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// findChange returns the entry in changes whose Path == path, failing the
// test if there is none.
func findChange(t *testing.T, changes []domain.RepoFileChange, path string) domain.RepoFileChange {
	t.Helper()
	for _, c := range changes {
		if c.Path == path {
			return c
		}
	}
	t.Fatalf("no change for %q in %v", path, changes)
	return domain.RepoFileChange{}
}

// TestChanges_UntrackedDirectoryExpansion: a brand-new directory with
// several files reports one entry per file, not one for the directory --
// `-uall` is what asks git to expand it.
func TestChanges_UntrackedDirectoryExpansion(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	require.NoError(t, os.MkdirAll(filepath.Join(work, "flows", "new"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "new", "b.flow.yaml"), []byte("b: 1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "new", "c.flow.yaml"), []byte("c: 1\n"), 0o644))

	m := testManager(t, env)
	changes, err := m.Changes(context.Background(), work)
	require.NoError(t, err)

	b := findChange(t, changes, "flows/new/b.flow.yaml")
	assert.Equal(t, domain.ChangeUntracked, b.State)
	c := findChange(t, changes, "flows/new/c.flow.yaml")
	assert.Equal(t, domain.ChangeUntracked, c.State)
	for _, ch := range changes {
		assert.NotEqual(t, "flows/new", ch.Path, "the directory itself must not appear, only its files")
	}
}

// TestChanges_Rename: a staged rename reports one ChangeRenamed entry
// carrying both the new and old paths.
func TestChanges_Rename(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	runGit(t, work, env, "mv", "flows/a.flow.yaml", "flows/renamed.flow.yaml")

	m := testManager(t, env)
	changes, err := m.Changes(context.Background(), work)
	require.NoError(t, err)

	r := findChange(t, changes, "flows/renamed.flow.yaml")
	assert.Equal(t, domain.ChangeRenamed, r.State)
	assert.Equal(t, "flows/a.flow.yaml", r.OldPath)
}

// TestChanges_Deleted: a tracked file removed from the working tree, not
// yet committed, reports deleted.
func TestChanges_Deleted(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	require.NoError(t, os.Remove(filepath.Join(work, "flows", "a.flow.yaml")))

	m := testManager(t, env)
	changes, err := m.Changes(context.Background(), work)
	require.NoError(t, err)

	d := findChange(t, changes, "flows/a.flow.yaml")
	assert.Equal(t, domain.ChangeDeleted, d.State)
}

// TestChanges_UnpushedListing: a file touched by a local, unpushed commit,
// and otherwise clean, reports unpushed -- alongside an untracked file that
// git status already reports on its own.
func TestChanges_UnpushedListing(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n", "flows/b.flow.yaml": "b: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	commitLocal(t, work, env, map[string]string{"flows/a.flow.yaml": "a: 2\n"}, "unpushed change")
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "new.flow.yaml"), []byte("n: 1\n"), 0o644))

	m := testManager(t, env)
	changes, err := m.Changes(context.Background(), work)
	require.NoError(t, err)

	a := findChange(t, changes, "flows/a.flow.yaml")
	assert.Equal(t, domain.ChangeUnpushed, a.State)
	n := findChange(t, changes, "flows/new.flow.yaml")
	assert.Equal(t, domain.ChangeUntracked, n.State)
	for _, ch := range changes {
		assert.NotEqual(t, "flows/b.flow.yaml", ch.Path, "an untouched, already-shipped file must not appear")
	}
}

// TestChanges_NoUpstream_NothingExtraReported: without an upstream at all,
// Changes reports only what `git status` says -- it never scans the whole
// history to guess at "unpushed".
func TestChanges_NoUpstream_NothingExtraReported(t *testing.T) {
	env := hermeticGitEnv(t)
	work := t.TempDir()
	runGit(t, "", env, "init", "-b", "main", work)
	commitLocal(t, work, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "b.flow.yaml"), []byte("b: 1\n"), 0o644))

	m := testManager(t, env)
	changes, err := m.Changes(context.Background(), work)
	require.NoError(t, err)

	require.Len(t, changes, 1)
	assert.Equal(t, "flows/b.flow.yaml", changes[0].Path)
	assert.Equal(t, domain.ChangeUntracked, changes[0].State)
}

// TestChanges_SubdirWorkspace: when dir is a subdirectory of the
// repository, only files under it are reported, still repo-root-relative.
func TestChanges_SubdirWorkspace(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{
		"team-a/flows/a.flow.yaml": "a: 1\n",
		"team-b/flows/b.flow.yaml": "b: 1\n",
	}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "team-a", "flows", "a.flow.yaml"), []byte("a: 2\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(work, "team-b", "flows", "b.flow.yaml"), []byte("b: 2\n"), 0o644))

	m := testManager(t, env)
	changes, err := m.Changes(context.Background(), filepath.Join(work, "team-a"))
	require.NoError(t, err)

	require.Len(t, changes, 1)
	assert.Equal(t, "team-a/flows/a.flow.yaml", changes[0].Path)
}

// TestCommitPaths_Deletion: CommitPaths (git add -A) commits a working-tree
// deletion correctly.
func TestCommitPaths_Deletion(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	pathA := filepath.Join(work, "flows", "a.flow.yaml")
	require.NoError(t, os.Remove(pathA))

	m := testManager(t, env)
	sha, err := m.CommitPaths(context.Background(), work, []string{pathA}, "Remove flow a")
	require.NoError(t, err)
	assert.NotEmpty(t, sha)

	status := runGit(t, work, env, "status", "--porcelain=v1")
	assert.Empty(t, status, "the deletion must be committed, leaving a clean tree")
	assert.NoFileExists(t, pathA)
}

// TestCommitPaths_Rename: CommitPaths (git add -A) commits a rename
// correctly.
func TestCommitPaths_Rename(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	oldPath := filepath.Join(work, "flows", "a.flow.yaml")
	newPath := filepath.Join(work, "flows", "renamed.flow.yaml")
	require.NoError(t, os.Rename(oldPath, newPath))

	m := testManager(t, env)
	sha, err := m.CommitPaths(context.Background(), work, []string{oldPath, newPath}, "Rename flow a")
	require.NoError(t, err)
	assert.NotEmpty(t, sha)

	status := runGit(t, work, env, "status", "--porcelain=v1")
	assert.Empty(t, status, "the rename must be committed, leaving a clean tree")
	assert.NoFileExists(t, oldPath)
	assert.FileExists(t, newPath)
}

// TestCommitPaths_MessageRequired: an empty (or whitespace-only) message is
// refused before any git command runs.
func TestCommitPaths_MessageRequired(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	pathA := filepath.Join(work, "flows", "a.flow.yaml")
	require.NoError(t, os.WriteFile(pathA, []byte("a: 2\n"), 0o644))

	m := testManager(t, env)
	_, err := m.CommitPaths(context.Background(), work, []string{pathA}, "   ")
	require.Error(t, err)
}

// TestCommitPaths_NothingChanged: a path FileStates reports as already
// shipped (or unpushed) refuses with an error instead of letting `git
// commit` fail on its own.
func TestCommitPaths_NothingChanged(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	pathA := filepath.Join(work, "flows", "a.flow.yaml")

	m := testManager(t, env)
	_, err := m.CommitPaths(context.Background(), work, []string{pathA}, "no-op")
	require.Error(t, err)
}

// TestDiff_UntrackedContent: an untracked file's Diff response carries its
// raw content.
func TestDiff_UntrackedContent(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "new.flow.yaml"), []byte("n: 1\n"), 0o644))

	m := testManager(t, env)
	diff, err := m.Diff(context.Background(), work, "flows/new.flow.yaml")
	require.NoError(t, err)
	assert.Equal(t, domain.ChangeUntracked, diff.State)
	assert.Equal(t, "n: 1\n", diff.Content)
	assert.False(t, diff.Binary)
	assert.False(t, diff.Truncated)
}

// TestDiff_Modified: a tracked, modified file's Diff shows a textual diff
// against HEAD.
func TestDiff_Modified(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "a.flow.yaml"), []byte("a: 2\n"), 0o644))

	m := testManager(t, env)
	diff, err := m.Diff(context.Background(), work, "flows/a.flow.yaml")
	require.NoError(t, err)
	assert.Equal(t, domain.ChangeModified, diff.State)
	assert.Contains(t, diff.Diff, "-a: 1")
	assert.Contains(t, diff.Diff, "+a: 2")
}

// TestDiff_UnpushedCleanFile: a committed-but-not-pushed file with no
// working-tree changes diffs against its upstream, not HEAD.
func TestDiff_UnpushedCleanFile(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	commitLocal(t, work, env, map[string]string{"flows/a.flow.yaml": "a: 2\n"}, "unpushed change")

	m := testManager(t, env)
	diff, err := m.Diff(context.Background(), work, "flows/a.flow.yaml")
	require.NoError(t, err)
	assert.Equal(t, domain.ChangeUnpushed, diff.State)
	assert.Contains(t, diff.Diff, "-a: 1")
	assert.Contains(t, diff.Diff, "+a: 2")
}

// TestDiff_Truncation: a file bigger than the cap comes back truncated.
func TestDiff_Truncation(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	big := strings.Repeat("x", diffCap+1024)
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "big.flow.yaml"), []byte(big), 0o644))

	m := testManager(t, env)
	diff, err := m.Diff(context.Background(), work, "flows/big.flow.yaml")
	require.NoError(t, err)
	assert.True(t, diff.Truncated)
	assert.LessOrEqual(t, len(diff.Content), diffCap)
	assert.False(t, diff.Binary)
}

// TestDiff_Binary: an untracked file with a NUL byte is reported binary,
// with no content.
func TestDiff_Binary(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "bin.dat"), []byte{0x00, 0x01, 0x02, 0x03}, 0o644))

	m := testManager(t, env)
	diff, err := m.Diff(context.Background(), work, "flows/bin.dat")
	require.NoError(t, err)
	assert.True(t, diff.Binary)
	assert.Empty(t, diff.Content)
}

// TestDiff_BinaryTracked: a tracked binary file's diff is reported binary
// via numstat, without ever materializing a textual diff.
func TestDiff_BinaryTracked(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{
		"flows/a.flow.yaml": "a: 1\n",
		"flows/bin.dat":     "\x00\x01",
	}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "flows", "bin.dat"), []byte{0x00, 0x01, 0x02}, 0o644))

	m := testManager(t, env)
	diff, err := m.Diff(context.Background(), work, "flows/bin.dat")
	require.NoError(t, err)
	assert.True(t, diff.Binary)
	assert.Empty(t, diff.Diff)
}

// TestDiff_RejectsAbsolutePath: an absolute path is refused.
func TestDiff_RejectsAbsolutePath(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)

	m := testManager(t, env)
	_, err := m.Diff(context.Background(), work, filepath.Join(work, "flows", "a.flow.yaml"))
	require.Error(t, err)
}

// TestDiff_RejectsEscapingPath: a path that climbs out of the repository is
// refused.
func TestDiff_RejectsEscapingPath(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)

	m := testManager(t, env)
	_, err := m.Diff(context.Background(), work, "../../etc/passwd")
	require.Error(t, err)
}

// TestDiff_RejectsIgnoredPath: a path git ignores is refused.
func TestDiff_RejectsIgnoredPath(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, _ := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"flows/a.flow.yaml": "a: 1\n", ".gitignore": "ignored.txt\n"}, "init")

	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "ignored.txt"), []byte("secret\n"), 0o644))

	m := testManager(t, env)
	_, err := m.Diff(context.Background(), work, "ignored.txt")
	require.Error(t, err)
}
