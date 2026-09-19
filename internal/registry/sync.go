package registry

import (
	"context"
	"sync"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/events"
	"github.com/gs-sinha/sapien/internal/gitsrc"
)

// defaultGitSyncInterval is the daemon's background git sync period
// (PLAN §18) when SyncGitPeriodically is given a non-positive interval.
const defaultGitSyncInterval = 10 * time.Minute

// Syncer drives synchronization of a workspace's services into an Indexer
// (PLAN §17).
type Syncer struct {
	ws  *domain.Workspace
	idx Indexer
	bus *events.Bus     // may be nil
	git *gitsrc.Manager // optional; required for git-sourced services (PLAN §18)

	// cache lets repeated syncs of an unchanged contract skip the parse
	// (ingestcache.go). It lives on the Syncer because the Syncer is what
	// syncs the same service over and over -- the file watcher calls
	// SyncOne for every change under the package, docs included.
	cache *ingestCache

	mu      sync.Mutex
	sources map[string]domain.Source // name -> the Source last synced, for Remove's best-effort clone cleanup
}

// NewSyncer returns a Syncer for ws, applying snapshots through idx. bus may
// be nil, in which case no events are published.
func NewSyncer(ws *domain.Workspace, idx Indexer, bus *events.Bus) *Syncer {
	return &Syncer{ws: ws, idx: idx, bus: bus, sources: map[string]domain.Source{}, cache: newIngestCache()}
}

// WithGit sets the git manager used to resolve and sync git-sourced
// services. Optional: without one, a git-sourced service's build fails
// with errs.NotImplemented, same as an unconfigured Builder.
func (s *Syncer) WithGit(m *gitsrc.Manager) *Syncer {
	s.git = m
	return s
}

// SyncAll builds and applies every service registered in the workspace. A
// failure on one service does not stop the others; each failure is recorded
// via Indexer.MarkServiceError and surfaced as a service.sync_failed event,
// while every success is applied and surfaced as a catalog.changed event.
// The returned slice has one entry per workspace service, in workspace
// order, reflecting the resulting Status/Error either way. SyncAll itself
// only returns a non-nil error for something outside any one service (there
// is currently nothing that can cause that; it always returns nil).
func (s *Syncer) SyncAll(ctx context.Context) ([]domain.Service, error) {
	out := make([]domain.Service, 0, len(s.ws.Services))
	for _, ref := range s.ws.Services {
		svc, _ := s.syncRef(ctx, ref, gitFetch)
		out = append(out, svc)
	}
	return out, nil
}

// SyncOne builds and applies the named service. It returns errs.ServiceNotFound
// if name is not registered in the workspace. On a build or apply failure, it
// still returns the resulting (error) Service record alongside the error, so
// callers can render both.
func (s *Syncer) SyncOne(ctx context.Context, name string) (*domain.Service, error) {
	ref, ok := findServiceRef(s.ws, name)
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found in workspace", name)
	}
	svc, err := s.syncRef(ctx, ref, gitFetch)
	return &svc, err
}

// SyncOneFromDisk is SyncOne without the git fetch: it rebuilds the named
// service from the files already on disk, resolving a git source's existing
// clone as-is (cloning only if there is none yet).
//
// It exists because fetching is the wrong response to a local change. The
// file watcher and the engine's staleness check both react to something
// having changed *here*; answering that by going to the network and running
// `git reset --hard origin/<ref>` (what Manager.Sync does, see syncRef)
// fetches on every file save and reverts uncommitted edits inside the
// managed clone -- including the ones that triggered the sync, if they were
// to tracked files. An agent writing documentation into a git-sourced
// package would be fighting the daemon for its own work.
//
// Fetching stays where a user or a timer asked for it: Services().Sync and
// Reindex, service Add, and the daemon's SyncGitPeriodically tick.
func (s *Syncer) SyncOneFromDisk(ctx context.Context, name string) (*domain.Service, error) {
	ref, ok := findServiceRef(s.ws, name)
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found in workspace", name)
	}
	svc, err := s.syncRef(ctx, ref, useCheckoutOnDisk)
	return &svc, err
}

// Remove removes a service from the catalog. It does not touch the
// workspace file; callers that also want to forget the service persistently
// should call workspace.RemoveService + workspace.Save separately.
//
// For a git-sourced service (recorded the last time it was synced), it also
// removes the managed clone via Manager.Remove, best effort, but only when
// no other service remaining in the workspace shares the same URL -- so two
// services pointing at the same repo (different Subdir/Ref) don't fight
// over one clone.
func (s *Syncer) Remove(ctx context.Context, name string) error {
	src, hadSource := s.sourceFor(name)

	if err := s.idx.RemoveService(ctx, name); err != nil {
		return err
	}

	if hadSource && s.git != nil && src.Kind == domain.SourceGit && src.URL != "" && !s.urlStillInUse(src.URL, name) {
		_ = s.git.Remove(src) // best effort: an orphaned clone costs disk, not correctness
	}

	return nil
}

// urlStillInUse reports whether any git-sourced workspace service other
// than excludeName still points at url.
func (s *Syncer) urlStillInUse(url, excludeName string) bool {
	for _, ref := range s.ws.Services {
		if ref.Name == excludeName {
			continue
		}
		if ref.Source.Kind == domain.SourceGit && ref.Source.URL == url {
			return true
		}
	}
	return false
}

// SyncGitPeriodically syncs every git-sourced workspace service every
// interval (PLAN §18's daemon timer; interval <= 0 uses the PLAN default of
// 10 minutes), until ctx is done. Each service's own sync failure is
// recorded the same way SyncOne records one (MarkServiceError,
// service.sync_failed) and never stops the timer. Without a git manager
// configured via WithGit it syncs nothing, but still runs on its timer:
// it doubles as the sweep of the contract ingest cache (ingestcache.go).
func (s *Syncer) SyncGitPeriodically(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = defaultGitSyncInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The daemon's only periodic heartbeat, so it is also where
			// the contract ingest cache sheds entries nobody has asked
			// for since the last tick: that cache expires lazily on use,
			// and a workspace that has gone quiet never uses it again
			// (ingestcache.go).
			s.cache.Sweep()
			s.syncGitOnce(ctx)
		}
	}
}

// syncGitOnce runs one SyncGitPeriodically cycle: syncRef for every
// git-sourced service currently registered in the workspace.
func (s *Syncer) syncGitOnce(ctx context.Context) {
	if s.git == nil {
		return
	}
	for _, ref := range s.ws.Services {
		if ref.Source.Kind != domain.SourceGit {
			continue
		}
		_, _ = s.syncRef(ctx, ref, gitFetch)
	}
}

// rememberSource records ref's Source under name, so a later Remove(name)
// -- called after the caller has typically already dropped ref from
// s.ws.Services -- can still tell whether it was git-sourced and what URL
// to consider cleaning up.
func (s *Syncer) rememberSource(name string, src domain.Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sources == nil {
		s.sources = map[string]domain.Source{}
	}
	s.sources[name] = src
}

func (s *Syncer) sourceFor(name string) (domain.Source, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.sources[name]
	return src, ok
}

// syncRef builds and applies one service reference, handling the
// gitFetch and useCheckoutOnDisk name syncRef's fetch argument at its call
// sites, so that whether a sync reaches the network is legible where the
// decision is made rather than inside syncRef.
const (
	// gitFetch brings a git-sourced service's clone up to date first
	// (Manager.Sync: fetch, then reset --hard onto the tracked ref). Only
	// for syncs a user or a timer asked for.
	gitFetch = true
	// useCheckoutOnDisk builds from whatever the clone already holds, with
	// no network call and no checkout. For syncs triggered by a local
	// change; see SyncOneFromDisk.
	useCheckoutOnDisk = false
)

// success/failure bookkeeping (MarkServiceError, event emission) shared by
// SyncAll and SyncOne. For a git source with a configured Manager, it fetches
// via Manager.Sync first when fetch is gitFetch, so Build resolves the
// up-to-date clone rather than whatever commit happened to be checked out
// already; with useCheckoutOnDisk it leaves the clone alone and Build
// resolves it through Manager.Ensure, which touches the network only when
// there is no clone yet. It always returns a usable Service record; the
// error return is nil only on full success.
func (s *Syncer) syncRef(ctx context.Context, ref domain.ServiceRef, fetch bool) (domain.Service, error) {
	// EffectiveSource flags a machine-local ref override (PLAN §34f item 2)
	// so every gitsrc call below -- and whatever Remove later remembers via
	// rememberSource -- resolves the override's own managed-clone directory
	// instead of the one every other ref of this URL shares.
	ref.Source = ref.EffectiveSource()
	s.rememberSource(ref.Name, ref.Source)

	b := NewBuilder(s.ws).withIngestCache(s.cache)
	if s.git != nil {
		b = b.WithGit(s.git)
	}

	if fetch && ref.Source.Kind == domain.SourceGit && s.git != nil {
		// Build must not fetch again below, but must also not blame a stale
		// clone if the package is missing: this just fetched.
		b = b.WithGitSynced()
		if _, _, err := s.git.Sync(ctx, ref.Source); err != nil {
			svc := domain.Service{
				ID:          ref.Name,
				Name:        ref.Name,
				Source:      ref.Source,
				Status:      domain.SyncError,
				Error:       err.Error(),
				LastIndexed: time.Now(),
			}
			s.fail(ctx, svc, err)
			return svc, err
		}
	}

	snap, err := b.Build(ctx, ref)
	if err != nil {
		svc := domain.Service{
			ID:          ref.Name,
			Name:        ref.Name,
			Source:      ref.Source,
			Status:      domain.SyncError,
			Error:       err.Error(),
			LastIndexed: time.Now(),
		}
		s.fail(ctx, svc, err)
		return svc, err
	}

	change, err := s.idx.Apply(ctx, *snap)
	if err != nil {
		svc := snap.Service
		svc.Status = domain.SyncError
		svc.Error = err.Error()
		s.fail(ctx, svc, err)
		return svc, err
	}
	if reviewer, ok := s.idx.(TaskReviewer); ok {
		reviewed, reviewErr := reviewer.ReviewTasks(ctx, *snap)
		if reviewErr != nil {
			svc := snap.Service
			svc.Status = domain.SyncError
			svc.Error = reviewErr.Error()
			s.fail(ctx, svc, reviewErr)
			return svc, reviewErr
		}
		snap.Service = reviewed
	}

	s.emit(domain.EventCatalogChanged, change)
	return snap.Service, nil
}

// fail records svc as errored via the Indexer and emits service.sync_failed.
// The MarkServiceError error itself is intentionally swallowed: the original
// build/apply error is what matters to the caller, and there is no second
// error channel to report it on.
func (s *Syncer) fail(ctx context.Context, svc domain.Service, cause error) {
	_ = s.idx.MarkServiceError(ctx, svc, cause.Error())
	s.emit(domain.EventServiceSyncFailed, map[string]any{
		"service": svc.Name,
		"error":   cause.Error(),
	})
}

func (s *Syncer) emit(typ domain.EventType, payload any) {
	if s.bus == nil {
		return
	}
	events.Emit(s.bus, typ, payload)
}

func findServiceRef(ws *domain.Workspace, name string) (domain.ServiceRef, bool) {
	for _, s := range ws.Services {
		if s.Name == name {
			return s, true
		}
	}
	return domain.ServiceRef{}, false
}
