package local

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/gitsrc"
	"github.com/gs-sinha/sapien/internal/registry"
)

// browseSkipDirs names directories that are never worth listing as
// checkout candidates: build output, not a developer's own clones.
// Dotfiles are skipped unconditionally, in the caller.
var browseSkipDirs = map[string]bool{"node_modules": true}

// BrowseCheckouts lists dir's subdirectories for the checkout picker (PLAN
// §7b), describing each git repository found and saying whether it is a
// checkout of name's team repository. dir "" means the user's home
// directory.
func (s *serviceAPI) BrowseCheckouts(ctx context.Context, name, dir string) (*engine.DirListing, error) {
	l := s.l
	ref, ok := findRef(l.ws, name)
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}

	root, err := browseDir(dir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "reading %s", root)
	}

	team := teamSourceOf(ref)
	wantOrigin, teamRef := "", ""
	if team.Kind == domain.SourceGit {
		wantOrigin = gitsrc.NormalizeRemote(team.URL)
		teamRef = team.Ref
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, ".") || browseSkipDirs[n] {
			continue
		}
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })

	out := make([]engine.DirEntry, 0, len(names))
	for _, n := range names {
		out = append(out, l.describeBrowseEntry(ctx, n, filepath.Join(root, n), wantOrigin, teamRef, ref))
	}

	listing := &engine.DirListing{Path: root, Entries: out}
	if parent := filepath.Dir(root); parent != root {
		listing.Parent = parent
	}
	return listing, nil
}

// browseDir resolves BrowseCheckouts' dir argument to an absolute,
// existing directory: "" means the home directory, and "~" is expanded
// the same way checkoutDir expands it for Bind.
func browseDir(dir string) (string, error) {
	p := dir
	switch {
	case p == "":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		p = home
	case p == "~" || strings.HasPrefix(p, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "resolving %q", dir)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", errs.New(errs.Invalid, "not a directory: %s", abs).WithDetail("path", abs)
	}
	return abs, nil
}

// describeBrowseEntry classifies one subdirectory of a BrowseCheckouts
// listing. A cheap read of .git/config decides the origin without
// shelling out; the expensive DescribeAgainst + DiscoverPackage pair only
// runs for the one entry (if any) whose origin actually matches the
// service's team repository -- everything else in a large directory
// listing is answered from a stat and a file read.
func (l *Local) describeBrowseEntry(ctx context.Context, name, path, wantOrigin, teamRef string, ref domain.ServiceRef) engine.DirEntry {
	entry := engine.DirEntry{Name: name, Path: path}

	gitEntry := filepath.Join(path, ".git")
	info, statErr := os.Stat(gitEntry)
	if statErr != nil {
		return entry // a plain directory, not a git repository at all
	}

	var origin string
	if info.IsDir() {
		origin, _ = parseOriginURL(readFileBestEffort(filepath.Join(gitEntry, "config")))
	} else if described, err := l.gitMgr.Describe(ctx, path); err == nil && described != nil {
		// A worktree's ".git" is a file naming a private gitdir with no
		// config of its own; asking git directly is simpler than resolving
		// the shared config path by hand, and worktrees are rare enough
		// that the extra cost here does not matter the way it would for an
		// ordinary clone.
		origin = described.Remote
	}

	if wantOrigin == "" || gitsrc.NormalizeRemote(origin) != wantOrigin {
		entry.Matches = false
		if origin != "" {
			entry.Checkout = &domain.LocalCheckout{Path: path, Remote: origin}
			entry.Reason = "clone of " + origin
		}
		return entry
	}

	co, err := l.gitMgr.DescribeAgainst(ctx, path, teamRef)
	if err != nil || co == nil {
		entry.Matches = false
		entry.Checkout = &domain.LocalCheckout{Path: path, Remote: origin}
		entry.Reason = "clone of " + origin
		return entry
	}
	if pkg, perr := registry.DiscoverPackage(path, teamContract(ref)); perr == nil {
		co.Package = pkg.Dir
	}

	entry.Checkout = co
	entry.Matches = true
	if co.Package == "" {
		entry.Reason = "no API package under this checkout"
	}
	return entry
}

// readFileBestEffort returns path's contents, or "" if it cannot be read.
func readFileBestEffort(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// parseOriginURL extracts `url = ...` from a git config's
// `[remote "origin"]` section. It returns ("", false) when no such
// section or key is present.
func parseOriginURL(config string) (string, bool) {
	inOrigin := false
	for _, line := range strings.Split(config, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inOrigin = strings.EqualFold(trimmed, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		if k, v, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(k) == "url" {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}
