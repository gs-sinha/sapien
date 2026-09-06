package registry

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/gitsrc"
)

// The scenario these tests exist for, observed in real use: a service repo
// is registered from a git URL moments before its api/ package is pushed.
// The clone is made against the older tip, has no api/, and -- because
// Ensure never fetches -- every retry reads the same tree and fails
// identically, with an error naming a cache path and nothing about why.

func gitEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"HOME=" + t.TempDir(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	}
}

func git(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// pushFiles commits files (repo-relative path -> content) onto the bare
// repo's main branch and returns the new commit.
func pushFiles(t *testing.T, bare string, env []string, files map[string]string, msg string) string {
	t.Helper()
	work := t.TempDir()
	git(t, "", env, "clone", bare, work)
	for rel, content := range files {
		p := filepath.Join(work, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	git(t, work, env, "add", "-A")
	git(t, work, env, "commit", "-m", msg)
	git(t, work, env, "push", "origin", "HEAD:main")
	return git(t, work, env, "rev-parse", "HEAD")
}

const testContract = `openapi: 3.0.0
info:
  title: flow
  version: "1"
paths:
  /things:
    get:
      operationId: listThings
      responses:
        "200":
          description: ok
`

// gitFixture builds a bare repo holding only application code (no api/
// package), plus a Builder whose managed clones live in a temp cache.
func gitFixture(t *testing.T) (bare string, src domain.Source, newBuilder func() *Builder, env []string) {
	t.Helper()
	env = gitEnv(t)

	bare = filepath.Join(t.TempDir(), "repo.git")
	git(t, "", env, "init", "--bare", "-b", "main", bare)
	pushFiles(t, bare, env, map[string]string{"app/main.py": "print(1)\n"}, "app only")

	mgr := gitsrc.New(gitsrc.Options{CacheDir: t.TempDir(), Env: env})
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	src = domain.Source{Kind: domain.SourceGit, URL: "file://" + bare, Ref: "main"}

	return bare, src, func() *Builder { return NewBuilder(ws).WithGit(mgr) }, env
}

// Without a fetch, a clone taken before the package was pushed keeps
// failing -- this is the bug, pinned so it cannot come back silently.
func TestBuild_StaleCloneWithoutFetchKeepsFailing(t *testing.T) {
	bare, src, newBuilder, env := gitFixture(t)
	ref := domain.ServiceRef{Name: "flow", Source: src}

	// Clone now, while the repo has no api/ package.
	_, err := newBuilder().Build(context.Background(), ref)
	require.Error(t, err)

	// The package lands upstream a moment later.
	pushFiles(t, bare, env, map[string]string{"api/openapi.yaml": testContract}, "sapien docs")

	_, err = newBuilder().Build(context.Background(), ref)
	require.Error(t, err, "Ensure never fetches, so the clone still cannot see api/")
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
}

// With WithGitFetch, the same build picks the package up.
func TestBuild_GitFetchSeesAPackagePushedAfterTheClone(t *testing.T) {
	bare, src, newBuilder, env := gitFixture(t)
	ref := domain.ServiceRef{Name: "flow", Source: src}

	_, err := newBuilder().Build(context.Background(), ref)
	require.Error(t, err)

	pushFiles(t, bare, env, map[string]string{"api/openapi.yaml": testContract}, "sapien docs")

	snap, err := newBuilder().WithGitFetch().Build(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "flow", snap.Service.Name)
	require.Len(t, snap.Operations, 1)
	assert.Equal(t, "flow.listThings", snap.Operations[0].ID)
}

// A repo that genuinely has no package still fails -- but the error now
// says which commit was read and how current it is, instead of only naming
// a path inside the clone cache.
func TestBuild_MissingPackageErrorDescribesTheClone(t *testing.T) {
	_, src, newBuilder, _ := gitFixture(t)
	ref := domain.ServiceRef{Name: "flow", Source: src}

	_, err := newBuilder().Build(context.Background(), ref)
	require.Error(t, err)

	e := errs.As(err)
	assert.Equal(t, src.URL, e.Details["url"])
	assert.Equal(t, "main", e.Details["ref"])
	assert.NotEmpty(t, e.Details["commit"])
	// This clone's view of the remote is frozen at clone time, which is the
	// whole explanation, so the hint has to say so rather than leaving the
	// reader to conclude the package was never written.
	assert.Equal(t, false, e.Details["current"])
	assert.Contains(t, e.Hint, "has not been fetched since")
	assert.Contains(t, e.Hint, "sapien service sync")
}

// After a fetch the clone is current, so the error must stop blaming
// staleness and say the commit really has no package.
func TestBuild_MissingPackageAfterFetchBlamesTheCommit(t *testing.T) {
	_, src, newBuilder, _ := gitFixture(t)
	ref := domain.ServiceRef{Name: "flow", Source: src}

	_, err := newBuilder().WithGitFetch().Build(context.Background(), ref)
	require.Error(t, err)

	e := errs.As(err)
	assert.Equal(t, true, e.Details["current"])
	assert.Contains(t, e.Hint, "really does not carry this package")
	assert.NotContains(t, e.Hint, "has not been fetched since")
}

// A local source's error is untouched: there is no clone to describe.
func TestBuild_LocalSourceErrorIsNotDecorated(t *testing.T) {
	dir := t.TempDir()
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: dir}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "svc"), 0o755))

	_, err := NewBuilder(ws).Build(context.Background(), domain.ServiceRef{
		Name:   "svc",
		Source: domain.Source{Kind: domain.SourceLocal, Path: filepath.Join(dir, "svc")},
	})
	require.Error(t, err)

	e := errs.As(err)
	assert.Nil(t, e.Details["url"])
	assert.NotContains(t, e.Hint, "has not been fetched since")
}

// The syncer fetches itself and then builds; a missing package there must
// not be blamed on staleness, since the clone was just updated.
func TestBuild_GitSyncedIsTreatedAsCurrent(t *testing.T) {
	_, src, newBuilder, _ := gitFixture(t)
	ref := domain.ServiceRef{Name: "flow", Source: src}

	_, err := newBuilder().WithGitSynced().Build(context.Background(), ref)
	require.Error(t, err)

	e := errs.As(err)
	assert.Equal(t, true, e.Details["current"])
	assert.Contains(t, e.Hint, "really does not carry this package")
}

// The stale-clone hint has to read as a sentence: it is the whole value of
// the message.
func TestBuild_StaleHintReadsCleanly(t *testing.T) {
	_, src, newBuilder, _ := gitFixture(t)

	_, err := newBuilder().Build(context.Background(), domain.ServiceRef{Name: "flow", Source: src})
	require.Error(t, err)

	hint := errs.As(err).Hint
	assert.NotContains(t, hint, "since as of")
	assert.Regexp(t, `has not been fetched since (\d{4}-|it was cloned)`, hint)
}
