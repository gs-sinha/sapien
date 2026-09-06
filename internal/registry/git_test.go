package registry_test

// Git-backed registry tests (PLAN §18, Phase 5): Builder.WithGit and
// Syncer.WithGit against real, local, hermetic git fixtures -- a bare
// repository created with `git init --bare`, seeded from a working clone,
// and addressed by its file:// URL. Nothing here touches the network.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/gitsrc"
	"github.com/gs-sinha/sapien/internal/registry"
)

// hermeticGitEnv returns the extra environment every git invocation in
// these tests uses (both the gitsrc.Manager under test and the fixture
// helpers below), so nothing here depends on -- or mutates -- the
// developer's real git configuration.
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

// newBareRepo creates a local bare repository (HEAD symbolically pointing
// at refs/heads/main) and returns its filesystem path and file:// URL.
func newBareRepo(t *testing.T, env []string) (dir, url string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "repo.git")
	runGit(t, "", env, "init", "--bare", "-b", "main", dir)
	return dir, "file://" + dir
}

// copyDir recursively copies src's contents into dst.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dst, 0o755))
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, s, d)
			continue
		}
		data, rerr := os.ReadFile(s)
		require.NoError(t, rerr)
		require.NoError(t, os.WriteFile(d, data, 0o644))
	}
}

// pushDir clones bareDir, replaces its api/ subtree with srcDir's contents,
// commits (a marker file carrying msg guarantees a real diff even when
// srcDir's own content is unchanged from a previous call), and pushes. It
// returns the resulting commit sha.
func pushDir(t *testing.T, bareDir string, env []string, srcDir, msg string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	copyDir(t, srcDir, filepath.Join(work, "api"))
	require.NoError(t, os.WriteFile(filepath.Join(work, "api", ".sync-marker"), []byte(msg), 0o644))
	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", msg)
	runGit(t, work, env, "push", "origin", "HEAD")
	return runGit(t, work, env, "rev-parse", "HEAD")
}

func gitManager(t *testing.T, env []string) *gitsrc.Manager {
	t.Helper()
	return gitsrc.New(gitsrc.Options{
		CacheDir: filepath.Join(t.TempDir(), "repos"),
		Timeout:  10 * time.Second,
		Env:      env,
	})
}

func TestBuilder_Build_GitSource(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	srcAPI := filepath.Join(fixturesRoot(t), "order-service", "api")
	sha := pushDir(t, bareDir, env, srcAPI, "seed order-service")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	b := registry.NewBuilder(ws).WithGit(gitManager(t, env))

	ref := domain.ServiceRef{
		Name:   "order-service",
		Source: domain.Source{Kind: domain.SourceGit, URL: url},
	}
	snap, err := b.Build(context.Background(), ref)
	require.NoError(t, err)
	require.NotNil(t, snap)

	assert.Equal(t, "order-service", snap.Service.Name)
	assert.Equal(t, domain.SyncOK, snap.Service.Status)
	assert.Equal(t, sha, snap.Service.Commit)
	assert.Equal(t, domain.SourceGit, snap.Service.Source.Kind)
	assert.Greater(t, len(snap.Operations), 0)
	assert.Equal(t, len(snap.Operations), snap.Service.OperationCount)
}

func TestBuilder_Build_GitSource_EnsureFailure(t *testing.T) {
	env := hermeticGitEnv(t)
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	b := registry.NewBuilder(ws).WithGit(gitManager(t, env))

	ref := domain.ServiceRef{
		Name:   "broken",
		Source: domain.Source{Kind: domain.SourceGit, URL: "file:///no/such/repo/here.git"},
	}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
}

func TestPackageFingerprint_WithExtra_ChangesHash(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), minimalOpenAPI)

	pkg, err := registry.DiscoverPackage(root, "")
	require.NoError(t, err)

	base, err := registry.PackageFingerprint(pkg)
	require.NoError(t, err)

	withCommitA, err := registry.PackageFingerprint(pkg, "commit-a")
	require.NoError(t, err)
	withCommitB, err := registry.PackageFingerprint(pkg, "commit-b")
	require.NoError(t, err)

	assert.NotEqual(t, base, withCommitA, "an extra input must change the fingerprint")
	assert.NotEqual(t, withCommitA, withCommitB, "different extras must fingerprint differently")

	again, err := registry.PackageFingerprint(pkg, "commit-a")
	require.NoError(t, err)
	assert.Equal(t, withCommitA, again, "the same extra must fingerprint the same way")
}

func TestSyncer_WithGit_SyncOne_PicksUpPushedChange(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	srcAPI := filepath.Join(fixturesRoot(t), "order-service", "api")
	firstSHA := pushDir(t, bareDir, env, srcAPI, "seed")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	ws.Services = []domain.ServiceRef{
		{Name: "order-service", Source: domain.Source{Kind: domain.SourceGit, URL: url}},
	}

	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil).WithGit(gitManager(t, env))

	svc, err := s.SyncOne(context.Background(), "order-service")
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, firstSHA, svc.Commit)
	require.Len(t, idx.applied, 1)

	secondSHA := pushDir(t, bareDir, env, srcAPI, "update")
	require.NotEqual(t, firstSHA, secondSHA)

	svc2, err := s.SyncOne(context.Background(), "order-service")
	require.NoError(t, err)
	assert.Equal(t, secondSHA, svc2.Commit, "SyncOne must fetch before building")
	require.Len(t, idx.applied, 2)
}

func TestSyncer_SyncOne_GitFetchFailure(t *testing.T) {
	env := hermeticGitEnv(t)
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	ws.Services = []domain.ServiceRef{
		{Name: "broken-git", Source: domain.Source{Kind: domain.SourceGit, URL: "file:///no/such/repo/here.git"}},
	}

	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil).WithGit(gitManager(t, env))

	svc, err := s.SyncOne(context.Background(), "broken-git")
	require.Error(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncError, svc.Status)
	assert.NotEmpty(t, svc.Error)
	require.Len(t, idx.errored, 1)
}

func TestSyncer_Remove_RemovesGitClone_WhenNoLongerUsed(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	srcAPI := filepath.Join(fixturesRoot(t), "order-service", "api")
	pushDir(t, bareDir, env, srcAPI, "seed")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	ws.Services = []domain.ServiceRef{
		{Name: "order-service", Source: domain.Source{Kind: domain.SourceGit, URL: url}},
	}

	idx := &fakeIndexer{}
	m := gitManager(t, env)
	s := registry.NewSyncer(ws, idx, nil).WithGit(m)

	_, err := s.SyncOne(context.Background(), "order-service")
	require.NoError(t, err)

	clonedDir := m.Dir(url)
	require.DirExists(t, clonedDir)

	// The usual caller order: the service is already dropped from the
	// workspace file before Syncer.Remove is called.
	ws.Services = nil

	require.NoError(t, s.Remove(context.Background(), "order-service"))
	assert.NoDirExists(t, clonedDir)
	assert.Equal(t, []string{"order-service"}, idx.removed)
}

func TestSyncer_Remove_KeepsGitClone_WhenSharedURL(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	srcAPI := filepath.Join(fixturesRoot(t), "order-service", "api")
	pushDir(t, bareDir, env, srcAPI, "seed")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	ws.Services = []domain.ServiceRef{
		{Name: "order-service", Source: domain.Source{Kind: domain.SourceGit, URL: url}},
		{Name: "order-service-2", Source: domain.Source{Kind: domain.SourceGit, URL: url}},
	}

	idx := &fakeIndexer{}
	m := gitManager(t, env)
	s := registry.NewSyncer(ws, idx, nil).WithGit(m)

	_, err := s.SyncOne(context.Background(), "order-service")
	require.NoError(t, err)

	clonedDir := m.Dir(url)
	require.DirExists(t, clonedDir)

	require.NoError(t, s.Remove(context.Background(), "order-service"))
	assert.DirExists(t, clonedDir, "a clone still referenced by another service must be kept")
}

func TestSyncer_Remove_NonGitService_NoGitManager(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: fixturesRoot(t)}
	ws.Services = []domain.ServiceRef{fixtureRef("allocation-service")}

	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil) // no WithGit

	_, err := s.SyncOne(context.Background(), "allocation-service")
	require.NoError(t, err)

	require.NoError(t, s.Remove(context.Background(), "allocation-service"))
	assert.Equal(t, []string{"allocation-service"}, idx.removed)
}

func TestSyncer_SyncGitPeriodically_RunsThenStopsOnCancel(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	srcAPI := filepath.Join(fixturesRoot(t), "order-service", "api")
	pushDir(t, bareDir, env, srcAPI, "seed")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	ws.Services = []domain.ServiceRef{
		{Name: "order-service", Source: domain.Source{Kind: domain.SourceGit, URL: url}},
	}

	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil).WithGit(gitManager(t, env))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.SyncGitPeriodically(ctx, 50*time.Millisecond)
		close(done)
	}()

	require.Eventually(t, func() bool {
		idx.mu.Lock()
		defer idx.mu.Unlock()
		return len(idx.applied) >= 1
	}, 3*time.Second, 10*time.Millisecond, "expected at least one sync cycle")

	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SyncGitPeriodically did not return promptly after ctx cancel")
	}
}

func TestSyncer_SyncGitPeriodically_NoGitManager_ReturnsOnCancel(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil) // no WithGit

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.SyncGitPeriodically(ctx, 10*time.Millisecond)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SyncGitPeriodically did not return promptly after ctx cancel")
	}
}
