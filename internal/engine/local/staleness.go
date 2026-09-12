package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/example"
	"github.com/gs-sinha/sapien/internal/registry"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// staleCheck implements PLAN §4's one-shot CLI rule: for every registered
// service, compare a cheap content fingerprint (PackageFingerprint) of its
// package against the fingerprint recorded the last time it was
// successfully synced (settings key "fingerprint:<name>"). A service is
// resynced when its fingerprint changed, when no fingerprint could be
// computed (so staleness can't be ruled out), or when it is missing from
// the catalog entirely (first run). A resync failure is not fatal to Open:
// registry.Syncer already records it as a service error and emits
// service.sync_failed; staleCheck simply moves on to the next service.
//
// Git services (PLAN §18): computeFingerprint resolves a git source via
// gitsrc.Manager.Ensure, which -- for a clone that already exists on disk
// (the common case: a previous Add/Sync already created it) -- never shells
// out to `git fetch` or touches the network at all, only local commands
// (rev-parse HEAD, etc.) to read the currently checked-out commit. So a
// git-sourced service's staleness check here is exactly as cheap as a local
// one and, deliberately, can never observe an upstream change: a git
// service is only ever re-fetched by Services().Sync/Reindex or the
// daemon's SyncGitPeriodically timer (watch.go), never by opening the
// engine.
//
// That holds for the resync this triggers as well, not just for the
// fingerprint: it goes through SyncOneFromDisk. Until 2026-09-12 it called
// SyncOne, which fetches, so opening the engine did reach the network and
// `git reset --hard` every git-sourced clone -- the opposite of what the
// paragraph above promises, and of what a staleness check is for.
func (l *Local) staleCheck(ctx context.Context) error {
	for _, ref := range l.ws.Services {
		fp, fpErr := l.computeFingerprint(ctx, ref)

		needsSync := fpErr != nil
		if fpErr == nil {
			stored, ok := settingsGet(ctx, l.db, fingerprintKey(ref.Name))
			if !ok || stored != fp {
				needsSync = true
			}
		}
		if _, err := l.cat.GetService(ctx, ref.Name); err != nil {
			needsSync = true
		}

		if !needsSync {
			continue
		}

		svc, err := l.syncer.SyncOneFromDisk(ctx, ref.Name)
		if err == nil {
			if fpErr == nil {
				_ = settingsSet(ctx, l.db, fingerprintKey(ref.Name), fp)
			}
			if svc != nil && svc.Status == domain.SyncOK {
				l.enqueueSemanticIndex(ref.Name)
			}
		}
	}

	// Workspace flows reindex cheaply the same way: a stored fingerprint
	// (mtimes+sizes of every file under the directory) that, when it no
	// longer matches, triggers exactly one reindex. Memories and examples
	// are handled by reindexKnowledgeAreas, which Open runs after the
	// service directories are known, because both stores also read
	// <service>/api/{memories,examples}/ from every registered service.
	l.checkWorkspaceArea(ctx, "workspace-flows", filepath.Join(l.ws.Dir, domain.FlowsDir), l.reindexWorkspaceFlows)

	return nil
}

// reindexKnowledgeAreas fingerprints every directory the memory and example
// stores read -- the workspace's memories/ and examples/ plus each
// registered service's api/memories/ and api/examples/ -- and reindexes a
// store once when any of its areas changed. It must run after
// l.serviceDirs is populated: a memory or example committed in a service
// repo is otherwise invisible to a one-shot CLI Open (which never runs the
// daemon's file watcher) until an explicit reindex.
func (l *Local) reindexKnowledgeAreas(ctx context.Context) {
	memDirs := map[string]string{"workspace-memories": filepath.Join(l.ws.Dir, domain.MemoriesDir)}
	exDirs := map[string]string{"workspace-examples": filepath.Join(l.ws.Dir, example.ExamplesDir)}
	for name, dir := range l.serviceDirs {
		if dir == "" {
			continue
		}
		memDirs["service-memories:"+name] = filepath.Join(dir, domain.MemoriesDir)
		exDirs["service-examples:"+name] = filepath.Join(dir, example.ExamplesDir)
	}

	l.reindexAreasOnce(ctx, memDirs, func(ctx context.Context) error {
		_, err := l.memStore.Reindex(ctx)
		if err == nil {
			l.enqueueSemanticMemoryIndex(nil)
		}
		return err
	})
	l.reindexAreasOnce(ctx, exDirs, func(ctx context.Context) error {
		_, err := l.exStore.Reindex(ctx)
		return err
	})
}

// reindexAreasOnce runs reindex at most once when any area's fingerprint
// differs from the stored one, then records every area's fingerprint.
func (l *Local) reindexAreasOnce(ctx context.Context, areas map[string]string, reindex func(context.Context) error) {
	fps := make(map[string]string, len(areas))
	changed := false
	for area, dir := range areas {
		fp, err := dirFingerprint(dir)
		if err != nil {
			continue
		}
		fps[area] = fp
		if stored, ok := settingsGet(ctx, l.db, fingerprintKey(area)); !ok || stored != fp {
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := reindex(ctx); err != nil {
		return
	}
	for area, fp := range fps {
		_ = settingsSet(ctx, l.db, fingerprintKey(area), fp)
	}
}

// checkWorkspaceArea compares dir's fingerprint against the one stored the
// last time this area was reindexed, calling reindex and updating the
// stored fingerprint only when it changed (or was never recorded). Best
// effort: a fingerprinting or reindex failure is swallowed, at worst
// costing an extra reindex on the next Open rather than failing it.
func (l *Local) checkWorkspaceArea(ctx context.Context, area, dir string, reindex func(context.Context) error) {
	fp, err := dirFingerprint(dir)
	if err != nil {
		return
	}
	key := fingerprintKey(area)
	if stored, ok := settingsGet(ctx, l.db, key); ok && stored == fp {
		return
	}
	if err := reindex(ctx); err != nil {
		return
	}
	_ = settingsSet(ctx, l.db, key, fp)
}

// dirFingerprint hashes the name, size, and modification time of every file
// under dir (recursively), sorted for determinism. A missing directory
// fingerprints as a stable constant rather than erroring, since a fresh
// workspace has no flows/memories directory content yet.
func dirFingerprint(dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return "empty", nil
		}
		return "", err
	}

	var entries []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		entries = append(entries, rel+":"+info.ModTime().UTC().Format("20060102T150405.000000000")+":"+strconv.FormatInt(info.Size(), 10))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// computeFingerprint discovers ref's package and computes its content
// fingerprint. It returns an error for anything that keeps a fingerprint
// from being computed (an unresolvable source, a missing package, an
// unsupported source kind) rather than treating that as fatal: the caller
// reacts by resyncing unconditionally, and the resulting sync error (if
// any) is what actually gets surfaced.
//
// For a git source, the fingerprint folds in the resolved commit
// (registry.PackageFingerprint's extra argument) alongside file content, so
// two commits that happen to produce byte-identical files (an empty commit,
// a no-op merge) still change the fingerprint -- matching the exact case
// PackageFingerprint's own doc comment calls out. Resolving the commit is
// gitMgr.Ensure, which touches the network only on a clone's first-ever use
// (see staleCheck's doc comment).
func (l *Local) computeFingerprint(ctx context.Context, ref domain.ServiceRef) (string, error) {
	switch ref.Source.Kind {
	case domain.SourceLocal:
		root, err := workspace.ResolveSourcePath(l.ws, ref.Source)
		if err != nil {
			return "", err
		}
		pkg, err := registry.DiscoverPackage(root, ref.Source.Contract)
		if err != nil {
			return "", err
		}
		return registry.PackageFingerprint(pkg)

	case domain.SourceGit:
		if l.gitMgr == nil {
			return "", errNotFingerprintable
		}
		checkout, err := l.gitMgr.Ensure(ctx, ref.Source)
		if err != nil {
			return "", err
		}
		pkg, err := registry.DiscoverPackage(checkout.PackageDir, ref.Source.Contract)
		if err != nil {
			return "", err
		}
		return registry.PackageFingerprint(pkg, checkout.Commit)

	default:
		return "", errNotFingerprintable
	}
}

// refreshFingerprint recomputes and stores ref's fingerprint after a
// successful sync (Add, Sync, or a watch-triggered resync). Failures are
// swallowed: a stale/missing fingerprint only ever costs an extra resync on
// the next Open, never correctness.
func (l *Local) refreshFingerprint(ctx context.Context, ref domain.ServiceRef) {
	fp, err := l.computeFingerprint(ctx, ref)
	if err != nil {
		return
	}
	_ = settingsSet(ctx, l.db, fingerprintKey(ref.Name), fp)
}

type fingerprintError string

func (e fingerprintError) Error() string { return string(e) }

const errNotFingerprintable fingerprintError = "local: fingerprinting is only supported for local sources"
