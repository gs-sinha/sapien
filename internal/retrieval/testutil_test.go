package retrieval_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/catalog"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/memory"
	"github.com/growsimplee/sapien/internal/registry"
	"github.com/growsimplee/sapien/internal/retrieval"
	"github.com/growsimplee/sapien/internal/runs"
	"github.com/growsimplee/sapien/internal/search"
	"github.com/growsimplee/sapien/internal/store"
)

// fixtureServices is the fixed set of services under fixtures/logistics.
var fixtureServices = []string{"allocation-service", "order-service", "rider-service"}

// fixturesRoot returns the absolute path of fixtures/logistics, resolved
// relative to this test file rather than the working directory `go test`
// happens to use.
func fixturesRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "logistics")
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

// copyFixtures copies the three logistics service packages into a fresh
// temp dir, so a test that writes memory files (workspace- or
// service-scoped) never touches fixtures/logistics itself.
func copyFixtures(t *testing.T) string {
	t.Helper()
	root := fixturesRoot(t)
	dir := t.TempDir()
	for _, svc := range fixtureServices {
		require.NoError(t, copyDir(filepath.Join(root, svc), filepath.Join(dir, svc)))
	}
	return dir
}

// testEnv bundles everything a retrieval.Builder test needs: the catalog and
// search reader over an in-memory database seeded from the (copied) logistics
// fixtures via the registry package, a memory.Store wired to a
// retrieval.CatalogResolver over the same catalog, and a runs.Store.
type testEnv struct {
	db     *store.DB
	cat    *catalog.Catalog
	srch   *search.Searcher
	mem    *memory.Store
	runs   *runs.Store
	ws     *domain.Workspace
	fixDir string
}

// newTestEnv copies the logistics fixtures into a temp dir, ingests all
// three services through registry.NewBuilder into a fresh in-memory catalog,
// and wires up search/memory/runs over the same database.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	fixDir := copyFixtures(t)
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: fixDir}

	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cat := catalog.New(db)
	b := registry.NewBuilder(ws)

	serviceDirs := map[string]string{}
	for _, name := range fixtureServices {
		snap, err := b.Build(ctx, domain.ServiceRef{
			Name:   name,
			Source: domain.Source{Kind: domain.SourceLocal, Path: name},
		})
		require.NoError(t, err)
		_, err = cat.Apply(ctx, catalog.Snapshot(*snap))
		require.NoError(t, err)
		serviceDirs[name] = snap.Service.PackageDir
	}

	srch := search.New(db)

	loc := memory.Locator{WorkspaceDir: fixDir, ServiceDirs: serviceDirs}
	res := &retrieval.CatalogResolver{Cat: cat}
	mem := memory.New(db, loc, res)

	runsStore := runs.New(db)

	return &testEnv{db: db, cat: cat, srch: srch, mem: mem, runs: runsStore, ws: ws, fixDir: fixDir}
}

// createMemory is a small helper around mem.Create that fails the test on
// error and returns the stored memory.
func createMemory(t *testing.T, env *testEnv, m domain.Memory) *domain.Memory {
	t.Helper()
	created, err := env.mem.Create(context.Background(), m)
	require.NoError(t, err)
	return created
}
