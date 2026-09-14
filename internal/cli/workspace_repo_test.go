package cli_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// These tests drive `workspace status`, `workspace pull`, and `workspace
// sync` against the enginetest fake (setupFakeEngine), seeding what they
// read through SetRepoStatus.

func TestWorkspaceStatus_NotInGit(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: false})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "status")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "not a git repository")
	assert.NotContains(t, stdout, "branch")
}

func TestWorkspaceStatus_BehindAheadAndDirtyAllReported(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{
		InGit: true, Branch: "main", Upstream: "origin/main",
		Behind: 3, Ahead: 2, Dirty: 5,
		FetchedAt: time.Now().Add(-90 * time.Minute),
	})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "status")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "branch main, tracking origin/main")
	assert.Contains(t, stdout, "3 commits from the team waiting")
	assert.Contains(t, stdout, "you have 2 unpushed commits")
	assert.Contains(t, stdout, "5 uncommitted files")
	assert.Contains(t, stdout, "ago")
	assert.NotContains(t, stdout, "never fetched")
}

func TestWorkspaceStatus_NeverFetchedAndFetchError(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{
		InGit: true, Branch: "main", Upstream: "origin/main",
		FetchError: "dial tcp: no route to host",
	})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "status")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "never fetched")
	assert.Contains(t, stdout, "fetch error: dial tcp: no route to host")
	assert.Contains(t, stdout, "up to date, nothing uncommitted")
}

func TestWorkspaceStatus_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Behind: 1})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "status", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got domain.RepoStatus
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.InGit)
	assert.Equal(t, "main", got.Branch)
	assert.Equal(t, 1, got.Behind)
}

func TestWorkspacePull_PulledCommits(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 4})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "pull")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "pulled 4 commits")
}

func TestWorkspacePull_AlreadyCurrent(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main"})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "pull")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "already current")
}

// TestWorkspacePull_DirtyTreeIsConflict: the engine's Conflict error
// renders through the CLI's normal error formatting -- a non-zero exit
// and the error code on stderr.
func TestWorkspacePull_DirtyTreeIsConflict(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Dirty: 1})

	_, stderr, code := run(t, "--workspace", dir, "workspace", "pull")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "E_CONFLICT")
}

func TestWorkspaceSync_PulledCommits(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 2})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "sync")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "pulled 2 commits")
}

// TestWorkspaceSync_DirtyTreeSkipsWithoutError: unlike pull, a dirty tree
// never fails sync; it says what was skipped and why.
func TestWorkspaceSync_DirtyTreeSkipsWithoutError(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 1, Dirty: 1})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "sync")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "not pulled: uncommitted changes")
}

// --- workspace push ---

func TestWorkspacePush_PushedCommits(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Ahead: 3})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "push")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "pushed 3 commits")
	assert.Equal(t, "Repo.Push", lastCall(fake, "Repo.Push").Method)
}

func TestWorkspacePush_NothingToPush(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main"})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "push")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "nothing to push")
}

// TestWorkspacePush_BehindIsConflict: the engine's Conflict error renders
// through the CLI's normal error formatting -- a non-zero exit and the
// error code on stderr -- the same way TestWorkspacePull_DirtyTreeIsConflict
// proves it for pull.
func TestWorkspacePush_BehindIsConflict(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 2})

	_, stderr, code := run(t, "--workspace", dir, "workspace", "push")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "E_CONFLICT")
}

func TestWorkspacePush_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Ahead: 5})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "push", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got domain.RepoStatus
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.Pushed)
	assert.Equal(t, 5, got.PushedCount)
}

// TestWorkspaceStatus_AheadLineMentionsPushCommand: the "you have N
// unpushed commits" line now points at the command that clears it.
func TestWorkspaceStatus_AheadLineMentionsPushCommand(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Ahead: 2})

	stdout, stderr, code := run(t, "--workspace", dir, "workspace", "status")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "you have 2 unpushed commits (sapien workspace push)")
}
