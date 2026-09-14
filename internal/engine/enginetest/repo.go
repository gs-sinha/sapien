package enginetest

import (
	"context"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// repoAPI is the Fake's view of engine.RepoAPI: an in-memory RepoStatus a
// test seeds through SetRepoStatus. Fetch leaves it as is; Pull and Sync
// zero Behind when the tree is clean and mark Pulled, so a caller sees the
// shape a real pull produces.
type repoAPI Fake

func (r *repoAPI) f() *Fake { return (*Fake)(r) }

// Repo returns the fake repository API.
func (f *Fake) Repo() engine.RepoAPI { return (*repoAPI)(f) }

// SetRepoStatus seeds what Repo().Status reports.
func (f *Fake) SetRepoStatus(s domain.RepoStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repo = s
}

func (r *repoAPI) Status(ctx context.Context) (*domain.RepoStatus, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Repo.Status", nil)
	cp := f.repo
	return &cp, nil
}

func (r *repoAPI) Fetch(ctx context.Context) (*domain.RepoStatus, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Repo.Fetch", nil)
	cp := f.repo
	return &cp, nil
}

func (r *repoAPI) Pull(ctx context.Context) (*domain.RepoStatus, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Repo.Pull", nil)
	return r.pullLocked(true)
}

func (r *repoAPI) Sync(ctx context.Context) (*domain.RepoStatus, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Repo.Sync", nil)
	return r.pullLocked(false)
}

// pullLocked applies a pull to the seeded status. strict reports a dirty
// tree or missing upstream as an error (Pull); otherwise it records the
// reason in Skipped (Sync).
func (r *repoAPI) pullLocked(strict bool) (*domain.RepoStatus, error) {
	f := r.f()
	s := f.repo
	switch {
	case !s.InGit:
		if strict {
			return nil, errs.New(errs.Invalid, "workspace is not inside a git repository")
		}
		s.Skipped = "not a git repository"
	case s.Dirty > 0:
		if strict {
			return nil, errs.New(errs.Conflict, "workspace has uncommitted changes; commit or stash them before pulling")
		}
		s.Skipped = "uncommitted changes"
	case s.Upstream == "":
		if strict {
			return nil, errs.New(errs.Conflict, "branch has no upstream")
		}
		s.Skipped = "no upstream"
	case s.Behind > 0:
		s.Pulled, s.PulledCount, s.Behind = true, s.Behind, 0
	}
	f.repo = s
	cp := s
	return &cp, nil
}

// Push moves the seeded status's Ahead into PushedCount; refused when
// behind, as the real thing is.
func (r *repoAPI) Push(ctx context.Context) (*domain.RepoStatus, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Repo.Push", nil)
	s := f.repo
	switch {
	case !s.InGit:
		return nil, errs.New(errs.Invalid, "workspace is not inside a git repository")
	case s.Behind > 0:
		return nil, errs.New(errs.Conflict, "branch is behind its upstream by %d commits; pull first", s.Behind)
	case s.Ahead > 0:
		s.Pushed, s.PushedCount, s.Ahead = true, s.Ahead, 0
	}
	f.repo = s
	cp := s
	return &cp, nil
}
