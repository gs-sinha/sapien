package local

import (
	"context"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/events"
	"github.com/gs-sinha/sapien/internal/registry"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// serviceAPI implements engine.ServiceAPI over a Local.
type serviceAPI struct{ l *Local }

var _ engine.ServiceAPI = (*serviceAPI)(nil)

func (s *serviceAPI) List(ctx context.Context) ([]domain.Service, error) {
	return s.l.cat.ListServices(ctx)
}

func (s *serviceAPI) Get(ctx context.Context, name string) (*domain.Service, error) {
	return s.l.cat.GetService(ctx, name)
}

// Add registers src under name (deriving name from the ingested contract's
// title, via a first Build, when name is ""), writes it to
// sapien.workspace.yaml, and indexes it. On a sync failure the registration
// is kept (so the developer can fix the source and `sapien service sync`
// rather than re-adding) and Add returns both the resulting Service (status
// error) and the error.
func (s *serviceAPI) Add(ctx context.Context, name string, src domain.Source) (*domain.Service, error) {
	l := s.l
	ref := domain.ServiceRef{Name: name, Source: src}
	if ref.Source.Kind == domain.SourceLocal {
		ref.Source.Path = workspace.NormalizeLocalPath(l.ws, ref.Source.Path)
	}

	if ref.Name == "" {
		// WithGit(l.gitMgr) matters here even though it is always non-nil:
		// without it, deriving a name for an unnamed git source (peek-ingest
		// to learn the contract's title) would fail with errs.NotImplemented,
		// the same as an unconfigured Builder.
		b := registry.NewBuilder(l.ws).WithGit(l.gitMgr)
		snap, err := b.Build(ctx, ref)
		if err != nil {
			return nil, err
		}
		ref.Name = snap.Service.Name
	}

	if err := workspace.AddService(l.ws, ref); err != nil {
		return nil, err
	}
	if err := workspace.Save(l.ws); err != nil {
		return nil, err
	}

	svc, syncErr := l.syncer.SyncOne(ctx, ref.Name)
	// The fingerprint reflects on-disk content, independent of whether the
	// sync itself succeeded; recording it either way means a service whose
	// contract fails to parse isn't retried on every subsequent Open until
	// its files actually change.
	l.refreshFingerprint(ctx, ref)
	// Keep memory.Locator's ServiceDirs (shared by reference with l.memStore)
	// in sync so a service-scoped memory can be written immediately after
	// Add, without waiting for the next Open.
	if svc != nil && svc.PackageDir != "" {
		l.serviceDirs[ref.Name] = svc.PackageDir
	}
	if svc != nil && svc.Status == domain.SyncOK {
		l.enqueueSemanticIndex(ref.Name)
	}
	return svc, syncErr
}

// Remove unregisters name: removes it from sapien.workspace.yaml, drops its
// catalog rows, and forgets its stored fingerprint.
func (s *serviceAPI) Remove(ctx context.Context, name string) error {
	l := s.l

	removedOps, _ := l.cat.ListOperations(ctx, name)
	removedIDs := make([]string, len(removedOps))
	for i, op := range removedOps {
		removedIDs[i] = op.ID
	}

	if err := workspace.RemoveService(l.ws, name); err != nil {
		return err
	}
	if err := workspace.Save(l.ws); err != nil {
		return err
	}

	if err := l.syncer.Remove(ctx, name); err != nil {
		return err
	}
	_ = settingsDelete(ctx, l.db, fingerprintKey(name))
	delete(l.serviceDirs, name)

	l.emit(domain.EventCatalogChanged, domain.CatalogChange{Service: name, Removed: removedIDs})
	return nil
}

// Sync re-reads the source and reindexes: SyncOne for a named service, or
// SyncAll (every registered service) when name is "". For a git-sourced
// service this fetches first (registry.Syncer.syncRef, when a git Manager
// is configured, which Open always does): Sync (along with Reindex and the
// daemon's periodic timer) is one of the few places a git service is
// actually re-fetched -- see staleness.go's doc comment for why the
// staleness check itself never does.
func (s *serviceAPI) Sync(ctx context.Context, name string) ([]domain.Service, error) {
	l := s.l

	if name == "" {
		svcs, err := l.syncer.SyncAll(ctx)
		for i, ref := range l.ws.Services {
			l.refreshFingerprint(ctx, ref)
			if i < len(svcs) && svcs[i].Status == domain.SyncOK {
				l.enqueueSemanticIndex(svcs[i].Name)
			}
		}
		return svcs, err
	}

	svc, err := l.syncer.SyncOne(ctx, name)
	if svc == nil {
		return nil, err
	}
	if ref, ok := findRef(l.ws, name); ok {
		l.refreshFingerprint(ctx, ref)
	}
	if svc.Status == domain.SyncOK {
		l.enqueueSemanticIndex(svc.Name)
	}
	return []domain.Service{*svc}, err
}

// Reindex rebuilds the catalog for every registered service from their
// canonical files, fetching first for any git-sourced service (see Sync's
// doc comment).
func (s *serviceAPI) Reindex(ctx context.Context) error {
	l := s.l
	svcs, err := l.syncer.SyncAll(ctx)
	for i, ref := range l.ws.Services {
		l.refreshFingerprint(ctx, ref)
		if i < len(svcs) && svcs[i].Status == domain.SyncOK {
			l.enqueueSemanticIndex(svcs[i].Name)
		}
	}
	return err
}

// emit publishes typ/payload on l's event bus. Every other event flows
// through registry.Syncer (which already holds the bus); this is the one
// event Remove publishes itself, since removal isn't a Syncer operation.
func (l *Local) emit(typ domain.EventType, payload any) {
	if l.bus == nil {
		return
	}
	events.Emit(l.bus, typ, payload)
}
