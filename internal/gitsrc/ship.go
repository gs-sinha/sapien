package gitsrc

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// RepoRoot reports the git repository containing dir, or false when dir is
// not inside one -- or the query fails for any other reason; callers use
// this to decide whether the ship-state and commit features below apply at
// all, not to distinguish failure modes (PLAN §7b).
func (m *Manager) RepoRoot(ctx context.Context, dir string) (string, bool) {
	top, err := m.Toplevel(ctx, dir)
	if err != nil {
		return "", false
	}
	return top, true
}

// FileStates reports, for each of paths (absolute, inside repo), how far it
// has travelled toward the team: domain.ShipUntracked, ShipModified,
// ShipUnpushed, or ShipShipped (PLAN §7b -- "make the shipping state of
// every workspace-tier flow visible, from a read-only look at the workspace
// repository"). It never fetches, checks out, or writes anything.
//
// At most three git invocations run regardless of len(paths): one `status`
// classifies every untracked and modified path in one call; for whatever is
// left (tracked and clean), one `rev-parse @{upstream}` decides whether the
// unpushed-vs-shipped question can even be asked here, and if so one `log`
// answers it for every remaining path at once.
func (m *Manager) FileStates(ctx context.Context, repo string, paths []string) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	repo = filepath.Clean(repo)

	// absByRel maps git's repo-relative output back to the absolute paths
	// the caller gave us. Both `status` and `log` below report paths
	// relative to the process's cwd, which m.run always sets to repo for
	// these calls, so "relative to repo" and "relative to cwd" coincide.
	absByRel := make(map[string]string, len(paths))
	statusArgs := []string{"status", "--porcelain=v1", "--untracked-files=all", "--"}
	for _, p := range paths {
		abs := filepath.Clean(p)
		rel, err := filepath.Rel(repo, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errs.New(errs.Invalid, "path %s is not inside repository %s", p, repo)
		}
		absByRel[filepath.ToSlash(rel)] = abs
		statusArgs = append(statusArgs, abs)
	}

	statusOut, err := m.run(ctx, repo, statusArgs...)
	if err != nil {
		return nil, err
	}
	remaining := make(map[string]string, len(absByRel))
	for rel, abs := range absByRel {
		remaining[rel] = abs
	}
	for _, line := range strings.Split(statusOut, "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		rel := unquoteGitPath(renamedPath(line[3:]))
		abs, ok := remaining[rel]
		if !ok {
			continue
		}
		delete(remaining, rel)
		if xy == "??" {
			out[abs] = domain.ShipUntracked
			continue
		}
		// Anything else with a status -- staged, unstaged, added, renamed --
		// is a change nobody has committed yet.
		out[abs] = domain.ShipModified
	}
	if len(remaining) == 0 {
		return out, nil
	}

	// No upstream at all: nothing has ever been shared from this branch, so
	// every tracked-and-clean path is unpushed regardless of what HEAD
	// contains. Never fetch to find out.
	if _, err := m.run(ctx, repo, "rev-parse", "--abbrev-ref", "@{upstream}"); err != nil {
		for _, abs := range remaining {
			out[abs] = domain.ShipUnpushed
		}
		return out, nil
	}

	logArgs := []string{"log", "--name-only", "--format=", "@{upstream}..HEAD", "--"}
	for _, abs := range remaining {
		logArgs = append(logArgs, abs)
	}
	logOut, err := m.run(ctx, repo, logArgs...)
	if err != nil {
		return nil, err
	}
	touched := map[string]bool{}
	for _, line := range strings.Split(logOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		touched[unquoteGitPath(line)] = true
	}
	for rel, abs := range remaining {
		if touched[rel] {
			out[abs] = domain.ShipUnpushed
			continue
		}
		out[abs] = domain.ShipShipped
	}
	return out, nil
}

// renamedPath returns the destination half of a porcelain status line's
// path field: "old -> new" for a rename or copy, or rest unchanged
// otherwise.
func renamedPath(rest string) string {
	if idx := strings.Index(rest, " -> "); idx >= 0 {
		return rest[idx+4:]
	}
	return rest
}

// unquoteGitPath undoes git's C-style quoting of a path containing
// characters it considers unusual (applied by both `status` and `log`):
// wrapped in double quotes with backslash escapes. Anything that is not a
// quoted string -- the common case -- or fails to parse as one is returned
// unchanged.
func unquoteGitPath(p string) string {
	if len(p) < 2 || p[0] != '"' || p[len(p)-1] != '"' {
		return p
	}
	if unquoted, err := strconv.Unquote(p); err == nil {
		return unquoted
	}
	return p
}

// CommitPaths stages and commits exactly paths (absolute, inside repo) --
// `add -- <paths>` then `commit -m message -- <paths>`, so only these paths
// are committed even when the index already holds other staged changes --
// and returns the new HEAD sha. Never pushes (PLAN §7b: opt-in, never
// pushing). Errors are classified through the same gitError as every other
// git invocation in this package.
func (m *Manager) CommitPaths(ctx context.Context, repo string, paths []string, message string) (string, error) {
	if len(paths) == 0 {
		return "", errs.New(errs.Invalid, "CommitPaths: no paths given")
	}
	addArgs := append([]string{"add", "--"}, paths...)
	if _, err := m.run(ctx, repo, addArgs...); err != nil {
		return "", err
	}
	commitArgs := append([]string{"commit", "-m", message, "--"}, paths...)
	if _, err := m.run(ctx, repo, commitArgs...); err != nil {
		return "", err
	}
	sha, err := m.run(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}
