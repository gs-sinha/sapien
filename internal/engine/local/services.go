package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/events"
	"github.com/gs-sinha/sapien/internal/flow"
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
		l.syncRepoBestEffort(ctx)
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

// Bind is BindWith with no options: an explicit name, and no validation
// beyond checkoutDir's own. Kept as a thin wrapper because most existing
// callers (and the harness in binding_test.go) already know the name and
// trust the checkout.
func (s *serviceAPI) Bind(ctx context.Context, name, path string) (*domain.Service, error) {
	return s.BindWith(ctx, name, path, engine.BindOptions{})
}

// BindWith makes this machine read name from the local checkout at path
// (PLAN §7b). The override goes to sapien.workspace.local.yaml and never to
// the committed file, so it cannot reach a teammate through a commit; the
// root .gitignore is made to say so for workspaces older than the file.
// The override is kept even when the resync fails, for the reason Add
// keeps a failed registration: the developer fixes the checkout and syncs
// rather than binding again.
//
// An empty name is inferred from the checkout's own origin
// (inferServiceFromCheckout): exactly one registered service, ambiguity
// is an error. Unless opts.Force, the checkout is also validated before
// it is recorded (validateCheckoutForBind) -- binding the wrong sibling
// clone, or a directory with nothing indexable in it, would otherwise
// surface as a confusing sync error days later with nothing pointing back
// at the bind that caused it.
func (s *serviceAPI) BindWith(ctx context.Context, name, path string, opts engine.BindOptions) (*domain.Service, error) {
	l := s.l
	dir, err := checkoutDir(path)
	if err != nil {
		return nil, err
	}

	// Described once regardless of Force or an explicit name: inference,
	// validation and the older-clone nudge below all need it, and it is a
	// handful of fast, read-only git queries either way.
	co, err := l.gitMgr.Describe(ctx, dir)
	if err != nil {
		return nil, err
	}

	if name == "" {
		inferred, err := inferServiceFromCheckout(l.ws, dir, co)
		if err != nil {
			return nil, err
		}
		name = inferred
	}

	ref, ok := findRef(l.ws, name)
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}

	if !opts.Force {
		if err := validateCheckoutForBind(dir, co, ref); err != nil {
			return nil, err
		}
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

	l.warnAboutOlderClone(ctx, name, dir, co, ref)

	return s.resyncRebound(ctx, name)
}

// teamSourceOf returns the source every other machine reads ref from: the
// committed one (ref.Team) when this machine has overridden it with a
// local checkout, else ref.Source itself.
func teamSourceOf(ref domain.ServiceRef) domain.Source {
	if ref.Team != nil {
		return *ref.Team
	}
	return ref.Source
}

// teamContract returns the contract override the committed source
// carries, regardless of what this machine currently binds ref to --
// DiscoverPackage's second argument, when validating or browsing a
// checkout against ref.
func teamContract(ref domain.ServiceRef) string {
	if ref.Team != nil {
		return ref.Team.Contract
	}
	return ref.Source.Contract
}

// isGitRepoRoot reports whether dir is the root of a git repository: git
// marks that with a ".git" entry, a directory for an ordinary clone or a
// file for a worktree. Cheap to check without shelling out.
func isGitRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// resolvedOrSelf returns p with symlinks resolved, or p itself when that
// fails (a path that no longer exists, say). Used only to compare a path
// against something git itself reported, since `git rev-parse
// --show-toplevel` always resolves symlinks and the paths this package is
// handed otherwise never do.
func resolvedOrSelf(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// inferServiceFromCheckout names the one registered service whose team
// source (PLAN §7b: ref.Team when already bound elsewhere, else
// ref.Source when it is itself a git source) is the same repository as
// co's origin -- BindWith's answer to an omitted name.
func inferServiceFromCheckout(ws *domain.Workspace, dir string, co *domain.LocalCheckout) (string, error) {
	if co.Remote == "" {
		return "", errs.New(errs.Invalid, "%s has no git origin to infer a service from", dir).
			WithDetail("path", dir).
			WithHint("pass the service name: `sapien service bind <name> <path>`")
	}
	origin := gitsrc.NormalizeRemote(co.Remote)

	var matches []string
	for _, ref := range ws.Services {
		team := teamSourceOf(ref)
		if team.Kind == domain.SourceGit && gitsrc.NormalizeRemote(team.URL) == origin {
			matches = append(matches, ref.Name)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", errs.New(errs.Invalid, "no registered service is cloned from %s", co.Remote).
			WithDetail("path", dir).
			WithDetail("origin", co.Remote).
			WithHint("pass the service name (`sapien service bind <name> <path>`), or register it first with `sapien service add`")
	default:
		return "", errs.New(errs.Conflict, "%s is a clone of a repository more than one registered service uses: %s", co.Remote, strings.Join(matches, ", ")).
			WithDetail("path", dir).
			WithDetail("origin", co.Remote).
			WithDetail("names", matches).
			WithHint("pass the service name: `sapien service bind <name> <path>`")
	}
}

// validateCheckoutForBind applies BindWith's default safety checks (PLAN
// §7b) unless Force overrides them.
func validateCheckoutForBind(dir string, co *domain.LocalCheckout, ref domain.ServiceRef) error {
	if !isGitRepoRoot(dir) {
		return errs.New(errs.Invalid, "%s is not a git repository", dir).
			WithDetail("path", dir).
			WithHint("pass --force to bind a plain directory (with no drift or staleness information)")
	}

	team := teamSourceOf(ref)
	if co.Remote != "" && team.Kind == domain.SourceGit &&
		gitsrc.NormalizeRemote(co.Remote) != gitsrc.NormalizeRemote(team.URL) {
		return errs.New(errs.Invalid, "checkout at %s is a clone of %s, not of %s", dir, co.Remote, team.URL).
			WithDetail("path", dir).
			WithDetail("origin", co.Remote).
			WithDetail("team_url", team.URL).
			WithHint("pass --force to bind a fork or mirror")
	}

	if _, err := registry.DiscoverPackage(dir, teamContract(ref)); err != nil {
		return errs.New(errs.Invalid, "no API package under %s", dir).
			WithDetail("path", dir).
			WithHint("looked for api/openapi.{yaml,yml,json} (or api/service.yaml), openapi.{yaml,yml,json} in the root, and docs/, spec/, openapi/ subdirectories; pass --force to bind anyway and add the package later")
	}

	return nil
}

// warnAboutOlderClone logs at Info when another checkout of the same
// repository this machine already knows about (findCheckouts) was worked
// in more recently than the one just bound. This is deliberately not a
// field on the returned Service (no new API): the UI already gets the
// same information from Binding's Candidates, and a one-shot bind command
// only has a log line to say it with.
func (l *Local) warnAboutOlderClone(ctx context.Context, name, dir string, co *domain.LocalCheckout, ref domain.ServiceRef) {
	if co == nil || co.CommittedAt.IsZero() {
		return
	}
	team := teamSourceOf(ref)
	if team.Kind != domain.SourceGit {
		return
	}
	want := gitsrc.NormalizeRemote(team.URL)
	if want == "" {
		return
	}

	var newest *domain.LocalCheckout
	for _, other := range l.findCheckouts(ctx, want, dir) {
		if !other.CommittedAt.After(co.CommittedAt) {
			continue
		}
		if newest == nil || other.CommittedAt.After(newest.CommittedAt) {
			o := other
			newest = &o
		}
	}
	if newest == nil {
		return
	}
	if days := int(newest.CommittedAt.Sub(co.CommittedAt).Hours() / 24); days >= 1 {
		l.logger.Info(fmt.Sprintf("binding an older clone; %s is %d days newer", newest.Path, days),
			"service", name, "path", dir, "newer_checkout", newest.Path)
	}
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

// SetRef switches name's ref (PLAN §34f item 2): scope domain.RefScopeLocal
// (default, "") writes only sapien.workspace.local.yaml (this machine);
// domain.RefScopeTeam rewrites source.ref in the committed
// sapien.workspace.yaml. Refused (errs.Invalid) when the service is not
// git-sourced, or when ref is neither a branch nor a tag the remote has
// (validateRemoteRef, via LsRemote, before anything is written -- naming
// close matches, since the branch/tag list was already fetched to check).
// Resyncs through the normal resyncRebound path afterward, same as
// Bind/Unbind: fetch (a local ref override's own managed clone, via
// domain.ServiceRef.EffectiveSource, so it never disturbs the one every
// other ref of the URL shares), reindex, restart the file watcher.
func (s *serviceAPI) SetRef(ctx context.Context, name, ref string, scope string) (*domain.Service, error) {
	l := s.l
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errs.New(errs.Invalid, "ref must not be empty").WithDetail("name", name)
	}

	wsRef, ok := findRef(l.ws, name)
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	team := teamSourceOf(wsRef)
	if team.Kind != domain.SourceGit {
		return nil, errs.New(errs.Invalid, "service %q is not git-sourced; only a git source has a ref", name).
			WithDetail("name", name)
	}

	if err := l.validateRemoteRef(ctx, team.URL, ref); err != nil {
		return nil, err
	}

	switch scope {
	case "", domain.RefScopeLocal:
		if err := workspace.SetLocalRef(l.ws, name, ref); err != nil {
			return nil, err
		}
		if err := workspace.SaveLocal(l.ws); err != nil {
			return nil, err
		}
		if err := workspace.EnsureLocalIgnored(l.ws); err != nil {
			return nil, err
		}
	case domain.RefScopeTeam:
		if err := workspace.SetTeamRef(l.ws, name, ref); err != nil {
			return nil, err
		}
		if err := workspace.Save(l.ws); err != nil {
			return nil, err
		}
	default:
		return nil, errs.New(errs.Invalid, "unknown ref scope %q (want %q or %q)", scope, domain.RefScopeLocal, domain.RefScopeTeam).
			WithDetail("scope", scope)
	}

	return s.resyncRebound(ctx, name)
}

// validateRemoteRef refuses ref with errs.Invalid unless url's remote has
// it as a branch or a tag (LsRemote), naming the closest branch/tag names
// as a hint when it doesn't -- the same list LsRemote fetched to check the
// ref, so a suggestion costs nothing extra. A raw commit sha is not
// validated this way (ls-remote only advertises named refs); SetRef is
// meant to drive the Branches picker, not pin an arbitrary commit.
func (l *Local) validateRemoteRef(ctx context.Context, url, ref string) error {
	refs, err := l.gitMgr.LsRemote(ctx, url)
	if err != nil {
		return err
	}
	all := append(append([]string{}, refs.Branches...), refs.Tags...)
	for _, r := range all {
		if r == ref {
			return nil
		}
	}
	e := errs.New(errs.Invalid, "%q is not a branch or tag on %s", ref, url).
		WithDetail("ref", ref).WithDetail("url", url)
	if suggestions := flow.NearestSuggestions(ref, all, 3); len(suggestions) > 0 {
		e = e.WithHint("did you mean: " + strings.Join(suggestions, ", "))
	}
	return e
}

// ClearRef removes this machine's local ref override, resyncing back to
// whatever ref is now effective (the committed one, unless a path override
// is also active). Refused (errs.Invalid) when there is no local ref
// override to clear.
func (s *serviceAPI) ClearRef(ctx context.Context, name string) (*domain.Service, error) {
	l := s.l
	if err := workspace.ClearLocalRef(l.ws, name); err != nil {
		return nil, err
	}
	if err := workspace.SaveLocal(l.ws); err != nil {
		return nil, err
	}
	return s.resyncRebound(ctx, name)
}

// Branches lists a git-sourced service's branches and tags from
// `ls-remote` (PLAN §34f item 2): Current is the ref this machine actually
// reads (Source.Ref, "" meaning the remote's default branch), Default is
// the remote's own default branch, sorted first among Branches.
func (s *serviceAPI) Branches(ctx context.Context, name string) (*engine.BranchList, error) {
	l := s.l
	ref, ok := findRef(l.ws, name)
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	team := teamSourceOf(ref)
	if team.Kind != domain.SourceGit {
		return nil, errs.New(errs.Invalid, "service %q is not git-sourced; it has no branches to list", name).
			WithDetail("name", name)
	}
	refs, err := l.gitMgr.LsRemote(ctx, team.URL)
	if err != nil {
		return nil, err
	}
	return &engine.BranchList{
		Current:  ref.Source.Ref,
		Default:  refs.Default,
		Branches: refs.Branches,
		Tags:     refs.Tags,
	}, nil
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

// AddFromCheckout registers the repository a local checkout was cloned
// from as a git source in the committed workspace file, and binds the
// checkout on this machine (PLAN §7b): the fix for a new hire who writes
// a new service and has no good way to onboard it -- `service add <path>`
// would commit an absolute local path into the shared file, and `service
// add <url>` needs the package already pushed. Because the service ends
// up bound, indexing reads the checkout directly and succeeds even when
// nothing has been pushed yet.
//
// path may be a subdirectory of its repository (a monorepo service) only
// when opts.AllowSubdir is set; otherwise that is refused with an
// errs.Invalid carrying the detail local_add=true, the same detail a path
// with no git origin at all carries, meaning "this is not committable as
// a git source, but a plain `service add <path>` still is" -- callers
// (the CLI, MCP) use it to fall back automatically.
func (s *serviceAPI) AddFromCheckout(ctx context.Context, name, path string, opts engine.AddFromCheckoutOptions) (*domain.Service, error) {
	l := s.l
	dir, err := checkoutDir(path)
	if err != nil {
		return nil, err
	}

	co, err := l.gitMgr.Describe(ctx, dir)
	if err != nil {
		return nil, err
	}
	if co.Remote == "" {
		return nil, errs.New(errs.Invalid,
			"%s is not a git checkout with an origin; use `service add <path>` for a local-only source", dir).
			WithDetail("path", dir).
			WithDetail("local_add", true)
	}

	toplevel, err := l.gitMgr.Toplevel(ctx, dir)
	if err != nil {
		return nil, err
	}
	isSubdir := !workspace.SameDir(toplevel, dir)
	if isSubdir && !opts.AllowSubdir {
		return nil, errs.New(errs.Invalid, "path is inside the repository at %s, not its root", toplevel).
			WithDetail("path", dir).
			WithDetail("toplevel", toplevel).
			WithDetail("local_add", true).
			WithHint("pass --team (CLI) or team (MCP) to commit the repository with this path recorded as a subdir")
	}

	pkg, pkgErr := registry.DiscoverPackage(dir, "")
	if pkgErr != nil && !opts.Force {
		return nil, errs.New(errs.Invalid, "no API package under %s", dir).
			WithDetail("path", dir).
			WithHint("looked for api/openapi.{yaml,yml,json} (or api/service.yaml), openapi.{yaml,yml,json} in the root, and docs/, spec/, openapi/ subdirectories; pass --force to register anyway and add the package later")
	}

	src := domain.Source{Kind: domain.SourceGit, URL: co.Remote, Ref: opts.Ref}
	if isSubdir && pkgErr == nil {
		// toplevel came from git, which always resolves symlinks; pkg.Dir
		// was built from dir, which (like every path this package is handed)
		// was not. On a machine where the temp or home directory is itself a
		// symlink (macOS: /tmp -> /private/tmp), comparing them unresolved
		// would produce a nonsense Subdir full of "..".
		if rel, relErr := filepath.Rel(resolvedOrSelf(toplevel), resolvedOrSelf(pkg.Dir)); relErr == nil {
			src.Subdir = filepath.ToSlash(rel)
		}
	}

	ref := domain.ServiceRef{Name: name, Source: src}
	if ref.Name == "" {
		// A local ServiceRef, built from the checkout directly -- the same
		// "peek, then derive" trick Add uses for an unnamed git source, but
		// against the working copy so no clone is needed just to learn a
		// name.
		peek, err := registry.NewBuilder(l.ws).Build(ctx, domain.ServiceRef{
			Source: domain.Source{Kind: domain.SourceLocal, Path: dir},
		})
		if err != nil {
			return nil, err
		}
		ref.Name = peek.Service.Name
	}

	if err := workspace.AddService(l.ws, ref); err != nil {
		return nil, err
	}
	if err := workspace.Save(l.ws); err != nil {
		return nil, err
	}
	if err := workspace.Bind(l.ws, ref.Name, dir); err != nil {
		return nil, err
	}
	if err := workspace.SaveLocal(l.ws); err != nil {
		return nil, err
	}
	if err := workspace.EnsureLocalIgnored(l.ws); err != nil {
		return nil, err
	}

	return s.resyncRebound(ctx, ref.Name)
}
