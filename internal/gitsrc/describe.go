package gitsrc

import (
	"context"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// Describe reports what git says about the working tree at dir, using
// only read-only queries: Sapien never fetches, checks out or commits in
// a developer's repository (PLAN §7b), so this is the whole of what it
// may learn about one. A dir that is not inside a git repository is not
// an error -- a local source need not be a checkout at all -- and comes
// back with only Path set; a git failure of any other kind is returned.
//
// Every query goes through run so the Manager's timeout and
// GIT_TERMINAL_PROMPT=0 apply: a hung or prompting git here would stall a
// catalog build.
func (m *Manager) Describe(ctx context.Context, dir string) (*domain.LocalCheckout, error) {
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

	return co, nil
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
