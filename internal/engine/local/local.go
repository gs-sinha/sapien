// Package local implements engine.Engine in-process (PLAN.md §4): the
// one-shot CLI path opens a Local directly against the workspace's SQLite
// database, runs a cheap staleness check, answers, and exits. It composes
// the already-built internal/store, internal/catalog, internal/search,
// internal/registry, internal/flow, internal/runner, internal/runs,
// internal/env, internal/memory, and internal/retrieval packages, plus the
// in-process internal/events bus; it never talks to a daemon.
//
// One area per file: services.go/catalog.go/search.go (Phase 1),
// flows.go/runner.go/runs.go/envs.go (Phase 2), memories.go/context.go
// (Phase 3).
package local

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/events"
	"github.com/gs-sinha/sapien/internal/example"
	"github.com/gs-sinha/sapien/internal/gitsrc"
	"github.com/gs-sinha/sapien/internal/memory"
	"github.com/gs-sinha/sapien/internal/registry"
	"github.com/gs-sinha/sapien/internal/retrieval"
	"github.com/gs-sinha/sapien/internal/runner"
	"github.com/gs-sinha/sapien/internal/runs"
	"github.com/gs-sinha/sapien/internal/search"
	"github.com/gs-sinha/sapien/internal/semantic"
	"github.com/gs-sinha/sapien/internal/store"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// Options configures Open.
type Options struct {
	// Watch starts the registry file watcher (daemon mode): changed service
	// packages, and workspace-level flow/memory changes, are reindexed
	// automatically.
	Watch bool
	// SkipStaleCheck skips the one-shot staleness check Open otherwise runs
	// (PLAN §4). Tests use this to open a Local against a catalog exactly as
	// it was left, without triggering an implicit sync.
	SkipStaleCheck bool
	// Logger receives diagnostic logging. Defaults to slog.Default().
	Logger *slog.Logger
	// Secrets, when set, overrides the default secret chain
	// (env.DefaultChain, which reaches the OS keychain). Tests inject an
	// in-memory store (env.NewMemoryStore) so they never touch the OS
	// keychain.
	Secrets env.SecretStore

	// GitCacheDir overrides config.Config.Git.CacheDir (which itself
	// defaults to "~/.sapien/repos" via gitsrc). Tests use this so a managed
	// clone never lands under a developer's real home directory.
	GitCacheDir string
	// GitSyncInterval overrides config.Config.Git.SyncIntervalDuration()
	// for the periodic background git sync started in Watch mode (PLAN
	// §18). Tests use a short interval to observe a sync without waiting
	// out the real (10 minute) default.
	GitSyncInterval time.Duration

	// Embedder, when set, is used directly for semantic search (PLAN §16)
	// instead of building one from config.Config.Semantic — bypassing
	// config entirely, enabled or not. Tests inject a fake Embedder so
	// semantic indexing/query never makes a real network call.
	Embedder semantic.Embedder
}

// Local is the in-process engine.Engine implementation.
type Local struct {
	ws     *domain.Workspace
	db     *store.DB
	cat    *catalog.Catalog
	srch   *search.Searcher
	bus    *events.Bus
	syncer *registry.Syncer
	logger *slog.Logger

	runsStore  *runs.Store
	memStore   *memory.Store
	exStore    *example.Store
	ctxBuilder *retrieval.Builder
	secrets    env.SecretStore
	runner     *runner.Runner
	// serviceDirs maps a registered service to its resolved API package
	// directory. It backs memory.Locator.ServiceDirs; Services().Add/Remove
	// keep it in sync with the catalog after Open (the map is shared by
	// reference with the Locator already embedded in memStore).
	serviceDirs map[string]string
	// readOnlyServices names services read from a managed git clone, whose
	// package must never be written into (the daemon resets it on every
	// sync). It backs memory.Locator.ReadOnly and example.Locator.ReadOnly,
	// shared by reference like serviceDirs, and is refreshed alongside it.
	readOnlyServices map[string]bool

	// gitMgr resolves and syncs git-sourced services (PLAN §18). Always
	// non-nil: Open always constructs one, so a git source is usable
	// without any config beyond `type: git` in the workspace file.
	gitMgr *gitsrc.Manager
	// gitSyncInterval is the period Watch mode's background
	// registry.Syncer.SyncGitPeriodically runs on.
	gitSyncInterval time.Duration

	// semIdx is the semantic vector index (PLAN §16), or nil when semantic
	// search is disabled (the common case: off by default). semQueue feeds
	// a single background worker goroutine (started in Open alongside
	// semIdx) that does the actual embedding/upsert work, so a sync/apply
	// or memory write is never blocked on an embedding call; semCancel
	// stops that goroutine on Close.
	semIdx    *semantic.Index
	semQueue  chan semanticJob
	semCancel context.CancelFunc
	// semDone is closed by semanticWorker when it exits; stopSemantic waits
	// on it so Close never closes l.db while the worker might still be
	// running a job against it.
	semDone chan struct{}

	watcher     *registry.Watcher
	watchCancel context.CancelFunc
}

var _ engine.Engine = (*Local)(nil)

// Open opens (creating if necessary) the workspace's SQLite database, wires
// the catalog/search/registry stack, runs the staleness check described in
// PLAN §4 (unless opts.SkipStaleCheck), and — if opts.Watch is set — starts
// the registry file watcher.
func Open(ws *domain.Workspace, opts Options) (*Local, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if err := workspace.EnsureStateDir(ws); err != nil {
		return nil, err
	}

	db, err := store.Open(workspace.DBPath(ws))
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(ws)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	cat := catalog.New(db)
	srch := search.New(db)
	bus := events.New()
	syncer := registry.NewSyncer(ws, &catalogIndexer{cat: cat, srch: srch}, bus)

	gitCacheDir := opts.GitCacheDir
	if gitCacheDir == "" {
		gitCacheDir = cfg.Git.CacheDir
	}
	gitMgr := gitsrc.New(gitsrc.Options{
		CacheDir: gitCacheDir,
		Timeout:  cfg.Git.TimeoutDuration(),
		Logger:   logger,
	})
	syncer.WithGit(gitMgr)

	gitSyncInterval := opts.GitSyncInterval
	if gitSyncInterval <= 0 {
		gitSyncInterval = cfg.Git.SyncIntervalDuration()
	}

	secrets := opts.Secrets
	if secrets == nil {
		secrets = env.DefaultChain(ws)
	}
	runsStore := runs.New(db)

	l := &Local{
		ws:               ws,
		db:               db,
		cat:              cat,
		srch:             srch,
		bus:              bus,
		syncer:           syncer,
		logger:           logger,
		runsStore:        runsStore,
		secrets:          secrets,
		serviceDirs:      map[string]string{},
		readOnlyServices: map[string]bool{},
		gitMgr:           gitMgr,
		gitSyncInterval:  gitSyncInterval,
	}

	if err := l.setupSemantic(cfg, opts); err != nil {
		_ = db.Close()
		return nil, err
	}

	loc := memory.Locator{
		WorkspaceDir: ws.Dir,
		LocalDir:     workspace.LocalDir(ws),
		ServiceDirs:  l.serviceDirs,
		ReadOnly:     l.readOnlyServices,
		FlowOwner: func(flowID string) (string, string) {
			fs, err := cat.GetFlowSummary(context.Background(), flowID)
			if err != nil || fs == nil {
				return "workspace", ""
			}
			return fs.OwnerKind, fs.OwnerID
		},
	}
	l.memStore = memory.New(db, loc, &retrieval.CatalogResolver{Cat: cat})
	l.exStore = example.New(db, example.Locator{WorkspaceDir: ws.Dir, ServiceDirs: l.serviceDirs, ReadOnly: l.readOnlyServices})
	l.ctxBuilder = retrieval.New(cat, srch, l.memStore, runsStore)
	l.runner = runner.New(&operationsAdapter{cat: cat})

	// Populate the service-dir map used by memory.Locator and
	// example.Locator from the catalog as it stands (services registered by
	// earlier runs), so a service-scoped memory or example written during
	// this invocation lands in the right repo even before the stale check
	// has run; the map is refreshed again below once staleCheck has synced
	// every registered service.
	l.refreshServiceDirs(context.Background())

	if !opts.SkipStaleCheck {
		if err := l.staleCheck(context.Background()); err != nil {
			l.stopSemantic()
			_ = db.Close()
			return nil, err
		}
	}
	l.refreshServiceDirs(context.Background())

	if !opts.SkipStaleCheck {
		// Memories and examples live in the workspace and in every
		// registered service's repo; reindex each store once when any of
		// those directories changed, now that the service directories are
		// known. Best effort: a failure never fails Open.
		l.reindexKnowledgeAreas(context.Background())
	}

	if opts.Watch {
		if err := l.startWatch(); err != nil {
			l.stopSemantic()
			_ = db.Close()
			return nil, err
		}
	}

	return l, nil
}

// Workspace returns the workspace this engine was opened against.
func (l *Local) Workspace() *domain.Workspace { return l.ws }

// Services returns the service management API.
func (l *Local) Services() engine.ServiceAPI { return &serviceAPI{l: l} }

// Catalog returns the read-only catalog API.
func (l *Local) Catalog() engine.CatalogAPI { return &catalogAPI{l: l} }

// Search returns the search API.
func (l *Local) Search() engine.SearchAPI { return &searchAPI{l: l} }

// Events returns the event subscription API.
func (l *Local) Events() engine.EventAPI { return &eventAPI{l: l} }

// Flows returns the flow management API (PLAN §8).
func (l *Local) Flows() engine.FlowAPI { return &flowAPI{l: l} }

// Runner returns the flow/call execution API (PLAN §9).
func (l *Local) Runner() engine.RunnerAPI { return &runnerAPI{l: l} }

// Runs returns the run history API (PLAN §9, §15).
func (l *Local) Runs() engine.RunAPI { return &runAPI{l: l} }

// Memories returns the memory management API (PLAN §10-13, §26).
func (l *Local) Memories() engine.MemoryAPI { return &memoryAPI{l: l} }

// Examples returns the saved-example API (PLAN §34b).
func (l *Local) Examples() engine.ExampleAPI { return &exampleAPI{l: l} }

// Context returns the agent context builder API (PLAN §14).
func (l *Local) Context() engine.ContextAPI { return &contextAPI{l: l} }

// Envs returns the environments/secrets API (PLAN §7, §20).
func (l *Local) Envs() engine.EnvAPI { return &envAPI{l: l} }

// Stats summarizes the size of the catalog (PLAN §21's `sapien reindex`
// output). It is not part of engine.Engine (which has no facade for it
// yet); callers that want it type-assert the concrete *Local (or any future
// engine implementation that chooses to expose the same method).
func (l *Local) Stats(ctx context.Context) (catalog.Stats, error) {
	return l.cat.Stats(ctx)
}

// Close stops the watcher (if running), the semantic indexing worker (if
// running), and closes the database.
func (l *Local) Close() error {
	dropRing(l)
	if l.watcher != nil {
		_ = l.watcher.Close()
	}
	if l.watchCancel != nil {
		l.watchCancel()
	}
	l.stopSemantic()
	return l.db.Close()
}

// catalogIndexer adapts *catalog.Catalog to registry.Indexer. registry.Snapshot
// and catalog.Snapshot mirror each other field-for-field (same field names,
// order, and types), so the conversion is a plain struct conversion.
type catalogIndexer struct {
	cat  *catalog.Catalog
	srch *search.Searcher
}

func (i *catalogIndexer) ReviewTasks(ctx context.Context, snap registry.Snapshot) (domain.Service, error) {
	svc, err := i.cat.GetService(ctx, snap.Service.ID)
	if err != nil {
		return domain.Service{}, err
	}
	// Builder ran acceptance before runtime retrieval assertions existed.
	// Recompute stale acceptances now that both static and runtime warnings
	// are known.
	filtered := svc.Warnings[:0]
	for _, warning := range svc.Warnings {
		if warning.Code != "STALE_ACCEPTANCE" {
			filtered = append(filtered, warning)
		}
	}
	svc.Warnings = filtered
	matchedRules := make([]bool, len(snap.Service.WarningRules))
	for ri, rule := range snap.Service.WarningRules {
		for _, accepted := range svc.AcceptedWarnings {
			if registry.WarningMatches(accepted.LintWarning, rule) {
				matchedRules[ri] = true
			}
		}
	}

	coverage := domain.TaskCoverage{Tasks: len(snap.Tasks)}
	for _, task := range snap.Tasks {
		for _, test := range task.Tests {
			coverage.Assertions++
			results, err := i.srch.Operations(ctx, test.Query, domain.SearchOptions{Service: svc.Name, Limit: test.TopK, Deterministic: true})
			if err != nil {
				return domain.Service{}, err
			}
			found := false
			for _, result := range results {
				for _, expected := range test.ExpectAny {
					if result.Operation.ID == expected {
						found = true
					}
				}
			}
			if found {
				coverage.Discoverable++
				continue
			}
			warning := domain.LintWarning{Code: "UNDISCOVERABLE_OPERATION", Message: fmt.Sprintf("task %q query %q did not return any of %s in the top %d", task.ID, test.Query, strings.Join(test.ExpectAny, ", "), test.TopK), Source: &domain.SourceLoc{File: "service.yaml", Pointer: "/tasks/" + task.ID + "/tests"}}
			accepted, ok := registry.AcceptWarning(warning, snap.Service.WarningRules)
			if ok {
				svc.AcceptedWarnings = append(svc.AcceptedWarnings, accepted)
				for ri, rule := range snap.Service.WarningRules {
					if registry.WarningMatches(warning, rule) {
						matchedRules[ri] = true
					}
				}
			} else {
				svc.Warnings = append(svc.Warnings, warning)
			}
		}
	}
	for ri, rule := range snap.Service.WarningRules {
		if !matchedRules[ri] {
			svc.Warnings = append(svc.Warnings, domain.LintWarning{Code: "STALE_ACCEPTANCE", Message: fmt.Sprintf("accepted_warnings entry for %s (match %q) matches no warning; remove it", rule.Code, rule.Match)})
		}
	}
	svc.TaskCoverage = &coverage
	if err := i.cat.UpdateServiceDiagnostics(ctx, *svc); err != nil {
		return domain.Service{}, err
	}
	return *svc, nil
}

func (i *catalogIndexer) Apply(ctx context.Context, snap registry.Snapshot) (domain.CatalogChange, error) {
	return i.cat.Apply(ctx, catalog.Snapshot(snap))
}

func (i *catalogIndexer) MarkServiceError(ctx context.Context, svc domain.Service, msg string) error {
	return i.cat.MarkServiceError(ctx, svc, msg)
}

func (i *catalogIndexer) RemoveService(ctx context.Context, id string) error {
	return i.cat.RemoveService(ctx, id)
}

// settingsKey/settingsGet/settingsSet implement the tiny key/value
// bookkeeping Local needs on top of the `settings` table (PLAN §15):
// per-service content fingerprints, used by the staleness check (staleness.go).
func fingerprintKey(service string) string { return "fingerprint:" + service }

func settingsGet(ctx context.Context, db *store.DB, key string) (string, bool) {
	var value string
	err := db.SQL().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", false
	}
	return value, true
}

func settingsSet(ctx context.Context, db *store.DB, key, value string) error {
	_, err := db.SQL().ExecContext(ctx, `INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, value)
	return err
}

func settingsDelete(ctx context.Context, db *store.DB, key string) error {
	_, err := db.SQL().ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}

// findRef returns the workspace's ServiceRef named name.
func findRef(ws *domain.Workspace, name string) (domain.ServiceRef, bool) {
	for _, r := range ws.Services {
		if r.Name == name {
			return r, true
		}
	}
	return domain.ServiceRef{}, false
}

// refreshServiceDirs fills l.serviceDirs (shared by reference with the
// memory and example locators) from the catalog's registered services.
func (l *Local) refreshServiceDirs(ctx context.Context) {
	svcs, err := l.cat.ListServices(ctx)
	if err != nil {
		return
	}
	for _, s := range svcs {
		if s.PackageDir != "" {
			l.serviceDirs[s.Name] = s.PackageDir
		}
		l.setReadOnly(s.Name, s.Source)
	}
}

// setReadOnly records whether service-scoped knowledge may be written for
// name: not when its effective source is a git source, because that is
// read from a managed clone the daemon resets on every sync (PLAN §7b). A
// local source, including a per-machine override of a git source, is
// writable.
func (l *Local) setReadOnly(name string, src domain.Source) {
	if src.Kind == domain.SourceGit {
		l.readOnlyServices[name] = true
		return
	}
	delete(l.readOnlyServices, name)
}
