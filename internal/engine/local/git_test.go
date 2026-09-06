package local

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// The helpers below replicate the hermetic git test environment pattern
// from internal/gitsrc/testutil_test.go (a _test.go file, so it cannot be
// imported across packages): a throwaway HOME plus a forced-off global git
// config, and a small local bare-repo fixture builder, so these tests never
// touch the developer's real git configuration or the network.

// hermeticGitEnv returns the extra environment every git invocation in
// these tests uses (both the engine's own gitsrc.Manager, via
// Options.GitCacheDir + the process environment, and the fixture helpers
// below, via runGit's env parameter).
func hermeticGitEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"HOME=" + home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	}
}

func runGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s (dir=%q): %v\n%s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// newBareRepo creates a local bare repository (no commits yet, HEAD
// symbolically pointing at refs/heads/main) and returns its filesystem path
// and its file:// URL.
func newBareRepo(t *testing.T, env []string) (dir, url string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "repo.git")
	runGit(t, "", env, "init", "--bare", "-b", "main", dir)
	return dir, "file://" + dir
}

// commitAndPush clones bareDir into a fresh working directory, writes files
// (path relative to the repo root -> content, creating parent directories
// as needed), commits them with a fixed test identity, and pushes to
// whichever branch is currently checked out (bareDir's default branch on
// the first call). It returns the new commit's sha.
func commitAndPush(t *testing.T, bareDir string, env []string, files map[string]string, msg string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)

	for rel, content := range files {
		p := filepath.Join(work, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", msg)
	runGit(t, work, env, "push", "origin", "HEAD")
	return runGit(t, work, env, "rev-parse", "HEAD")
}

// fixturesRootForGit mirrors fixturesRoot (local_test.go) but is redefined
// here for clarity at the call sites in this file.
func fixturesRootForGit(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "fixtures", "logistics")
}

// readFixtureAPIFiles reads every file under
// fixtures/logistics/<service>/api/ and returns them keyed by their path
// relative to the service root (i.e. "api/openapi.yaml", "api/docs/x.md"),
// ready to hand to commitAndPush -- so a git-service test's bare repo
// carries the exact same "api/" package a local-source test would use.
func readFixtureAPIFiles(t *testing.T, service string) map[string]string {
	t.Helper()
	root := filepath.Join(fixturesRootForGit(t), service)
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	require.NoError(t, err)
	return out
}

// newGitTestWorkspace initializes a fresh, empty workspace (no services
// registered) in a temp dir, isolating $SAPIEN_CONFIG the same way
// setupWorkspace does.
func newGitTestWorkspace(t *testing.T) *domain.Workspace {
	t.Helper()
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	ws, err := workspace.Init(t.TempDir(), "git-ws")
	require.NoError(t, err)
	return ws
}

func TestGitService_Add_DerivesNameAndIndexes(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := newGitTestWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svc, err := l.Services().Add(ctx, "", domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"})
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, "order-service", svc.Name)
	assert.Equal(t, domain.SyncOK, svc.Status)
	assert.NotEmpty(t, svc.Commit)

	ops, err := l.Catalog().ListOperations(ctx, "order-service")
	require.NoError(t, err)
	assert.Len(t, ops, 5)

	// The workspace file records a git source, not a resolved local path.
	got, err := l.Services().Get(ctx, "order-service")
	require.NoError(t, err)
	assert.Equal(t, domain.SourceGit, got.Source.Kind)
	assert.Equal(t, bareURL, got.Source.URL)
}

func TestGitService_Add_UnknownRefIsAServiceSourceError(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := newGitTestWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svc, err := l.Services().Add(ctx, "order-service",
		domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "no-such-branch"})
	require.Error(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncError, svc.Status)
}

func TestGitService_Sync_PicksUpPushedChangeAndCommitMoves(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	files := readFixtureAPIFiles(t, "order-service")
	firstCommit := commitAndPush(t, bareDir, env, files, "initial import")

	ws := newGitTestWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svc, err := l.Services().Add(ctx, "order-service", domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"})
	require.NoError(t, err)
	assert.Equal(t, firstCommit, svc.Commit)

	op, err := l.Catalog().GetOperation(ctx, "order-service.createOrder")
	require.NoError(t, err)
	originalSummary := op.Summary

	updatedOpenAPI := strings.Replace(files["api/openapi.yaml"], "summary: Create a new order", "summary: Create a brand-new order", 1)
	require.NotEqual(t, files["api/openapi.yaml"], updatedOpenAPI, "expected to find and replace the summary line")
	secondCommit := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": updatedOpenAPI}, "tweak summary")
	require.NotEqual(t, firstCommit, secondCommit)

	svcs, err := l.Services().Sync(ctx, "order-service")
	require.NoError(t, err)
	require.Len(t, svcs, 1)
	assert.Equal(t, secondCommit, svcs[0].Commit)
	assert.Equal(t, domain.SyncOK, svcs[0].Status)

	op2, err := l.Catalog().GetOperation(ctx, "order-service.createOrder")
	require.NoError(t, err)
	assert.NotEqual(t, originalSummary, op2.Summary)
	assert.Contains(t, op2.Summary, "brand-new")
}

func TestGitService_Watch_PeriodicSyncPicksUpPush(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	files := readFixtureAPIFiles(t, "order-service")
	commitAndPush(t, bareDir, env, files, "initial import")

	ws := newGitTestWorkspace(t)
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "order-service",
		Source: domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"},
	}))
	require.NoError(t, workspace.Save(ws))

	l, err := Open(ws, Options{
		Watch:           true,
		GitCacheDir:     t.TempDir(),
		GitSyncInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()
	svc, err := l.Services().Get(ctx, "order-service")
	require.NoError(t, err)
	require.Equal(t, domain.SyncOK, svc.Status, "the initial staleCheck sync at Open should have succeeded")

	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	updatedOpenAPI := strings.Replace(files["api/openapi.yaml"], "summary: Create a new order", "summary: Create a brand-new order", 1)
	require.NotEqual(t, files["api/openapi.yaml"], updatedOpenAPI)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": updatedOpenAPI}, "tweak summary")

	// The task-level requirement is "within 3s" (PLAN §18's daemon timer,
	// exercised here with a 100ms GitSyncInterval so it fires many times
	// over); the deadline below is more generous purely to absorb scheduler
	// jitter when this suite runs alongside many other CPU-heavy test
	// binaries (observed in practice under concurrent agents sharing this
	// checkout), not because the feature itself needs longer than 3s.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != domain.EventCatalogChanged {
				continue
			}
			change, ok := ev.Payload.(domain.CatalogChange)
			if !ok || change.Service != "order-service" {
				continue
			}
			// A catalog.changed for this service may come from the initial
			// watch resync before the periodic fetch has picked up the push;
			// keep waiting until the pushed change is actually visible.
			op, err := l.Catalog().GetOperation(ctx, "order-service.createOrder")
			if err == nil && strings.Contains(op.Summary, "brand-new") {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for catalog.changed after a push, with GitSyncInterval=100ms")
		}
	}
}

func TestGitService_StaleCheck_DoesNotFetchOnOpen(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := newGitTestWorkspace(t)
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "order-service",
		Source: domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"},
	}))
	require.NoError(t, workspace.Save(ws))

	sharedCacheDir := t.TempDir()

	l1, err := Open(ws, Options{GitCacheDir: sharedCacheDir})
	require.NoError(t, err)
	ctx := context.Background()
	svc1, err := l1.Services().Get(ctx, "order-service")
	require.NoError(t, err)
	require.Equal(t, domain.SyncOK, svc1.Status)
	require.NoError(t, l1.Close())

	// Make the origin unreachable: rename the bare repo out of the way.
	// Any code path that tried to `git fetch` (or re-clone) it now would
	// fail loudly; staleCheck must never take that path on an unchanged,
	// already-cloned git service.
	require.NoError(t, os.Rename(bareDir, bareDir+"-moved"))

	l2, err := Open(ws, Options{GitCacheDir: sharedCacheDir})
	require.NoError(t, err, "Open must succeed from the cached catalog even though the origin is now unreachable")
	defer l2.Close()

	svc2, err := l2.Services().Get(ctx, "order-service")
	require.NoError(t, err)
	assert.Equal(t, domain.SyncOK, svc2.Status)
	assert.Equal(t, svc1.LastIndexed, svc2.LastIndexed, "no resync should have happened: the fingerprint did not change")

	ops, err := l2.Catalog().ListOperations(ctx, "order-service")
	require.NoError(t, err)
	assert.Len(t, ops, 5, "the cached catalog should still be fully served")
}

func TestGitService_Remove_CleansUpManagedClone(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	cacheDir := t.TempDir()
	ws := newGitTestWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: cacheDir})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svc, err := l.Services().Add(ctx, "order-service", domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"})
	require.NoError(t, err)
	require.Equal(t, domain.SyncOK, svc.Status)

	cloneDir := l.gitMgr.Dir(bareURL)
	require.DirExists(t, cloneDir)

	require.NoError(t, l.Services().Remove(ctx, "order-service"))

	_, err = os.Stat(cloneDir)
	assert.True(t, os.IsNotExist(err), "Remove should have deleted the managed clone")

	_, err = l.Services().Get(ctx, "order-service")
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
}
