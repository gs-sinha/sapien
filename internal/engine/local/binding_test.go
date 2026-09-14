package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// Binding tests (PLAN §7b) reuse git_test.go's hermetic fixtures: a local
// bare repository carrying the order-service fixture's api/ package,
// registered as a git source, and a plain clone of it standing in for a
// developer's own checkout.

// teamWorkspace registers the bare repo at bareURL as the git-sourced
// "order-service" in a fresh workspace.
func teamWorkspace(t *testing.T, bareURL string) *domain.Workspace {
	t.Helper()
	ws := newGitTestWorkspace(t)
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "order-service",
		Source: domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"},
	}))
	require.NoError(t, workspace.Save(ws))
	return ws
}

// developerClone clones bareDir the way a developer would (no sparse
// checkout, a feature branch checked out) and returns its path.
func developerClone(t *testing.T, env []string, bareDir, branch string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "order-service")
	runGit(t, "", env, "clone", bareDir, clone)
	if branch != "" {
		runGit(t, clone, env, "checkout", "-b", branch)
	}
	return clone
}

func serviceMemory(service string) domain.Memory {
	return domain.Memory{
		Text:    "createOrder rejects a QCOM order without a pickup pincode.",
		Scope:   domain.ScopeService,
		Subject: domain.Subject{Service: service},
	}
}

func TestServiceBind_ReadsFromCheckoutAndUnbindRestoresTeamSource(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	// Team mode: read from the managed clone, nothing writable.
	before, err := l.Services().Get(ctx, "order-service")
	require.NoError(t, err)
	require.NotNil(t, before.Binding)
	assert.Equal(t, domain.BindingTeam, before.Binding.Mode)
	assert.False(t, before.Binding.Writable)

	_, err = l.Memories().Create(ctx, serviceMemory("order-service"))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Hint, "sapien service bind")

	clone := developerClone(t, env, bareDir, "feature/qcom")

	svc, err := l.Services().Bind(ctx, "order-service", clone)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncOK, svc.Status)
	assert.Equal(t, domain.SourceLocal, svc.Source.Kind)
	assert.Equal(t, clone, svc.Source.Path)
	assert.Equal(t, filepath.Join(clone, "api"), svc.PackageDir, "the service is read from the clone now")
	require.NotNil(t, svc.Binding)
	assert.Equal(t, domain.BindingLocal, svc.Binding.Mode)
	assert.True(t, svc.Binding.Writable)
	require.NotNil(t, svc.Binding.Team)
	assert.Equal(t, domain.SourceGit, svc.Binding.Team.Kind)
	assert.Equal(t, bareURL, svc.Binding.Team.URL)
	require.NotNil(t, svc.Binding.Local)
	assert.Equal(t, clone, svc.Binding.Local.Path)
	assert.Equal(t, "feature/qcom", svc.Binding.Local.Branch)
	assert.NotEmpty(t, svc.Binding.Local.Commit)

	// On disk: the override file exists, the committed file still says git
	// and never mentions the clone, and the override is gitignored.
	assert.FileExists(t, workspace.LocalOverridePath(ws))
	committed, err := os.ReadFile(ws.File)
	require.NoError(t, err)
	assert.Contains(t, string(committed), "type: git")
	assert.NotContains(t, string(committed), clone)
	ignore, err := os.ReadFile(filepath.Join(ws.Dir, ".gitignore"))
	require.NoError(t, err)
	assert.Contains(t, string(ignore), domain.WorkspaceLocalFileName)

	// A fresh Load (what the next CLI invocation or daemon does) sees the
	// override and still knows the committed source.
	reloaded, err := workspace.Load(ws.File)
	require.NoError(t, err)
	require.Len(t, reloaded.Services, 1)
	assert.Equal(t, domain.SourceLocal, reloaded.Services[0].Source.Kind)
	require.NotNil(t, reloaded.Services[0].Team)
	assert.Equal(t, bareURL, reloaded.Services[0].Team.URL)

	// The catalog agrees, and service-scoped knowledge now lands in the
	// clone's api/memories, on the developer's own branch.
	got, err := l.Services().Get(ctx, "order-service")
	require.NoError(t, err)
	require.NotNil(t, got.Binding)
	assert.Equal(t, domain.BindingLocal, got.Binding.Mode)

	mem, err := l.Memories().Create(ctx, serviceMemory("order-service"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(mem.FilePath, filepath.Join(clone, "api", domain.MemoriesDir)),
		"memory %q should live under the clone's api/memories", mem.FilePath)

	// Unbind: back to the managed clone, read-only again, override gone.
	restored, err := l.Services().Unbind(ctx, "order-service")
	require.NoError(t, err)
	require.NotNil(t, restored)
	assert.Equal(t, domain.SyncOK, restored.Status)
	assert.Equal(t, domain.SourceGit, restored.Source.Kind)
	assert.Equal(t, bareURL, restored.Source.URL)
	require.NotNil(t, restored.Binding)
	assert.Equal(t, domain.BindingTeam, restored.Binding.Mode)
	assert.False(t, restored.Binding.Writable)
	assert.NoFileExists(t, workspace.LocalOverridePath(ws))

	_, err = l.Memories().Create(ctx, serviceMemory("order-service"))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Hint, "sapien service bind")

	// Nothing left to unbind.
	_, err = l.Services().Unbind(ctx, "order-service")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestServiceBind_RejectsBadInput(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Services().Bind(ctx, "order-service", filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.NotEmpty(t, errs.As(err).Hint)

	file := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	_, err = l.Services().Bind(ctx, "order-service", file)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	_, err = l.Services().Bind(ctx, "no-such-service", t.TempDir())
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))

	// A rejected bind leaves no override behind.
	assert.NoFileExists(t, workspace.LocalOverridePath(ws))
	assert.Nil(t, ws.Services[0].Team)
}

// The daemon's watcher was built from the managed clone; after a bind it
// has to follow the checkout, or edits there would only be seen on the
// next explicit sync.
func TestServiceBind_WatcherFollowsTheCheckout(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	files := readFixtureAPIFiles(t, "order-service")
	commitAndPush(t, bareDir, env, files, "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{Watch: true, GitCacheDir: t.TempDir(), GitSyncInterval: time.Hour})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	clone := developerClone(t, env, bareDir, "")
	_, err = l.Services().Bind(ctx, "order-service", clone)
	require.NoError(t, err)

	updated := strings.Replace(files["api/openapi.yaml"], "summary: Create a new order", "summary: Create a brand-new order", 1)
	require.NotEqual(t, files["api/openapi.yaml"], updated)
	require.NoError(t, os.WriteFile(filepath.Join(clone, "api", "openapi.yaml"), []byte(updated), 0o644))

	require.Eventually(t, func() bool {
		op, err := l.Catalog().GetOperation(ctx, "order-service.createOrder")
		return err == nil && strings.Contains(op.Summary, "brand-new")
	}, 10*time.Second, 100*time.Millisecond, "an edit in the bound checkout must be picked up by the restarted watcher")
}

func TestServiceBinding_ListsCheckoutsOfTheSameRepository(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL) // also points $SAPIEN_CONFIG at a temp file

	// Another registered workspace on this machine reads the same
	// repository from a developer clone, and an unrelated one from another
	// repository; only the first is a candidate.
	clone := developerClone(t, env, bareDir, "")
	otherBare, _ := newBareRepo(t, env)
	commitAndPush(t, otherBare, env, readFixtureAPIFiles(t, "rider-service"), "initial import")
	unrelated := filepath.Join(t.TempDir(), "rider-service")
	runGit(t, "", env, "clone", otherBare, unrelated)

	other, err := workspace.Init(t.TempDir(), "other")
	require.NoError(t, err)
	require.NoError(t, workspace.AddService(other, domain.ServiceRef{
		Name: "orders", Source: domain.Source{Kind: domain.SourceLocal, Path: clone},
	}))
	require.NoError(t, workspace.AddService(other, domain.ServiceRef{
		Name: "riders", Source: domain.Source{Kind: domain.SourceLocal, Path: unrelated},
	}))
	require.NoError(t, workspace.Save(other))
	require.NoError(t, config.AddWorkspace(other.Dir))

	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	info, err := l.Services().Binding(ctx, "order-service")
	require.NoError(t, err)
	assert.Equal(t, "order-service", info.Service)
	assert.Equal(t, domain.BindingTeam, info.Binding.Mode)
	require.NotNil(t, info.Binding.Team)
	assert.Equal(t, bareURL, info.Binding.Team.URL)
	require.Len(t, info.Candidates, 1)
	assert.Equal(t, clone, info.Candidates[0].Path)
	assert.Equal(t, "main", info.Candidates[0].Branch)
	assert.NotEmpty(t, info.Candidates[0].Commit)

	// Once bound to that clone it is what the service reads, not an offer.
	_, err = l.Services().Bind(ctx, "order-service", clone)
	require.NoError(t, err)
	info, err = l.Services().Binding(ctx, "order-service")
	require.NoError(t, err)
	assert.Equal(t, domain.BindingLocal, info.Binding.Mode)
	require.NotNil(t, info.Binding.Local)
	assert.Equal(t, clone, info.Binding.Local.Path)
	assert.Empty(t, info.Candidates)

	_, err = l.Services().Binding(ctx, "no-such-service")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
}
