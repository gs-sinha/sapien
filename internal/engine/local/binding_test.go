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
	"github.com/gs-sinha/sapien/internal/engine"
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

// --- BindWith: name inference and validation (PLAN §7b) ---

func TestServiceBindWith_InfersNameFromOrigin(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	clone := developerClone(t, env, bareDir, "")
	svc, err := l.Services().BindWith(ctx, "", clone, engine.BindOptions{})
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, "order-service", svc.Name)
	require.NotNil(t, svc.Binding)
	assert.Equal(t, domain.BindingLocal, svc.Binding.Mode)
}

func TestServiceBindWith_InferNoMatchIsInvalid(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	otherBare, _ := newBareRepo(t, env)
	commitAndPush(t, otherBare, env, readFixtureAPIFiles(t, "rider-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	unrelated := developerClone(t, env, otherBare, "")
	_, err = l.Services().BindWith(ctx, "", unrelated, engine.BindOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	assert.Contains(t, e.Message, "no registered service is cloned from")
	assert.NotEmpty(t, e.Hint)
}

func TestServiceBindWith_InferAmbiguousIsConflict(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "order-service-mirror",
		Source: domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"},
	}))
	require.NoError(t, workspace.Save(ws))

	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	clone := developerClone(t, env, bareDir, "")
	_, err = l.Services().BindWith(ctx, "", clone, engine.BindOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	e := errs.As(err)
	assert.Contains(t, e.Message, "order-service")
	assert.Contains(t, e.Message, "order-service-mirror")
}

func TestServiceBindWith_OriginMismatchIsInvalidUnlessForced(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	otherBare, _ := newBareRepo(t, env)
	commitAndPush(t, otherBare, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	mismatched := developerClone(t, env, otherBare, "")
	_, err = l.Services().BindWith(ctx, "order-service", mismatched, engine.BindOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	// Exact message, since the UI surfaces it verbatim: "checkout at <path>
	// is a clone of <origin>, not of <team url>".
	assert.Equal(t, "checkout at "+mismatched+" is a clone of "+otherBare+", not of "+bareURL, e.Message)
	assert.Contains(t, e.Hint, "--force")

	svc, err := l.Services().BindWith(ctx, "order-service", mismatched, engine.BindOptions{Force: true})
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.BindingLocal, svc.Binding.Mode)
}

func TestServiceBindWith_NoPackageIsInvalidUnlessForced(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	clone := developerClone(t, env, bareDir, "")
	require.NoError(t, os.RemoveAll(filepath.Join(clone, "api")))

	_, err = l.Services().BindWith(ctx, "order-service", clone, engine.BindOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	assert.Equal(t, "no API package under "+clone, e.Message)
	assert.Contains(t, e.Hint, "--force")

	svc, err := l.Services().BindWith(ctx, "order-service", clone, engine.BindOptions{Force: true})
	// The override is still recorded even though the resync itself fails --
	// the same "keep it, fix and resync" contract Bind already has for a
	// checkout that fails to index (see resyncRebound's doc comment) -- but
	// the resync failure is still returned alongside it, and a Service that
	// never successfully built carries no Binding.
	require.Error(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncError, svc.Status)
	assert.Equal(t, domain.SourceLocal, svc.Source.Kind)
	assert.Equal(t, clone, svc.Source.Path)

	reloaded, err := workspace.Load(ws.File)
	require.NoError(t, err)
	require.Len(t, reloaded.Services, 1)
	assert.Equal(t, clone, reloaded.Services[0].Source.Path, "the override was recorded despite the sync failure")
}

func TestServiceBindWith_NotAGitRepoIsInvalidUnlessForced(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	plain := t.TempDir()
	for rel, content := range readFixtureAPIFiles(t, "order-service") {
		p := filepath.Join(plain, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	_, err = l.Services().BindWith(ctx, "order-service", plain, engine.BindOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	assert.Equal(t, plain+" is not a git repository", e.Message)
	assert.Contains(t, e.Hint, "--force")

	svc, err := l.Services().BindWith(ctx, "order-service", plain, engine.BindOptions{Force: true})
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncOK, svc.Status, "a plain directory with a real package indexes fine once forced")
}

// --- AddFromCheckout (PLAN §7b) ---

func TestServiceAddFromCheckout(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	// The bare "team" repo starts with nothing at all -- no api/ package --
	// a brand-new hire's situation: the repository exists, but nobody has
	// pushed this service yet.
	commitAndPush(t, bareDir, env, map[string]string{"README.md": "root\n"}, "init")

	ws := newGitTestWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	clone := filepath.Join(t.TempDir(), "order-service")
	// Cloned from the file:// URL, not the bare directory's own path:
	// AddFromCheckout commits the origin verbatim, and a real remote
	// (GitHub, GitLab, ...) always has a scheme -- a bare filesystem path
	// as an origin is not a URL gitsrc.Manager can later clone from.
	runGit(t, "", env, "clone", bareURL, clone)
	for rel, content := range readFixtureAPIFiles(t, "order-service") {
		p := filepath.Join(clone, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	// Deliberately never committed or pushed: AddFromCheckout must still
	// index the service, because the bound checkout is read directly.
	svc, err := l.Services().AddFromCheckout(ctx, "", clone, engine.AddFromCheckoutOptions{Ref: "main"})
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, "order-service", svc.Name, "derived the same way Add derives an unnamed service's name")
	assert.Equal(t, domain.SyncOK, svc.Status)
	require.NotNil(t, svc.Binding)
	assert.Equal(t, domain.BindingLocal, svc.Binding.Mode)
	require.NotNil(t, svc.Binding.Team)
	assert.Equal(t, domain.SourceGit, svc.Binding.Team.Kind)
	assert.Equal(t, bareURL, svc.Binding.Team.URL)
	assert.Equal(t, "main", svc.Binding.Team.Ref)
	require.NotNil(t, svc.Binding.Local)
	assert.Equal(t, clone, svc.Binding.Local.Path)

	committed, err := os.ReadFile(ws.File)
	require.NoError(t, err)
	assert.Contains(t, string(committed), "type: git")
	assert.Contains(t, string(committed), bareURL)
	assert.NotContains(t, string(committed), clone, "the committed file must never carry a local path")

	localFile, err := os.ReadFile(workspace.LocalOverridePath(ws))
	require.NoError(t, err)
	assert.Contains(t, string(localFile), clone)

	ops, err := l.Catalog().ListOperations(ctx, "order-service")
	require.NoError(t, err)
	assert.Len(t, ops, 5)

	// The developer commits and pushes their branch: the team's managed
	// clone can now see the package too, so unbinding back to the
	// committed git source still syncs cleanly.
	runGit(t, clone, env, "add", "-A")
	runGit(t, clone, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "add order-service")
	runGit(t, clone, env, "push", "origin", "HEAD:main")

	restored, err := l.Services().Unbind(ctx, "order-service")
	require.NoError(t, err)
	require.NotNil(t, restored)
	assert.Equal(t, domain.SyncOK, restored.Status)
	assert.Equal(t, domain.SourceGit, restored.Source.Kind)
	assert.Equal(t, bareURL, restored.Source.URL)
}

func TestServiceAddFromCheckout_NoOriginIsInvalidWithLocalAddDetail(t *testing.T) {
	ws := newGitTestWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	plain := t.TempDir()
	_, err = l.Services().AddFromCheckout(ctx, "x", plain, engine.AddFromCheckoutOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	assert.Contains(t, e.Message, "not a git checkout with an origin")
	assert.Contains(t, e.Message, "service add <path>")
	local, _ := e.Details["local_add"].(bool)
	assert.True(t, local, "the CLI/MCP use this detail to fall back to a plain local Add")
}

func TestServiceAddFromCheckout_Subdirectory(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)

	// A monorepo: order-service's api/ package lives under
	// services/order-service, not at the repository root.
	files := map[string]string{}
	for rel, content := range readFixtureAPIFiles(t, "order-service") {
		files[filepath.Join("services", "order-service", rel)] = content
	}
	commitAndPush(t, bareDir, env, files, "monorepo import")

	ws := newGitTestWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	clone := t.TempDir()
	runGit(t, "", env, "clone", bareURL, clone)
	subdir := filepath.Join(clone, "services", "order-service")

	_, err = l.Services().AddFromCheckout(ctx, "order-service", subdir, engine.AddFromCheckoutOptions{Ref: "main"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	assert.Contains(t, e.Message, "not its root")
	local, _ := e.Details["local_add"].(bool)
	assert.True(t, local)
	assert.Contains(t, e.Hint, "--team")

	svc, err := l.Services().AddFromCheckout(ctx, "order-service", subdir,
		engine.AddFromCheckoutOptions{Ref: "main", AllowSubdir: true})
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncOK, svc.Status)
	require.NotNil(t, svc.Binding)
	require.NotNil(t, svc.Binding.Team)
	assert.Equal(t, bareURL, svc.Binding.Team.URL)
	assert.Equal(t, filepath.ToSlash(filepath.Join("services", "order-service", "api")), svc.Binding.Team.Subdir)
	require.NotNil(t, svc.Binding.Local)
	assert.Equal(t, subdir, svc.Binding.Local.Path, "the bound path stays the given subdirectory, not the repo root")
}
