package gitsrc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// metaFileName is the small marker file Manager writes into every managed
// clone, recording the information List needs without re-deriving it from
// the workspace. It is untracked by git, so `git reset --hard` and
// sparse-checkout never touch it.
const metaFileName = "sapien.json"

// Checkout is a resolved, on-disk managed clone of one git source.
type Checkout struct {
	Dir        string // absolute; the repository root (<CacheDir>/<hash>-<slug>)
	PackageDir string // absolute; Dir/Subdir -- the API package root
	Commit     string // resolved commit sha at HEAD
	Ref        string // the ref actually checked out: branch, tag, or commit
	URL        string
	Subdir     string
}

// repoMeta is the JSON shape of sapien.json.
type repoMeta struct {
	URL    string `json:"url"`
	Ref    string `json:"ref"`
	Subdir string `json:"subdir"`
}

// Ensure resolves src to a Checkout, cloning it first if the managed clone
// does not exist yet. An existing clone is never fetched or re-checked-out
// here -- it is simply resolved (dir, current commit, recorded ref/subdir);
// use Sync to bring it up to date. src.Subdir defaults to "api"; src.Ref
// defaults to the remote's default branch, resolved once at clone time (or,
// for a clone predating Sapien, on first Ensure) and remembered afterward.
func (m *Manager) Ensure(ctx context.Context, src domain.Source) (*Checkout, error) {
	subdir, err := validateGitSource(src)
	if err != nil {
		return nil, err
	}

	dir := m.Dir(src.URL)
	fresh := !isGitRepo(dir)

	if fresh {
		if err := m.clone(ctx, src.URL, dir, subdir); err != nil {
			return nil, err
		}
	}

	ref := src.Ref
	if ref == "" {
		if meta, ok := readMeta(dir); ok && meta.Ref != "" {
			ref = meta.Ref
		}
	}
	if ref == "" {
		resolved, err := m.resolveDefaultRef(ctx, dir, src.URL)
		if err != nil {
			return nil, err
		}
		ref = resolved
	}

	if fresh {
		if _, err := m.run(ctx, dir, "checkout", ref); err != nil {
			return nil, withDetail(err, "url", src.URL)
		}
	}

	commit, err := m.currentCommit(ctx, dir)
	if err != nil {
		return nil, withDetail(err, "url", src.URL)
	}

	if err := writeMeta(dir, repoMeta{URL: src.URL, Ref: ref, Subdir: subdir}); err != nil {
		return nil, err
	}

	return &Checkout{
		Dir:        dir,
		PackageDir: filepath.Join(dir, subdir),
		Commit:     commit,
		Ref:        ref,
		URL:        src.URL,
		Subdir:     subdir,
	}, nil
}

// Sync fetches src's remote and updates the managed clone to its configured
// ref (PLAN §18): a branch is reset hard to origin/<ref>; a tag or commit is
// checked out directly. It clones first (via Ensure) if the managed clone
// does not exist yet, which always reports changed=true. Otherwise changed
// reports whether the resolved commit moved. A clone with modified tracked
// files is never reset: Sync fails with errs.ServiceSource and leaves the
// modifications in place (see the guard below).
func (m *Manager) Sync(ctx context.Context, src domain.Source) (*Checkout, bool, error) {
	subdir, err := validateGitSource(src)
	if err != nil {
		return nil, false, err
	}

	dir := m.Dir(src.URL)

	if !isGitRepo(dir) {
		co, err := m.Ensure(ctx, src)
		if err != nil {
			return nil, false, err
		}
		return co, true, nil
	}

	before, err := m.currentCommit(ctx, dir)
	if err != nil {
		return nil, false, withDetail(err, "url", src.URL)
	}

	if _, err := m.run(ctx, dir, "fetch", "--prune", "origin"); err != nil {
		return nil, false, withDetail(err, "url", src.URL)
	}

	// Nothing may be written into a managed clone: the locators refuse
	// service-scoped writes for git sources, and a bound checkout is where
	// edits belong (PLAN §7b). This guard is the insurance behind that
	// rule. An editor opened on the cache path, or an agent built before
	// the rule existed, can still put work here, and the reset below would
	// erase it without a trace; refusing keeps the work and turns it into a
	// visible sync error that says where it should have gone. Untracked
	// files are not modifications (sapien.json lives here), and the check
	// runs before sparse-checkout can touch the tree.
	if dirty, err := m.run(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil {
		return nil, false, withDetail(err, "url", src.URL)
	} else if files := nonEmptyLines(dirty); len(files) > 0 {
		return nil, false, errs.New(errs.ServiceSource, "managed clone at %s has local modifications; refusing to reset", dir).
			WithDetail("url", src.URL).
			WithDetail("dir", dir).
			WithDetail("files", files).
			WithHint("this clone is a cache that Sapien resets on every sync; move the edits to a checkout of your own and read the service from it with `sapien service bind <name> <path>`, or discard them with `git -C " + dir + " checkout -- .`")
	}

	// Keep sparse-checkout in sync in case Subdir changed since the clone.
	if _, err := m.run(ctx, dir, "sparse-checkout", "set", subdir); err != nil {
		return nil, false, withDetail(err, "url", src.URL)
	}

	ref := src.Ref
	if ref == "" {
		if meta, ok := readMeta(dir); ok && meta.Ref != "" {
			ref = meta.Ref
		} else {
			resolved, err := m.resolveDefaultRef(ctx, dir, src.URL)
			if err != nil {
				return nil, false, err
			}
			ref = resolved
		}
	}

	if m.isBranch(ctx, dir, ref) {
		if _, err := m.run(ctx, dir, "reset", "--hard", "origin/"+ref); err != nil {
			return nil, false, withDetail(err, "url", src.URL)
		}
	} else {
		if _, err := m.run(ctx, dir, "checkout", ref); err != nil {
			return nil, false, withDetail(err, "url", src.URL)
		}
	}

	after, err := m.currentCommit(ctx, dir)
	if err != nil {
		return nil, false, withDetail(err, "url", src.URL)
	}

	if err := writeMeta(dir, repoMeta{URL: src.URL, Ref: ref, Subdir: subdir}); err != nil {
		return nil, false, err
	}

	return &Checkout{
		Dir:        dir,
		PackageDir: filepath.Join(dir, subdir),
		Commit:     after,
		Ref:        ref,
		URL:        src.URL,
		Subdir:     subdir,
	}, before != after, nil
}

// Remove deletes the managed clone for url, if any. Removing a clone that
// does not exist is not an error.
func (m *Manager) Remove(url string) error {
	dir := m.Dir(url)
	if err := os.RemoveAll(dir); err != nil {
		return errs.Wrap(errs.Internal, err, "removing managed clone %s", dir)
	}
	return nil
}

// List scans CacheDir for managed clones, reading each one's sapien.json
// and current commit. Entries that cannot be read (a directory without a
// sapien.json, or one whose commit cannot be resolved) are skipped rather
// than failing the whole listing, since List is meant to survive a partially
// cleaned-up cache. Results are sorted by URL. A missing CacheDir yields
// (nil, nil), not an error.
func (m *Manager) List() ([]Checkout, error) {
	entries, err := os.ReadDir(m.cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.Wrap(errs.Internal, err, "scanning %s", m.cacheDir)
	}

	out := make([]Checkout, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.cacheDir, e.Name())

		meta, ok := readMeta(dir)
		if !ok {
			continue
		}
		commit, err := m.currentCommit(context.Background(), dir)
		if err != nil {
			continue
		}

		out = append(out, Checkout{
			Dir:        dir,
			PackageDir: filepath.Join(dir, meta.Subdir),
			Commit:     commit,
			Ref:        meta.Ref,
			URL:        meta.URL,
			Subdir:     meta.Subdir,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// validateGitSource checks that src is usable by Ensure/Sync and returns
// its effective subdirectory (src.Subdir, defaulting to "api").
func validateGitSource(src domain.Source) (string, error) {
	if src.Kind != domain.SourceGit {
		return "", errs.New(errs.Invalid, "gitsrc: only git sources are supported, got %q", src.Kind)
	}
	if src.URL == "" {
		return "", errs.New(errs.ServiceSource, "git source has an empty url")
	}
	if !IsGitURL(src.URL) {
		return "", errs.New(errs.ServiceSource, "not a git url: %s", src.URL).
			WithDetail("url", src.URL).
			WithHint("git sources must be an SSH, HTTPS, git://, or file:// URL")
	}
	subdir := src.Subdir
	if subdir == "" {
		subdir = defaultSubdir
	}
	return subdir, nil
}

// clone creates a fresh, blobless, checkout-less clone of url at dir, then
// configures cone-mode sparse-checkout limited to subdir. It does not check
// out any ref; the caller does that once the ref is known.
func (m *Manager) clone(ctx context.Context, url, dir, subdir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "creating cache directory %s", filepath.Dir(dir))
	}
	// Best-effort cleanup of a partial clone left by an earlier failed attempt.
	_ = os.RemoveAll(dir)

	if _, err := m.run(ctx, "", "clone", "--filter=blob:none", "--no-checkout", url, dir); err != nil {
		_ = os.RemoveAll(dir)
		return withDetail(err, "url", url)
	}
	if _, err := m.run(ctx, dir, "sparse-checkout", "init", "--cone"); err != nil {
		return withDetail(err, "url", url)
	}
	if _, err := m.run(ctx, dir, "sparse-checkout", "set", subdir); err != nil {
		return withDetail(err, "url", url)
	}
	return nil
}

// resolveDefaultRef determines the remote's default branch: first via the
// origin/HEAD symbolic ref set by clone's initial fetch, falling back to
// `ls-remote --symref` (for a clone predating Sapien, or one where that
// tracking ref is missing for some other reason).
func (m *Manager) resolveDefaultRef(ctx context.Context, dir, url string) (string, error) {
	out, err := m.run(ctx, dir, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		if ref := strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/origin/"); ref != "" {
			return ref, nil
		}
	}

	out, err = m.run(ctx, "", "ls-remote", "--symref", url, "HEAD")
	if err != nil {
		return "", withDetail(err, "url", url)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "ref:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			return strings.TrimPrefix(fields[1], "refs/heads/"), nil
		}
	}

	return "", errs.New(errs.ServiceSource, "could not determine the default branch for %s", url).
		WithDetail("url", url).
		WithHint("set an explicit `ref` in the workspace file")
}

// currentCommit returns the commit sha HEAD resolves to in dir.
func (m *Manager) currentCommit(ctx context.Context, dir string) (string, error) {
	out, err := m.run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// isBranch reports whether ref names a branch that origin has (i.e.
// refs/remotes/origin/<ref> exists), as opposed to a tag or a raw commit.
func (m *Manager) isBranch(ctx context.Context, dir, ref string) bool {
	_, err := m.run(ctx, dir, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+ref)
	return err == nil
}

// isGitRepo reports whether dir looks like an existing git working tree.
func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func metaPath(dir string) string { return filepath.Join(dir, metaFileName) }

// readMeta reads dir's sapien.json. ok is false if it is absent or unreadable.
func readMeta(dir string) (repoMeta, bool) {
	data, err := os.ReadFile(metaPath(dir))
	if err != nil {
		return repoMeta{}, false
	}
	var meta repoMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return repoMeta{}, false
	}
	return meta, true
}

// writeMeta writes dir's sapien.json.
func writeMeta(dir string, meta repoMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "encoding %s", metaFileName)
	}
	if err := os.WriteFile(metaPath(dir), data, 0o644); err != nil {
		return errs.Wrap(errs.Internal, err, "writing %s", metaPath(dir))
	}
	return nil
}

// LastFetch reports when the managed clone at dir last fetched from its
// remote. Git records no fetch timestamp of its own, so this uses
// FETCH_HEAD's modification time, which every fetch rewrites.
//
// ok is false when the clone has never fetched since it was created: a
// clone made by Ensure stays in that state until something calls Sync, so
// its view of the remote is frozen at clone time. That distinction is what
// makes "no API package found" diagnosable -- a package pushed after the
// clone was made is invisible to it, and no amount of retrying changes
// that (see Builder.Build's error).
func LastFetch(dir string) (time.Time, bool) {
	info, err := os.Stat(filepath.Join(dir, ".git", "FETCH_HEAD"))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// ClonedAt reports when the managed clone at dir was created, using the
// birth of its .git directory. Used only to describe a clone in an error.
func ClonedAt(dir string) (time.Time, bool) {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
