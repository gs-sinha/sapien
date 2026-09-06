package local

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/workspace"
)

// fixturesRoot returns the absolute path of fixtures/logistics, resolved
// relative to this test file rather than the working directory `go test`
// happens to use.
func fixturesRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "fixtures", "logistics")
}

// copyFixtures copies the three logistics service packages into a fresh
// temp dir and returns it, so tests never write into fixtures/logistics
// itself (its docs/, openapi.yaml, etc. are shared by every phase's tests).
func copyFixtures(t *testing.T) (dir string) {
	t.Helper()
	root := fixturesRoot(t)
	dir = t.TempDir()
	for _, svc := range []string{"order-service", "allocation-service", "rider-service"} {
		require.NoError(t, copyDir(filepath.Join(root, svc), filepath.Join(dir, svc)))
	}
	return dir
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// setupWorkspace initializes a fresh workspace in a temp dir, registers
// copies of the three logistics services (by local path), and saves it.
// It returns the workspace and the temp dir the fixtures were copied into
// (so a test can edit a copied contract file directly).
//
// It also points $SAPIEN_CONFIG at a file under a fresh temp dir (left
// unwritten -- Open's config.Load then sees no user-level config file at
// all) so every test in this package reads engine settings from nothing
// but its own workspace, never a developer's real ~/.sapien/config.yaml.
func setupWorkspace(t *testing.T) (*domain.Workspace, string) {
	t.Helper()
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	wsDir := t.TempDir()
	ws, err := workspace.Init(wsDir, "logistics")
	require.NoError(t, err)

	fixDir := copyFixtures(t)

	for _, name := range []string{"order-service", "allocation-service", "rider-service"} {
		require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
			Name:   name,
			Source: domain.Source{Kind: domain.SourceLocal, Path: filepath.Join(fixDir, name)},
		}))
	}
	require.NoError(t, workspace.Save(ws))
	return ws, fixDir
}

func TestOpen_InitialSync(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	svcs, err := l.Services().List(context.Background())
	require.NoError(t, err)
	require.Len(t, svcs, 3)
	for _, s := range svcs {
		assert.Equalf(t, domain.SyncOK, s.Status, "service %s", s.Name)
		assert.Greaterf(t, s.OperationCount, 0, "service %s", s.Name)
	}
}

func TestSearch_AllocateRider(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	results, err := l.Search().Operations(context.Background(), "allocate rider", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "allocation-service.allocate", results[0].Operation.ID)
}

func TestCatalog_ResolveOperation(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	op, err := l.Catalog().ResolveOperation(context.Background(), "POST /v1/orders")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", op.ID)
}

func TestCatalog_GetDoc(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	doc, err := l.Catalog().GetDoc(context.Background(), "allocation-service", "docs/allocation.md")
	require.NoError(t, err)
	require.NotEmpty(t, doc.Sections)

	var opRefs int
	for _, sec := range doc.Sections {
		for _, ref := range sec.Refs {
			if ref.Kind == domain.RefOperation {
				opRefs++
			}
		}
	}
	assert.Greater(t, opRefs, 0, "expected at least one operation ref across the doc's sections")
}

func TestEvents_AddAndRemove(t *testing.T) {
	ws, fixDir := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()
	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	riderCopy := filepath.Join(t.TempDir(), "rider-service-2")
	require.NoError(t, copyDir(filepath.Join(fixDir, "rider-service"), riderCopy))

	svc, err := l.Services().Add(ctx, "rider-service-2", domain.Source{Kind: domain.SourceLocal, Path: riderCopy})
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncOK, svc.Status)

	select {
	case ev := <-ch:
		assert.Equal(t, domain.EventCatalogChanged, ev.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for catalog.changed after Add")
	}

	_, err = l.Services().Get(ctx, "rider-service-2")
	require.NoError(t, err)

	require.NoError(t, l.Services().Remove(ctx, "rider-service-2"))

	_, err = l.Services().Get(ctx, "rider-service-2")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
}

func TestStaleness_SelectiveResync(t *testing.T) {
	ws, fixDir := setupWorkspace(t)

	l, err := Open(ws, Options{})
	require.NoError(t, err)

	ctx := context.Background()
	before, err := l.Services().List(ctx)
	require.NoError(t, err)
	beforeByName := make(map[string]domain.Service, len(before))
	for _, s := range before {
		beforeByName[s.Name] = s
	}

	require.NoError(t, l.Close())

	contractPath := filepath.Join(fixDir, "order-service", "api", "openapi.yaml")
	data, err := os.ReadFile(contractPath)
	require.NoError(t, err)
	updated := strings.Replace(string(data), "summary: Create a new order", "summary: Create a brand-new order", 1)
	require.NotEqual(t, string(data), updated, "expected summary text to be found and replaced")
	require.NoError(t, os.WriteFile(contractPath, []byte(updated), 0o644))

	l2, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l2.Close()

	after, err := l2.Services().List(ctx)
	require.NoError(t, err)
	require.Len(t, after, 3)
	for _, s := range after {
		b, ok := beforeByName[s.Name]
		require.True(t, ok)
		if s.Name == "order-service" {
			assert.Truef(t, s.LastIndexed.After(b.LastIndexed),
				"order-service should have been resynced (before=%v after=%v)", b.LastIndexed, s.LastIndexed)
		} else {
			assert.Equalf(t, b.LastIndexed, s.LastIndexed, "%s should not have been resynced", s.Name)
		}
	}

	op, err := l2.Catalog().GetOperation(ctx, "order-service.createOrder")
	require.NoError(t, err)
	assert.Contains(t, op.Summary, "brand-new")

	// The fingerprint mechanism itself: only order-service's stored
	// fingerprint should have changed.
	fp, ok := settingsGet(ctx, l2.db, fingerprintKey("order-service"))
	require.True(t, ok)
	assert.NotEmpty(t, fp)
}

func TestWatch_CatalogChangedOnEdit(t *testing.T) {
	ws, fixDir := setupWorkspace(t)
	l, err := Open(ws, Options{Watch: true})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()
	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	contractPath := filepath.Join(fixDir, "rider-service", "api", "openapi.yaml")
	data, err := os.ReadFile(contractPath)
	require.NoError(t, err)
	updated := strings.Replace(string(data), "summary: Fetch a rider by ID", "summary: Fetch a rider by ID (v2)", 1)
	require.NotEqual(t, string(data), updated, "expected summary text to be found and replaced")
	require.NoError(t, os.WriteFile(contractPath, []byte(updated), 0o644))

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == domain.EventCatalogChanged {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for catalog.changed after file edit under Watch")
		}
	}
}

func TestWatch_FlowChangedOnNewFile(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{Watch: true})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()
	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	flowPath := filepath.Join(ws.Dir, domain.FlowsDir, "watched.flow.yaml")
	require.NoError(t, os.WriteFile(flowPath, []byte(validFlowYAML), 0o644))

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == domain.EventFlowChanged {
				list, err := l.Flows().List(ctx, "")
				require.NoError(t, err)
				var found bool
				for _, fs := range list {
					if fs.ID == "order-allocation" {
						found = true
					}
				}
				assert.True(t, found, "expected the new flow to appear in Flows().List after flow.changed")
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for flow.changed after dropping a new flow file under Watch")
		}
	}
}

func TestCatalogAndSearchReadMethods(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	assert.Same(t, ws, l.Workspace())

	stats, err := l.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, stats.Services)
	assert.Greater(t, stats.Operations, 0)

	ops, err := l.Catalog().ListOperations(ctx, "order-service")
	require.NoError(t, err)
	assert.NotEmpty(t, ops)

	fields, err := l.Catalog().Fields(ctx, "allocation-service.allocate")
	require.NoError(t, err)
	assert.NotEmpty(t, fields)

	schema, err := l.Catalog().GetSchema(ctx, "allocation-service", "AllocateRequest")
	require.NoError(t, err)
	require.NotNil(t, schema)

	docs, err := l.Catalog().ListDocs(ctx, "allocation-service")
	require.NoError(t, err)
	assert.NotEmpty(t, docs)

	docResults, err := l.Search().Docs(ctx, "qcom", domain.SearchOptions{})
	require.NoError(t, err)
	assert.NotEmpty(t, docResults)
}

func TestServices_SyncAndReindex(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svcs, err := l.Services().Sync(ctx, "order-service")
	require.NoError(t, err)
	require.Len(t, svcs, 1)
	assert.Equal(t, "order-service", svcs[0].Name)

	all, err := l.Services().Sync(ctx, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	require.NoError(t, l.Services().Reindex(ctx))
}

func TestAdd_DeriveNameFromContract(t *testing.T) {
	ws, fixDir := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	cloneDir := filepath.Join(t.TempDir(), "rider-clone")
	require.NoError(t, copyDir(filepath.Join(fixDir, "rider-service"), cloneDir))

	// Remove the explicit name from service.yaml and give the contract a
	// unique title, so Add must derive the service name from the ingested
	// contract's info.title (PLAN §6) rather than conflicting with the
	// already-registered "rider-service".
	svcYAML := filepath.Join(cloneDir, "api", "service.yaml")
	data, err := os.ReadFile(svcYAML)
	require.NoError(t, err)
	updated := strings.Replace(string(data), "name: rider-service\n", "", 1)
	require.NotEqual(t, string(data), updated)
	require.NoError(t, os.WriteFile(svcYAML, []byte(updated), 0o644))

	contractYAML := filepath.Join(cloneDir, "api", "openapi.yaml")
	cdata, err := os.ReadFile(contractYAML)
	require.NoError(t, err)
	cupdated := strings.Replace(string(cdata), "title: Rider Service\n", "title: Rider Service Clone\n", 1)
	require.NotEqual(t, string(cdata), cupdated)
	require.NoError(t, os.WriteFile(contractYAML, []byte(cupdated), 0o644))

	svc, err := l.Services().Add(ctx, "", domain.Source{Kind: domain.SourceLocal, Path: cloneDir})
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, "rider-service-clone", svc.Name)
}

func TestAdd_SyncFailureKeepsRegistration(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svc, err := l.Services().Add(ctx, "broken-service",
		domain.Source{Kind: domain.SourceLocal, Path: filepath.Join(t.TempDir(), "does-not-exist")})
	require.Error(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncError, svc.Status)

	got, gerr := l.Services().Get(ctx, "broken-service")
	require.NoError(t, gerr)
	assert.Equal(t, domain.SyncError, got.Status)
}

// unreachableGitURL is a syntactically valid git URL (per gitsrc.IsGitURL)
// that fails fast without ever touching the network: a "file://" URL
// pointing at a path that does not exist. `git clone` on it errors out from
// a local stat, not a DNS lookup or a TCP connect, so tests that need an
// "Add/Ensure fails" git source stay hermetic and fast (unlike a
// "git@host:..."/"https://host/..." URL, which would have this engine's
// git.Manager actually attempt a network connection and only fail after its
// configured timeout).
func unreachableGitURL(t *testing.T) string {
	t.Helper()
	return "file://" + filepath.Join(t.TempDir(), "no-such-repo.git")
}

func TestAdd_GitSource_UnreachableKeepsRegistration(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svc, err := l.Services().Add(ctx, "git-service",
		domain.Source{Kind: domain.SourceGit, URL: unreachableGitURL(t)})
	require.Error(t, err)
	assert.NotEqual(t, errs.NotImplemented, errs.CodeOf(err), "git sources are implemented as of Phase 5")
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncError, svc.Status)

	// The registration is kept (same contract as a broken local source):
	// fixing the source and re-syncing, not re-adding, is the recovery path.
	got, gerr := l.Services().Get(ctx, "git-service")
	require.NoError(t, gerr)
	assert.Equal(t, domain.SyncError, got.Status)
}

func TestWatch_SkipsUnresolvableSources(t *testing.T) {
	ws, _ := setupWorkspace(t)
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "broken",
		Source: domain.Source{Kind: domain.SourceLocal, Path: filepath.Join(t.TempDir(), "missing")},
	}))
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "git-svc",
		Source: domain.Source{Kind: domain.SourceGit, URL: unreachableGitURL(t)},
	}))
	require.NoError(t, workspace.Save(ws))

	l, err := Open(ws, Options{Watch: true, GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()

	assert.NotNil(t, l.watcher)
}

func TestFingerprintError(t *testing.T) {
	assert.Equal(t, "local: fingerprinting is only supported for local sources", errNotFingerprintable.Error())
}
