package registry_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/events"
	"github.com/gs-sinha/sapien/internal/registry"
)

// fakeIndexer records every call made to it, for assertions.
type fakeIndexer struct {
	mu sync.Mutex

	applied      []registry.Snapshot
	errored      []domain.Service
	erroredMsgs  []string
	removed      []string
	applyErr     error
	markErr      error
	removeErr    error
	applyForName map[string]error // per-service override
}

func (f *fakeIndexer) Apply(ctx context.Context, snap registry.Snapshot) (domain.CatalogChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.applyForName[snap.Service.Name]; ok && err != nil {
		return domain.CatalogChange{}, err
	}
	if f.applyErr != nil {
		return domain.CatalogChange{}, f.applyErr
	}
	f.applied = append(f.applied, snap)
	ids := make([]string, 0, len(snap.Operations))
	for _, op := range snap.Operations {
		ids = append(ids, op.ID)
	}
	return domain.CatalogChange{Service: snap.Service.Name, Added: ids}, nil
}

// snapshots returns the snapshots applied so far, under the lock.
func (f *fakeIndexer) snapshots() []registry.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]registry.Snapshot, len(f.applied))
	copy(out, f.applied)
	return out
}

func (f *fakeIndexer) MarkServiceError(ctx context.Context, svc domain.Service, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errored = append(f.errored, svc)
	f.erroredMsgs = append(f.erroredMsgs, msg)
	return f.markErr
}

func (f *fakeIndexer) RemoveService(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return f.removeErr
}

func syncWorkspace(t *testing.T) *domain.Workspace {
	t.Helper()
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: fixturesRoot(t)}
	ws.Services = []domain.ServiceRef{
		fixtureRef("allocation-service"),
		fixtureRef("order-service"),
		fixtureRef("rider-service"),
		{Name: "bogus-service", Source: domain.Source{Kind: domain.SourceLocal, Path: "does-not-exist"}},
	}
	return ws
}

func TestSyncer_SyncAll(t *testing.T) {
	ws := syncWorkspace(t)
	idx := &fakeIndexer{}
	bus := events.New()
	sub, unsubscribe := bus.Subscribe(context.Background())
	defer unsubscribe()

	s := registry.NewSyncer(ws, idx, bus)
	services, err := s.SyncAll(context.Background())
	require.NoError(t, err)
	require.Len(t, services, 4)

	byName := map[string]domain.Service{}
	for _, svc := range services {
		byName[svc.Name] = svc
	}

	for _, name := range []string{"allocation-service", "order-service", "rider-service"} {
		require.Contains(t, byName, name)
		assert.Equal(t, domain.SyncOK, byName[name].Status)
		assert.Empty(t, byName[name].Error)
	}

	bogus := byName["bogus-service"]
	assert.Equal(t, domain.SyncError, bogus.Status)
	assert.NotEmpty(t, bogus.Error)

	// The three good services were applied; the bogus one was marked errored.
	assert.Len(t, idx.applied, 3)
	require.Len(t, idx.errored, 1)
	assert.Equal(t, "bogus-service", idx.errored[0].Name)
	assert.Contains(t, idx.erroredMsgs[0], "does-not-exist")

	// Events: 3 catalog.changed + 1 service.sync_failed.
	var changed, failed int
	drainEvents(sub, 4, func(ev domain.Event) {
		switch ev.Type {
		case domain.EventCatalogChanged:
			changed++
		case domain.EventServiceSyncFailed:
			failed++
		}
	})
	assert.Equal(t, 3, changed)
	assert.Equal(t, 1, failed)
}

func TestSyncer_SyncOne_NotFound(t *testing.T) {
	ws := syncWorkspace(t)
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil)

	_, err := s.SyncOne(context.Background(), "does-not-exist")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
}

func TestSyncer_SyncOne_Success(t *testing.T) {
	ws := syncWorkspace(t)
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil) // nil bus must not panic

	svc, err := s.SyncOne(context.Background(), "allocation-service")
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncOK, svc.Status)
	assert.Len(t, idx.applied, 1)
}

func TestSyncer_SyncOne_BuildFailure(t *testing.T) {
	ws := syncWorkspace(t)
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil)

	svc, err := s.SyncOne(context.Background(), "bogus-service")
	require.Error(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncError, svc.Status)
	assert.NotEmpty(t, svc.Error)
	require.Len(t, idx.errored, 1)
}

func TestSyncer_SyncOne_ApplyFailure(t *testing.T) {
	ws := syncWorkspace(t)
	idx := &fakeIndexer{applyForName: map[string]error{"allocation-service": fakeErr("boom")}}
	s := registry.NewSyncer(ws, idx, nil)

	svc, err := s.SyncOne(context.Background(), "allocation-service")
	require.Error(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, domain.SyncError, svc.Status)
	assert.Contains(t, svc.Error, "boom")
	require.Len(t, idx.errored, 1)
	assert.Equal(t, "allocation-service", idx.errored[0].Name)
}

func TestSyncer_Remove(t *testing.T) {
	ws := syncWorkspace(t)
	idx := &fakeIndexer{}
	s := registry.NewSyncer(ws, idx, nil)

	require.NoError(t, s.Remove(context.Background(), "allocation-service"))
	assert.Equal(t, []string{"allocation-service"}, idx.removed)
}

// fakeErr builds a plain error carrying msg, for fake indexer
// wiring in tests above.
func fakeErr(msg string) error {
	return errs.New(errs.ServiceSource, "%s", msg)
}

// drainEvents reads up to n events from ch (failing the test if they don't
// arrive promptly), calling fn for each.
func drainEvents(ch <-chan domain.Event, n int, fn func(domain.Event)) {
	for i := 0; i < n; i++ {
		ev := <-ch
		fn(ev)
	}
}
