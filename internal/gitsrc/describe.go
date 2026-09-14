package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// Describe is DescribeAgainst with no team ref to compare against: it
// reports everything about dir except Ahead/Behind, which need one.
func (m *Manager) Describe(ctx context.Context, dir string) (*domain.LocalCheckout, error) {
	return m.DescribeAgainst(ctx, dir, "")
}

// DescribeAgainst reports what git says about the working tree at dir,
// using only read-only queries: Sapien never fetches, checks out or
// commits in a developer's repository (PLAN §7b), so this is the whole of
// what it may learn about one. A dir that is not inside a git repository
// is not an error -- a local source need not be a checkout at all -- and
// comes back with only Path set; a git failure of any other kind is
// returned.
//
// Beyond Describe's original fields (branch, commit, origin, dirty
// count), this also reports:
//
//   - CommittedAt: HEAD's commit time, which is what separates a checkout
//     someone actually develops in from an old backup clone of the same
//     repository -- two clones can share a commit and a branch name and
//     still disagree about which one to trust.
//   - Worktree: true when dir is a `git worktree` checkout rather than a
//     full clone (its ".git" is a file pointing at the real repository,
//     not a directory) -- still describable, but not the repository's
//     only checkout, which matters when deciding whether binding here
//     would surprise whoever uses the other one.
//   - Ahead/Behind, when ref is not "": how HEAD compares to
//     origin/<ref> as this clone last fetched it. Both are left zero when
//     origin/<ref> is unknown here (never fetched, or the ref does not
//     exist on this remote) -- Sapien never fetches to find out.
//
// Package is deliberately not filled in here: gitsrc knows nothing about
// API packages; the caller (registry.DiscoverPackage) fills it in.
//
// Every query goes through run so the Manager's timeout and
// GIT_TERMINAL_PROMPT=0 apply: a hung or prompting git here would stall a
// catalog build.
func (m *Manager) DescribeAgainst(ctx context.Context, dir, ref string) (*domain.LocalCheckout, error) {
	co := &domain.LocalCheckout{Path: dir}

	inside, err := m.run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if strings.Contains(strings.ToLower(stderrOf(err)), "not a git repository") {
			return co, nil
		}
		return nil, err
	}
	if strings.TrimSpace(inside) != "true" {
		return co, nil
	}

	// --verify --quiet exits 1 with nothing on stderr when HEAD has no
	// commit yet (a repository that was only ever `git init`ed); that is a
	// checkout with nothing to report, not a failure. Anything git
	// complains about is.
	commit, err := m.run(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
	switch {
	case err == nil:
		co.Commit = strings.TrimSpace(commit)
		branch, err := m.run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return nil, err
		}
		co.Branch = strings.TrimSpace(branch) // "HEAD" when detached

		if stamp, err := m.run(ctx, dir, "log", "-1", "--format=%cI"); err == nil {
			if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(stamp)); perr == nil {
				co.CommittedAt = t
			}
		}
	case stderrOf(err) != "":
		return nil, err
	}

	remote, err := m.run(ctx, dir, "remote", "get-url", "origin")
	switch {
	case err == nil:
		co.Remote = strings.TrimSpace(remote)
	case strings.Contains(strings.ToLower(stderrOf(err)), "no such remote"):
		// No origin: a repository that was never cloned from anywhere.
	default:
		return nil, err
	}

	// Scoped to dir rather than the whole repository: the question is how
	// much unshared work sits under the package this service reads.
	status, err := m.run(ctx, dir, "status", "--porcelain", "--", ".")
	if err != nil {
		return nil, err
	}
	co.Dirty = len(nonEmptyLines(status))

	if top, err := m.run(ctx, dir, "rev-parse", "--show-toplevel"); err == nil {
		if info, statErr := os.Stat(filepath.Join(strings.TrimSpace(top), ".git")); statErr == nil {
			co.Worktree = !info.IsDir()
		}
	}

	if ref != "" && co.Commit != "" {
		counts, err := m.run(ctx, dir, "rev-list", "--left-right", "--count", "HEAD..."+"origin/"+ref)
		switch {
		case err == nil:
			co.Ahead, co.Behind = parseLeftRightCounts(counts)
		case isUnknownRevision(stderrOf(err)):
			// origin/<ref> is not known in this clone (never fetched, or the
			// ref doesn't exist on this remote); never fetch to find out.
		default:
			return nil, err
		}
	}

	return co, nil
}

// Toplevel returns the absolute root directory of the git repository
// containing dir (git rev-parse --show-toplevel), for callers that need to
// tell a repository's root from a subdirectory of one -- a monorepo
// package, PLAN §7b's add-from-checkout.
func (m *Manager) Toplevel(ctx context.Context, dir string) (string, error) {
	top, err := m.run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(top)), nil
}

// isUnknownRevision reports whether stderrOutput is git's way of saying a
// ref does not resolve here, as opposed to any other failure.
func isUnknownRevision(stderrOutput string) bool {
	s := strings.ToLower(stderrOutput)
	return strings.Contains(s, "unknown revision") || strings.Contains(s, "bad revision")
}

// parseLeftRightCounts parses `git rev-list --left-right --count`'s
// "<ahead>\t<behind>" output, returning zero for both on anything it does
// not recognize rather than erroring: this is display information, not a
// condition of the build.
func parseLeftRightCounts(s string) (ahead, behind int) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) != 2 {
		return 0, 0
	}
	a, errA := strconv.Atoi(fields[0])
	b, errB := strconv.Atoi(fields[1])
	if errA != nil || errB != nil {
		return 0, 0
	}
	return a, b
}

// NormalizeRemote reduces a git remote URL to the form used to decide
// whether two of them name the same repository: lower-cased, without
// scheme, credentials, port, trailing slash or ".git", with the scp-like
// "host:path" written as "host/path". So "git@github.com:Org/Repo.git",
// "https://github.com/org/repo" and "ssh://git@github.com/org/repo.git"
// all become "github.com/org/repo". It exists because a checkout's origin
// is whatever the developer typed when cloning, and the workspace's URL is
// whatever the team committed; the two agree on the repository far more
// often than on the spelling.
func NormalizeRemote(url string) string {
	s := strings.ToLower(strings.TrimSpace(url))
	if s == "" {
		return ""
	}

	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		host, rest, _ := strings.Cut(s, "/")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if colon := strings.Index(host, ":"); colon >= 0 {
			host = host[:colon]
		}
		s = host + "/" + rest
	} else if colon := strings.Index(s, ":"); colon >= 0 && !strings.Contains(s[:colon], "/") {
		// scp-like: "[user@]host:path".
		host := s[:colon]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		s = host + "/" + s[colon+1:]
	}

	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimSuffix(s, "/")
}

// stderrOf returns the stderr run attached to a failed git invocation, or
// "" for any other error.
func stderrOf(err error) string {
	e := errs.As(err)
	if e == nil {
		return ""
	}
	s, _ := e.Details["stderr"].(string)
	return s
}

// nonEmptyLines splits s into lines, dropping blank ones.
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
