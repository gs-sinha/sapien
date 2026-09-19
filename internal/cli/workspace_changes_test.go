package cli_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// These tests drive `workspace changes` and `workspace commit` against the
// enginetest fake (setupFakeEngine), seeding what they read through
// SetRepoStatus/SetRepoChanges (PLAN §34f item 1).

func TestWorkspaceChanges_NotInGit(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: false})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "changes")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "not a git repository")
}

func TestWorkspaceChanges_GroupedByKind(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main"})
	fake.SetRepoChanges([]domain.RepoFileChange{
		{Path: "flows/a.flow.yaml", State: domain.ChangeUntracked, Kind: domain.RepoKindFlow, ID: "order-allocation", Title: "Order allocation"},
		{Path: "sapien.workspace.yaml", State: domain.ChangeModified, Kind: domain.RepoKindWorkspace},
		{Path: "README.md", State: domain.ChangeDeleted, Kind: domain.RepoKindOther},
	})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "changes")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "flows:")
	assert.Contains(t, stdout, "untracked  flows/a.flow.yaml")
	assert.Contains(t, stdout, "Order allocation")
	assert.Contains(t, stdout, "workspace:")
	assert.Contains(t, stdout, "modified  sapien.workspace.yaml")
	assert.Contains(t, stdout, "other:")
	assert.Contains(t, stdout, "deleted  README.md")
}

func TestWorkspaceChanges_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main"})
	fake.SetRepoChanges([]domain.RepoFileChange{{Path: "flows/a.flow.yaml", State: domain.ChangeUntracked}})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "changes", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var out domain.RepoChanges
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	require.Len(t, out.Files, 1)
	assert.Equal(t, "flows/a.flow.yaml", out.Files[0].Path)
}

func TestWorkspaceCommit_RequiresPathsOrAll(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main"})

	_, stderr, code := run(t, "--workspace", dir, "workspace", "commit", "-m", "x")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "requires at least one path, or --all")
}

func TestWorkspaceCommit_PathsAndAllTogetherRefused(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main"})

	_, stderr, code := run(t, "--workspace", dir, "workspace", "commit", "-m", "x", "--all", "flows/a.flow.yaml")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "pass paths or --all, not both")
}

// TestWorkspaceCommit_ExplicitPath: an absolute path under the workspace
// repository's root resolves to its repo-root-relative form before
// reaching the engine.
func TestWorkspaceCommit_ExplicitPath(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Root: dir})

	abs := filepath.Join(dir, "flows", "a.flow.yaml")
	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "commit", "-m", "Add flow a", abs)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "committed")
	assert.Contains(t, stdout, "flows/a.flow.yaml")

	call := lastCall(fake, "Repo.Commit")
	args, ok := call.Args.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Add flow a", args["message"])
	paths, ok := args["paths"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"flows/a.flow.yaml"}, paths)
}

// TestWorkspaceCommit_All: --all commits every untracked/modified/deleted/
// renamed file Changes reports, skipping unpushed and conflicted ones.
func TestWorkspaceCommit_All(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Root: dir})
	fake.SetRepoChanges([]domain.RepoFileChange{
		{Path: "flows/a.flow.yaml", State: domain.ChangeUntracked},
		{Path: "flows/b.flow.yaml", State: domain.ChangeModified},
		{Path: "flows/c.flow.yaml", State: domain.ChangeUnpushed},
		{Path: "flows/d.flow.yaml", State: domain.ChangeConflicted},
	})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "commit", "-m", "batch", "--all")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "committed")

	call := lastCall(fake, "Repo.Commit")
	args, ok := call.Args.(map[string]any)
	require.True(t, ok)
	paths, ok := args["paths"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"flows/a.flow.yaml", "flows/b.flow.yaml"}, paths)
}

func TestWorkspaceCommit_AllNothingToCommit(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Root: dir})
	fake.SetRepoChanges(nil)

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "commit", "-m", "batch", "--all")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "nothing to commit")
}

func TestWorkspaceCommit_PathOutsideRepoRefused(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Root: dir})

	_, stderr, code := run(t, "--workspace", dir, "workspace", "commit", "-m", "x", "/etc/passwd")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "outside the workspace repository")
}
