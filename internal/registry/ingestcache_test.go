package registry_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/registry"
)

// oneOperationSpec is a contract with a single named operation, so a test
// can tell one revision of it from the next by what the sync produced.
func oneOperationSpec(opID string) string {
	return fmt.Sprintf(`
openapi: 3.1.0
info:
  title: Cacheable
  version: "1.0.0"
paths:
  /v1/things:
    get:
      operationId: %s
      summary: List things
      responses:
        "200":
          description: OK
`, opID)
}

// cacheableWorkspace builds a writable workspace holding one local service
// package with a contract and a docs/ directory, and returns the workspace
// plus the package directory.
func cacheableWorkspace(t *testing.T) (*domain.Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "svc", "api")
	writeFile(t, filepath.Join(pkgDir, "openapi.yaml"), oneOperationSpec("listThings"))
	writeFile(t, filepath.Join(pkgDir, "docs", "guide.md"), "# Guide\n\nCall listThings to list things.\n")

	ws := &domain.Workspace{
		Version: 1, Name: "w", Dir: dir,
		File: filepath.Join(dir, domain.WorkspaceFileName),
		Services: []domain.ServiceRef{{
			Name:   "svc",
			Source: domain.Source{Kind: domain.SourceLocal, Path: "svc"},
		}},
	}
	return ws, pkgDir
}

// A change that leaves the contract's bytes alone -- an agent writing into
// api/docs/, which is what the file watcher resyncs the whole service for --
// must not re-parse the contract. The parse is the single most expensive
// thing a sync does (61% of everything the daemon allocated in the
// reproduction that motivated the cache), so this is asserted directly: two
// syncs after the ingest is being kept must serve the *same* operations
// slice, which only happens when the ingest was reused rather than
// repeated.
//
// The first sync is expected to re-parse: retention is earned on the second
// build of the same contract, so that indexing a workspace once and going
// idle keeps nothing (see ingestcache.go).
func TestSyncer_DocOnlyChangeReusesContractIngest(t *testing.T) {
	ws, pkgDir := cacheableWorkspace(t)
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil)

	for i, doc := range []string{"second", "third", "fourth"} {
		svc, err := s.SyncOne(context.Background(), "svc")
		require.NoError(t, err, "sync %d", i)
		require.Equal(t, domain.SyncOK, svc.Status)
		writeFile(t, filepath.Join(pkgDir, "docs", doc+".md"),
			"# "+doc+"\n\nMore prose about listThings.\n")
	}

	snaps := idx.snapshots()
	require.Len(t, snaps, 3)
	require.NotEmpty(t, snaps[1].Operations)
	require.Len(t, snaps[2].Operations, len(snaps[1].Operations))
	assert.Same(t, &snaps[1].Operations[0], &snaps[2].Operations[0],
		"the contract was re-parsed even though its bytes did not change")

	// The docs themselves must still be re-read: that is the change.
	var paths []string
	for _, d := range snaps[2].Docs {
		paths = append(paths, d.Path)
	}
	assert.Contains(t, paths, "docs/second.md")
	assert.Contains(t, paths, "docs/third.md")
}

// Retention is earned: one build of a contract must not leave it parsed in
// memory, or a daemon that indexes a workspace at startup and then sits
// idle would hold every service's contract on the heap forever.
func TestSyncer_SingleBuildKeepsNothingCached(t *testing.T) {
	ws, pkgDir := cacheableWorkspace(t)
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil)

	_, err := s.SyncOne(context.Background(), "svc")
	require.NoError(t, err)

	writeFile(t, filepath.Join(pkgDir, "docs", "second.md"), "# Second\n\nProse.\n")

	_, err = s.SyncOne(context.Background(), "svc")
	require.NoError(t, err)

	snaps := idx.snapshots()
	require.Len(t, snaps, 2)
	assert.NotSame(t, &snaps[0].Operations[0], &snaps[1].Operations[0],
		"the first build of a contract was retained; only a rebuilt one should be")
}

// A cached ingest is shared, not copied, so every Build that reads one must
// treat it as read-only -- Build copies merged.Warnings before appending
// coverage warnings to it for exactly this reason. Run under -race, two
// concurrent syncs of the same service are what would catch a Build that
// forgot.
func TestSyncer_ConcurrentSyncsShareCachedIngestSafely(t *testing.T) {
	ws, _ := cacheableWorkspace(t)
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil)

	// Prime the cache: retention is earned on the second build.
	for i := 0; i < 2; i++ {
		_, err := s.SyncOne(context.Background(), "svc")
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.SyncOne(context.Background(), "svc")
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
}

// The whole point of the watcher is that a contract edit lands promptly, so
// the cache must never hold one back: a changed contract file changes the
// key and is parsed again on the very next sync.
func TestSyncer_ContractChangeIsReingested(t *testing.T) {
	ws, pkgDir := cacheableWorkspace(t)
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil)

	_, err := s.SyncOne(context.Background(), "svc")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(
		filepath.Join(pkgDir, "openapi.yaml"), []byte(oneOperationSpec("listWidgets")), 0o644))

	_, err = s.SyncOne(context.Background(), "svc")
	require.NoError(t, err)

	snaps := idx.snapshots()
	require.Len(t, snaps, 2)
	assert.Equal(t, "svc.listThings", snaps[0].Operations[0].ID)
	assert.Equal(t, "svc.listWidgets", snaps[1].Operations[0].ID,
		"the edited contract was served from cache instead of being re-parsed")
}
