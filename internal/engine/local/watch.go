package local

import (
	"context"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/registry"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// watchDebounce mirrors PLAN §17's default fsnotify debounce.
const watchDebounce = 200 * time.Millisecond

// startWatch builds the (name -> Package) map registry.NewWatcher needs,
// starts the watcher, and wires its onChange callback to resync whatever
// changed (PLAN §4's daemon mode). Services whose package can't currently
// be discovered (a bad path, or a git source whose clone can't be resolved)
// are simply left out of the watch set; the CLI's own staleness check is
// what surfaces those as errors, not the watcher.
//
// A git-sourced service is watched at its checkout's package directory,
// exactly like a local one (PLAN §18: `sapien service sync` run from
// another process, or a branch that moves on disk some other way, is
// picked up the same way an edited local file is). Alongside the watcher,
// this also starts registry.Syncer.SyncGitPeriodically in a goroutine tied
// to the same lifetime (stopped by Close, via watchCancel): that is the
// daemon's own timer-driven git fetch (PLAN §18), the one-shot CLI's
// staleness check deliberately never fetches (staleness.go).
func (l *Local) startWatch() error {
	packages := map[string]*registry.Package{}
	for _, ref := range l.ws.Services {
		root, err := l.resolvePackageRootForWatch(ref)
		if err != nil {
			continue
		}
		pkg, err := registry.DiscoverPackage(root, ref.Source.Contract)
		if err != nil {
			continue
		}
		packages[ref.Name] = pkg
	}

	w, err := registry.NewWatcher(l.ws, packages, watchDebounce, l.onWatchChange)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := w.Start(ctx); err != nil {
		cancel()
		return err
	}

	l.watcher = w
	l.watchCancel = cancel

	if l.gitMgr != nil {
		go l.syncer.SyncGitPeriodically(ctx, l.gitSyncInterval)
	}

	return nil
}

// resolvePackageRootForWatch resolves the directory startWatch should pass
// to registry.DiscoverPackage for ref: the resolved local path, or -- for a
// git source -- the managed clone's checkout directory, resolved via
// gitMgr.Ensure (no network call for a clone that already exists on disk;
// see staleCheck's doc comment in staleness.go).
func (l *Local) resolvePackageRootForWatch(ref domain.ServiceRef) (string, error) {
	switch ref.Source.Kind {
	case domain.SourceLocal:
		return workspace.ResolveSourcePath(l.ws, ref.Source)
	case domain.SourceGit:
		if l.gitMgr == nil {
			return "", errNotFingerprintable
		}
		checkout, err := l.gitMgr.Ensure(context.Background(), ref.Source)
		if err != nil {
			return "", err
		}
		return checkout.PackageDir, nil
	default:
		return "", errNotFingerprintable
	}
}

// onWatchChange is registry.Watcher's onChange callback: it resyncs every
// changed service (storing its refreshed fingerprint on success) and
// reindexes workspace-level areas ("flows", "memories"); "environments" and
// "workspace" (the workspace file itself) need no reindex -- they are read
// straight from disk on every use.
func (l *Local) onWatchChange(ch registry.Change) {
	ctx := context.Background()

	for _, name := range ch.Services {
		svc, err := l.syncer.SyncOne(ctx, name)
		if err == nil {
			if ref, ok := findRef(l.ws, name); ok {
				l.refreshFingerprint(ctx, ref)
			}
			if svc != nil && svc.Status == domain.SyncOK {
				l.enqueueSemanticIndex(name)
			}
		}
	}

	for _, area := range ch.Workspace {
		switch area {
		case "flows":
			if err := l.reindexWorkspaceFlows(ctx); err != nil {
				l.logger.Warn("reindexing workspace flows failed", "error", err)
				continue
			}
			l.emit(domain.EventFlowChanged, map[string]string{"area": "flows"})
		case "memories":
			if _, err := l.memStore.Reindex(ctx); err != nil {
				l.logger.Warn("reindexing workspace memories failed", "error", err)
			} else {
				l.enqueueSemanticMemoryIndex(nil)
			}
		case "environments", "workspace":
			// No index to refresh: read directly from disk on every use.
		default:
			l.logger.Debug("workspace change observed; no handler for this area", "area", area)
		}
	}
}
