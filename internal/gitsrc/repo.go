package gitsrc

import (
	"context"
	"strconv"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// RepoStatus reports the workspace's own git repository (PLAN §7b): the
// team's checkout whose flows/, memories/ and examples/ are the workspace
// tier. Every query here is read-only against refs already on disk -- this
// is what the daemon's git tick and Status/Sync call, and it must never
// reach the network; FetchRepo below is the only thing in this file that
// does.
//
// dir need not be the repository root; every query runs against the
// resolved Toplevel. A dir outside any git repository is not a failure --
// the workspace need not be a checkout at all -- so InGit simply comes back
// false with every other field zero and a nil error.
func (m *Manager) RepoStatus(ctx context.Context, dir string) (*domain.RepoStatus, error) {
	root, err := m.Toplevel(ctx, dir)
	if err != nil {
		return &domain.RepoStatus{}, nil
	}

	status := &domain.RepoStatus{InGit: true, Root: root}

	branch, err := m.run(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, err
	}
	status.Branch = strings.TrimSpace(branch)

	if remote, err := m.run(ctx, root, "remote", "get-url", "origin"); err == nil {
		status.Remote = strings.TrimSpace(remote)
	} else if !strings.Contains(strings.ToLower(stderrOf(err)), "no such remote") {
		return nil, err
	}

	// @{upstream} fails whenever the branch tracks nothing -- a fresh
	// branch, a detached HEAD, a remote with no matching branch yet. That
	// is a normal, common state, not a failure worth surfacing: Upstream
	// and Behind/Ahead simply stay at their zero values.
	if upstream, err := m.run(ctx, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		status.Upstream = strings.TrimSpace(upstream)

		counts, err := m.run(ctx, root, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
		if err != nil {
			return nil, err
		}
		status.Behind, status.Ahead = parseBehindAhead(counts)
	}

	// Every untracked file counts on its own (-uall), not once per new
	// directory: this is the number the status bar shows beside a link to
	// the Changes page, which lists files, and the two must agree.
	porcelain, err := m.run(ctx, root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	status.Dirty = len(nonEmptyLines(porcelain))

	if t, ok := LastFetch(root); ok {
		status.FetchedAt = t
	}

	return status, nil
}

// FetchRepo runs `git fetch --prune origin` against dir's repository: the
// one network call in this file, and the only one the daemon's tick (PLAN
// §7b) is allowed to make against the workspace's own repository -- it
// never touches the working tree, so a developer's uncommitted work is
// never at risk from it.
func (m *Manager) FetchRepo(ctx context.Context, dir string) error {
	root, err := m.Toplevel(ctx, dir)
	if err != nil {
		return err
	}
	_, err = m.run(ctx, root, "fetch", "--prune", "origin")
	return err
}

// PullFastForward fast-forwards dir's checkout onto its upstream and
// reports how many commits arrived. Every precondition -- inside a
// repository, an upstream configured, a clean tree -- is the caller's to
// check first (engine.RepoAPI.Pull does, PLAN §7b); this only ever refuses
// on an actual divergence, which `merge --ff-only` detects by aborting
// cleanly without touching the working tree or HEAD.
func (m *Manager) PullFastForward(ctx context.Context, dir string) (int, error) {
	root, err := m.Toplevel(ctx, dir)
	if err != nil {
		return 0, err
	}

	before, err := m.run(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return 0, err
	}
	beforeSHA := strings.TrimSpace(before)

	if _, err := m.run(ctx, root, "merge", "--ff-only", "@{upstream}"); err != nil {
		if isDiverged(stderrOf(err)) {
			return 0, errs.New(errs.Conflict, "branch has diverged from its upstream").
				WithDetail("stderr", stderrOf(err)).
				WithHint("reconcile by hand (merge or rebase in your own checkout), then pull again")
		}
		return 0, err
	}

	countOut, err := m.run(ctx, root, "rev-list", "--count", beforeSHA+"..HEAD")
	if err != nil {
		return 0, err
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(countOut))
	if convErr != nil {
		return 0, nil
	}
	return n, nil
}

// PushRepo sends dir's checkout's unpushed commits to its upstream (PLAN
// §7b): `git push --set-upstream origin <branch>` when the branch has no
// upstream configured yet, else a plain `git push` (which -- push.default's
// built-in "simple" -- pushes to the already-tracked upstream on its own).
// It returns how many commits went up, read from RepoStatus.Ahead before
// the push runs: a successful push always brings Ahead back to zero and
// nothing else on this branch can change it in the brief window in
// between. Never forces (no --force, ever); a push into a branch with no
// upstream yet has no "ahead" to report (Ahead is only ever computed
// relative to an upstream), so the returned count is 0 for that first
// push, same as RepoStatus would show beforehand. Errors are classified
// through gitError like every other invocation in this package, so an
// authentication failure carries the same hint FetchRepo's would.
func (m *Manager) PushRepo(ctx context.Context, dir string) (int, error) {
	root, err := m.Toplevel(ctx, dir)
	if err != nil {
		return 0, err
	}

	status, err := m.RepoStatus(ctx, root)
	if err != nil {
		return 0, err
	}
	pushed := status.Ahead

	if status.Upstream == "" {
		if _, err := m.run(ctx, root, "push", "--set-upstream", "origin", status.Branch); err != nil {
			return 0, err
		}
		return pushed, nil
	}
	if _, err := m.run(ctx, root, "push"); err != nil {
		return 0, err
	}
	return pushed, nil
}

// isDiverged reports whether stderrOutput is `merge --ff-only`'s way of
// saying the two branches cannot be fast-forwarded into each other.
func isDiverged(stderrOutput string) bool {
	return strings.Contains(strings.ToLower(stderrOutput), "not possible to fast-forward")
}

// parseBehindAhead parses `git rev-list --left-right --count
// @{upstream}...HEAD`'s "<behind>\t<ahead>" output (upstream is the left
// side of "...", HEAD the right, so a commit only upstream has is "behind"
// and a commit only HEAD has is "ahead"), returning zero for both on
// anything unrecognized rather than erroring: this is display information,
// not a condition of the query.
func parseBehindAhead(s string) (behind, ahead int) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) != 2 {
		return 0, 0
	}
	b, errB := strconv.Atoi(fields[0])
	a, errA := strconv.Atoi(fields[1])
	if errA != nil || errB != nil {
		return 0, 0
	}
	return b, a
}
