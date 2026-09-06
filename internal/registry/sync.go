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

	mu      sync.Mutex
	sources map[string]domain.Source // name -> the Source last synced, for Remove's best-effort clone cleanup
}

// NewSyncer returns a Syncer for ws, applying snapshots through idx. bus may
// be nil, in which case no events are published.
func NewSyncer(ws *domain.Workspace, idx Indexer, bus *events.Bus) *Syncer {
	return &Syncer{ws: ws, idx: idx, bus: bus, sources: map[string]domain.Source{}}
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
		svc, _ := s.syncRef(ctx, ref)
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
	svc, err := s.syncRef(ctx, ref)
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
		_ = s.git.Remove(src.URL) // best effort: an orphaned clone costs disk, not correctness
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
// service.sync_failed) and never stops the timer. It is a no-op (returns
// only when ctx is done) when no git manager has been configured via
// WithGit.
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
		_, _ = s.syncRef(ctx, ref)
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
// success/failure bookkeeping (MarkServiceError, event emission) shared by
// SyncAll and SyncOne. For a git source with a configured Manager, it fetches
// via Manager.Sync first, so Build resolves the up-to-date clone rather than
// whatever commit happened to be checked out already. It always returns a
// usable Service record; the error return is nil only on full success.
func (s *Syncer) syncRef(ctx context.Context, ref domain.ServiceRef) (domain.Service, error) {
	s.rememberSource(ref.Name, ref.Source)

	b := NewBuilder(s.ws)
	if s.git != nil {
		b = b.WithGit(s.git)
	}

	if ref.Source.Kind == domain.SourceGit && s.git != nil {
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
