package enginetest

import (
	"context"
	"strings"

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

// Changes returns the seeded status alongside whatever files a test seeded
// via SetRepoChanges (empty by default).
func (r *repoAPI) Changes(ctx context.Context) (*domain.RepoChanges, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Repo.Changes", nil)
	return &domain.RepoChanges{Status: f.repo, Files: append([]domain.RepoFileChange(nil), f.repoChanges...)}, nil
}

// SetRepoChanges seeds what Repo().Changes reports in Files.
func (f *Fake) SetRepoChanges(files []domain.RepoFileChange) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repoChanges = files
}

// Diff returns an empty diff for path; a test that needs specific diff
// content has no seam yet since nothing currently exercises it against the
// fake.
func (r *repoAPI) Diff(ctx context.Context, path string) (*domain.RepoDiff, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Repo.Diff", path)
	return &domain.RepoDiff{Path: path}, nil
}

// Commit validates message/paths the way the real engine does and echoes
// paths back as committed; the fake has no filesystem to actually commit
// anything to.
func (r *repoAPI) Commit(ctx context.Context, paths []string, message string) (*domain.RepoCommitResult, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Repo.Commit", map[string]any{"paths": paths, "message": message})

	if len(paths) == 0 {
		return nil, errs.New(errs.Invalid, "no paths given")
	}
	if strings.TrimSpace(message) == "" {
		return nil, errs.New(errs.Invalid, "message is required")
	}
	return &domain.RepoCommitResult{Commit: "fake-sha", Committed: paths, Status: f.repo}, nil
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
