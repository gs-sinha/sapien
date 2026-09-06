package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/registry"
)

// changeRecorder collects Change notifications from a Watcher under a
// mutex, for polling assertions from the test goroutine.
type changeRecorder struct {
	mu      sync.Mutex
	changes []registry.Change
}

func (r *changeRecorder) onChange(c registry.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, c)
}

func (r *changeRecorder) snapshot() []registry.Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]registry.Change, len(r.changes))
	copy(out, r.changes)
	return out
}

// waitFor polls until fn returns true or the timeout elapses, failing the
// test on timeout.
func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !fn() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func newTestWorkspace(t *testing.T) (*domain.Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	ws := &domain.Workspace{
		Version: 1, Name: "w", Dir: dir,
		File: filepath.Join(dir, domain.WorkspaceFileName),
	}
	require.NoError(t, os.WriteFile(ws.File, []byte("version: 1\nname: w\n"), 0o644))
	for _, d := range []string{domain.FlowsDir, domain.MemoriesDir, domain.EnvironmentsDir} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
	}
	return ws, dir
}

func TestWatcher_ServicePackageChange(t *testing.T) {
	ws, _ := newTestWorkspace(t)

	pkgDir := filepath.Join(ws.Dir, "svc-a")
	writeFile(t, filepath.Join(pkgDir, "openapi.yaml"), minimalOpenAPI)
	pkg, err := registry.DiscoverPackage(pkgDir, "")
	require.NoError(t, err)

	rec := &changeRecorder{}
	w, err := registry.NewWatcher(ws, map[string]*registry.Package{"svc-a": pkg}, 50*time.Millisecond, rec.onChange)
	require.NoError(t, err)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	// Let the watch settle before mutating.
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "openapi.yaml"), []byte(minimalOpenAPI+"\n# touched\n"), 0o644))

	waitFor(t, 2*time.Second, func() bool {
		for _, c := range rec.snapshot() {
			for _, s := range c.Services {
				if s == "svc-a" {
					return true
				}
			}
		}
		return false
	})

	// The burst of writes should coalesce into (at most a couple of) Change
	// notifications, not one per fsnotify event.
	assert.LessOrEqual(t, len(rec.snapshot()), 3)
}

func TestWatcher_NewSubdirDocFile(t *testing.T) {
	ws, _ := newTestWorkspace(t)

	pkgDir := filepath.Join(ws.Dir, "svc-b")
	writeFile(t, filepath.Join(pkgDir, "openapi.yaml"), minimalOpenAPI)
	pkg, err := registry.DiscoverPackage(pkgDir, "")
	require.NoError(t, err)

	rec := &changeRecorder{}
	w, err := registry.NewWatcher(ws, map[string]*registry.Package{"svc-b": pkg}, 50*time.Millisecond, rec.onChange)
	require.NoError(t, err)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	time.Sleep(50 * time.Millisecond)

	// A brand new subdirectory under docs/, created after Start, must be
	// picked up automatically -- and a file added inside it detected.
	newDocsSub := filepath.Join(pkgDir, "docs", "extra")
	require.NoError(t, os.MkdirAll(newDocsSub, 0o755))
	time.Sleep(100 * time.Millisecond) // let the dynamic watch-add land
	require.NoError(t, os.WriteFile(filepath.Join(newDocsSub, "note.md"), []byte("# Note\n"), 0o644))

	waitFor(t, 2*time.Second, func() bool {
		for _, c := range rec.snapshot() {
			for _, s := range c.Services {
				if s == "svc-b" {
					return true
				}
			}
		}
		return false
	})
}

func TestWatcher_WorkspaceFlowsDirChange(t *testing.T) {
	ws, dir := newTestWorkspace(t)

	rec := &changeRecorder{}
	w, err := registry.NewWatcher(ws, nil, 50*time.Millisecond, rec.onChange)
	require.NoError(t, err)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	time.Sleep(50 * time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(dir, domain.FlowsDir, "new.flow.yaml"), []byte("version: 1\nsteps: []\n"), 0o644))

	waitFor(t, 2*time.Second, func() bool {
		for _, c := range rec.snapshot() {
			for _, s := range c.Workspace {
				if s == "flows" {
					return true
				}
			}
		}
		return false
	})
}

func TestWatcher_WorkspaceFileChange(t *testing.T) {
	ws, _ := newTestWorkspace(t)

	rec := &changeRecorder{}
	w, err := registry.NewWatcher(ws, nil, 50*time.Millisecond, rec.onChange)
	require.NoError(t, err)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	time.Sleep(50 * time.Millisecond)

	require.NoError(t, os.WriteFile(ws.File, []byte("version: 1\nname: w\n# touched\n"), 0o644))

	waitFor(t, 2*time.Second, func() bool {
		for _, c := range rec.snapshot() {
			for _, s := range c.Workspace {
				if s == "workspace" {
					return true
				}
			}
		}
		return false
	})
}

func TestWatcher_IgnoresEditorTempFiles(t *testing.T) {
	ws, _ := newTestWorkspace(t)

	pkgDir := filepath.Join(ws.Dir, "svc-c")
	writeFile(t, filepath.Join(pkgDir, "openapi.yaml"), minimalOpenAPI)
	pkg, err := registry.DiscoverPackage(pkgDir, "")
	require.NoError(t, err)

	rec := &changeRecorder{}
	w, err := registry.NewWatcher(ws, map[string]*registry.Package{"svc-c": pkg}, 30*time.Millisecond, rec.onChange)
	require.NoError(t, err)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	time.Sleep(50 * time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "openapi.yaml~"), []byte("junk"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, ".#openapi.yaml"), []byte("junk"), 0o644))

	// Give the debounce window plenty of time to have fired if it were
	// (incorrectly) going to.
	time.Sleep(300 * time.Millisecond)
	assert.Empty(t, rec.snapshot())
}

func TestNewWatcher_DefaultDebounce(t *testing.T) {
	ws, _ := newTestWorkspace(t)
	w, err := registry.NewWatcher(ws, nil, 0, func(registry.Change) {})
	require.NoError(t, err)
	defer w.Close()
}

func TestWatcher_CloseIsIdempotent(t *testing.T) {
	ws, _ := newTestWorkspace(t)
	w, err := registry.NewWatcher(ws, nil, time.Millisecond, func(registry.Change) {})
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, w.Close())
}
