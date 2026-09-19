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
		// The workspace's own repository (PLAN §7b) gets the same tick, on
		// the same ctx so restartWatch's cancel stops this goroutine too
		// rather than doubling it, but its own timer: fetching it is
		// unrelated to whether any service is git-sourced.
		go l.fetchRepoPeriodically(ctx, l.gitSyncInterval)
	}

	return nil
}

// restartWatch tears the watcher down and starts a fresh one, so its watch
// set follows a source that just changed under it: a bind moves a service
// from the managed clone to a checkout the old watcher never looked at,
// and an unbind moves it back. Cancelling watchCancel also stops the
// SyncGitPeriodically goroutine startWatch began beside the old watcher,
// and startWatch begins a new one on the new context, so the daemon's git
// timer is restarted rather than doubled.
func (l *Local) restartWatch() error {
	if l.watchCancel != nil {
		l.watchCancel()
	}
	if l.watcher != nil {
		_ = l.watcher.Close()
	}
	l.watcher, l.watchCancel = nil, nil
	return l.startWatch()
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
		checkout, err := l.gitMgr.Ensure(context.Background(), ref.EffectiveSource())
		if err != nil {
			return "", err
		}
		return checkout.PackageDir, nil
	default:
		return "", errNotFingerprintable
	}
}

// defaultRepoFetchInterval mirrors registry.defaultGitSyncInterval for the
// case interval <= 0, which only happens when a caller other than Open's
// own defaulting passes one (Open always resolves gitSyncInterval from
// config before it reaches here).
const defaultRepoFetchInterval = 10 * time.Minute

// fetchRepoPeriodically fetches the workspace's own git repository (PLAN
// §7b) every interval, until ctx is done: the daemon's git tick for the
// team's shared copy of the workspace tier, run alongside the per-service
// SyncGitPeriodically this same startWatch call starts.
//
// It only ever fetches, never pulls. The tick is deliberately read-only for
// the workspace: a service's managed clone is a cache nobody edits by hand,
// so resetting it on a timer is safe, but the workspace directory is the
// developer's own working copy -- it can hold uncommitted flows or memories
// at any moment -- and turning "behind by N" into a pulled working tree is
// a decision only the developer gets to make, via Pull or "Sync all", never
// a background timer.
func (l *Local) fetchRepoPeriodically(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = defaultRepoFetchInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastErr := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lastErr = l.fetchRepoTick(ctx, lastErr)
		}
	}
}

// fetchRepoTick runs one fetchRepoPeriodically cycle and returns the
// fetch's error message (or "") for the next tick to compare against, so a
// failure logs at Warn only when it first appears or its message changes,
// never on every repeat of the same failure, and an Info line marks
// recovery. Skips entirely when the workspace isn't inside a git
// repository, or has no origin to fetch.
func (l *Local) fetchRepoTick(ctx context.Context, lastErr string) string {
	status, err := l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil || !status.InGit || status.Remote == "" {
		return lastErr
	}

	_, fetchErr := l.Repo().Fetch(ctx)
	if fetchErr != nil {
		msg := fetchErr.Error()
		if msg != lastErr {
			l.logger.Warn("fetching the workspace repository failed", "error", fetchErr)
		}
		return msg
	}
	if lastErr != "" {
		l.logger.Info("fetching the workspace repository recovered")
	}
	return ""
}

// onWatchChange is registry.Watcher's onChange callback: it resyncs every
// changed service (storing its refreshed fingerprint on success) and
// reindexes workspace-level areas ("flows", "memories"); "environments" and
// "workspace" (the workspace file itself) need no reindex -- they are read
// straight from disk on every use.
func (l *Local) onWatchChange(ch registry.Change) {
	ctx := context.Background()

	for _, name := range ch.Services {
		// From disk, never fetching: the watcher fired because files here
		// changed, which says nothing about the remote, and fetching would
		// also `git reset --hard` the clone -- discarding the very edits
		// that woke us. See registry.Syncer.SyncOneFromDisk.
		svc, err := l.syncer.SyncOneFromDisk(ctx, name)
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
