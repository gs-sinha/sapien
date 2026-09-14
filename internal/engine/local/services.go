package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/events"
	"github.com/gs-sinha/sapien/internal/gitsrc"
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
		//
		// WithGitFetch matters because this peek runs before the service is
		// registered, and the SyncOne below -- which does fetch -- never
		// runs if it fails. Without it, `service add <url>` and `service add
		// <url> --name x` disagree on a stale clone: the named form fetches
		// and succeeds, the unnamed form reads the old tree and fails with
		// "no API package found", identically on every retry.
		b := registry.NewBuilder(l.ws).WithGit(l.gitMgr).WithGitFetch()
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
	l.setReadOnly(ref.Name, ref.Source)
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
	delete(l.readOnlyServices, name)

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

// Bind makes this machine read name from the local checkout at path
// (PLAN §7b). The override goes to sapien.workspace.local.yaml and never to
// the committed file, so it cannot reach a teammate through a commit; the
// root .gitignore is made to say so for workspaces older than the file.
// The override is kept even when the resync fails, for the reason Add
// keeps a failed registration: the developer fixes the checkout and syncs
// rather than binding again.
func (s *serviceAPI) Bind(ctx context.Context, name, path string) (*domain.Service, error) {
	l := s.l
	dir, err := checkoutDir(path)
	if err != nil {
		return nil, err
	}
	if err := workspace.Bind(l.ws, name, dir); err != nil {
		return nil, err
	}
	if err := workspace.SaveLocal(l.ws); err != nil {
		return nil, err
	}
	if err := workspace.EnsureLocalIgnored(l.ws); err != nil {
		return nil, err
	}
	return s.resyncRebound(ctx, name)
}

// Unbind removes name's override so it is read from its committed source
// again -- a fetch of the managed clone, for a git source -- and resyncs.
func (s *serviceAPI) Unbind(ctx context.Context, name string) (*domain.Service, error) {
	l := s.l
	ref, ok := findRef(l.ws, name)
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	if ref.Team == nil {
		return nil, errs.New(errs.Invalid, "service %q is not bound to a local checkout", name).
			WithDetail("name", name).
			WithHint("it is already read from its committed source; `sapien service list` shows what each service reads")
	}
	if err := workspace.Unbind(l.ws, name); err != nil {
		return nil, err
	}
	if err := workspace.SaveLocal(l.ws); err != nil {
		return nil, err
	}
	return s.resyncRebound(ctx, name)
}

// resyncRebound re-reads name from its now-effective source and refreshes
// what Add refreshes after a sync: the fingerprint, the service-dir map and
// the read-only set the locators share by reference. Then it restarts the
// file watcher, whose watch set was built from the previous source and
// would otherwise keep watching a directory the service no longer reads.
// The sync error, if any, is returned alongside the resulting Service
// record, as Add does.
func (s *serviceAPI) resyncRebound(ctx context.Context, name string) (*domain.Service, error) {
	l := s.l
	svc, syncErr := l.syncer.SyncOne(ctx, name)
	if ref, ok := findRef(l.ws, name); ok {
		l.refreshFingerprint(ctx, ref)
		l.setReadOnly(name, ref.Source)
	}
	if svc != nil && svc.PackageDir != "" {
		l.serviceDirs[name] = svc.PackageDir
	}
	if svc != nil && svc.Status == domain.SyncOK {
		l.enqueueSemanticIndex(name)
	}
	if l.watcher != nil {
		if err := l.restartWatch(); err != nil {
			l.logger.Warn("restarting the file watcher after rebinding failed", "service", name, "error", err)
		}
	}
	return svc, syncErr
}

// Binding reports what name is read from on this machine, plus every local
// checkout of the same repository this machine already reads some service
// from (findCheckouts), so a UI can offer "read from ~/code/x instead" as
// one click. The catalog row's Binding is used when it has one; a row that
// predates bindings, or one left by a failed sync, gets the binding
// derived from the workspace entry instead, so the answer never depends on
// whether the last sync succeeded.
func (s *serviceAPI) Binding(ctx context.Context, name string) (*engine.BindingInfo, error) {
	l := s.l
	ref, ok := findRef(l.ws, name)
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}

	info := &engine.BindingInfo{Service: name}
	if svc, err := l.cat.GetService(ctx, name); err == nil && svc.Binding != nil {
		info.Binding = *svc.Binding
	} else {
		info.Binding = *l.bindingFromRef(ctx, ref)
	}

	team := &ref.Source
	if ref.Team != nil {
		team = ref.Team
	}
	if team.Kind != domain.SourceGit {
		return info, nil
	}
	want := gitsrc.NormalizeRemote(team.URL)
	if want == "" {
		return info, nil
	}
	exclude := ""
	if info.Binding.Mode == domain.BindingLocal && info.Binding.Local != nil {
		exclude = info.Binding.Local.Path
	}
	info.Candidates = l.findCheckouts(ctx, want, exclude)
	return info, nil
}

// bindingFromRef derives a binding from the workspace entry alone, for a
// service whose catalog row carries none.
func (l *Local) bindingFromRef(ctx context.Context, ref domain.ServiceRef) *domain.ServiceBinding {
	root := ""
	if ref.Source.Kind == domain.SourceLocal {
		if r, err := workspace.ResolveSourcePath(l.ws, ref.Source); err == nil {
			root = r
		}
	}
	return registry.BindingFor(ctx, l.gitMgr, ref, root)
}

// findCheckouts lists the local checkouts of the repository want (in
// gitsrc.NormalizeRemote form) that this machine already reads a service
// from: the local-source services of this workspace and of every other
// registered one, minus the checkout at exclude (the one currently bound).
// Every candidate is described through git, which is why only Binding
// does this and a listing never pays for it. A workspace that cannot be
// loaded or a source that cannot be resolved is skipped: this is an offer
// of shortcuts, not a report on the machine.
func (l *Local) findCheckouts(ctx context.Context, want, exclude string) []domain.LocalCheckout {
	dirs := []string{l.ws.Dir}
	if known, err := config.KnownWorkspaces(); err == nil {
		dirs = append(dirs, known...)
	}

	var out []domain.LocalCheckout
	var visited []string
	for _, dir := range dirs {
		if containsDir(visited, dir) {
			continue
		}
		visited = append(visited, dir)

		ws := l.ws
		if !workspace.SameDir(dir, l.ws.Dir) {
			loaded, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
			if err != nil {
				continue
			}
			ws = loaded
		}

		for _, ref := range ws.Services {
			if ref.Source.Kind != domain.SourceLocal {
				continue
			}
			root, err := workspace.ResolveSourcePath(ws, ref.Source)
			if err != nil {
				continue
			}
			if exclude != "" && workspace.SameDir(root, exclude) {
				continue
			}
			seen := false
			for _, c := range out {
				if workspace.SameDir(c.Path, root) {
					seen = true
					break
				}
			}
			if seen {
				continue
			}
			co, err := l.gitMgr.Describe(ctx, root)
			if err != nil || co == nil || gitsrc.NormalizeRemote(co.Remote) != want {
				continue
			}
			out = append(out, *co)
		}
	}
	return out
}

// containsDir reports whether dirs already names dir (by directory, not by
// spelling; see workspace.SameDir).
func containsDir(dirs []string, dir string) bool {
	for _, d := range dirs {
		if workspace.SameDir(d, dir) {
			return true
		}
	}
	return false
}

// checkoutDir resolves the path a developer typed for Bind to an absolute
// directory that exists. "~" is expanded here because the path is stored
// absolute: the override file is per machine, so there is nothing to keep
// portable, and an absolute path is the one a UI can open.
func checkoutDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errs.New(errs.Invalid, "bind: a checkout path is required").
			WithHint("pass the directory of your clone: `sapien service bind <name> <path>`")
	}
	p := path
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "resolving %q", path)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errs.New(errs.Invalid, "checkout path does not exist: %s", abs).
				WithDetail("path", abs).
				WithHint("clone the repository first, then bind the directory of the clone")
		}
		return "", errs.Wrap(errs.Internal, err, "checking %s", abs)
	}
	if !info.IsDir() {
		return "", errs.New(errs.Invalid, "checkout path is not a directory: %s", abs).
			WithDetail("path", abs).
			WithHint("bind the repository root (or the directory that holds api/), not a file")
	}
	return abs, nil
}
