package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// These tests cover PLAN §7b's addition to `sapien service sync` with no
// name: after the services table, one line reporting what happened to the
// workspace's own repository (which the local engine's Services().Sync("")
// already syncs as part of that call).

func TestServiceSync_TeamRepoLine_PulledCommits(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 3})

	stdout, stderr, code := run(t, "--workspace", dir, "service", "sync")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "team repo: pulled 3 commits")
}

func TestServiceSync_TeamRepoLine_AlreadyCurrent(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main"})

	stdout, stderr, code := run(t, "--workspace", dir, "service", "sync")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "team repo: already current")
}

// TestServiceSync_TeamRepoLine_SkippedWhenNotInGit: a workspace that is
// not a git repository at all gets no team-repo line -- there is nothing
// to report.
func TestServiceSync_TeamRepoLine_SkippedWhenNotInGit(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: false})

	stdout, stderr, code := run(t, "--workspace", dir, "service", "sync")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "team repo")
}

// TestServiceSync_WithNameOmitsTeamRepoLine: syncing one named service
// never touches or reports on the workspace repository.
func TestServiceSync_WithNameOmitsTeamRepoLine(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	fake.SetRepoStatus(domain.RepoStatus{InGit: true, Branch: "main", Upstream: "origin/main", Behind: 3})

	stdout, stderr, code := run(t, "--workspace", dir, "service", "sync", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "team repo")
}
