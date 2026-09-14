package local

import (
	"context"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// repoAPI implements engine.RepoAPI over a Local. Phase 0 stubs; the
// workspace-repo work in this change fills them in.
type repoAPI struct{ l *Local }

var _ engine.RepoAPI = (*repoAPI)(nil)

// Repo returns the workspace repository API.
func (l *Local) Repo() engine.RepoAPI { return &repoAPI{l: l} }

func (r *repoAPI) Status(ctx context.Context) (*domain.RepoStatus, error) {
	return nil, errs.New(errs.NotImplemented, "workspace repository status is not available yet")
}

func (r *repoAPI) Fetch(ctx context.Context) (*domain.RepoStatus, error) {
	return nil, errs.New(errs.NotImplemented, "workspace repository fetch is not available yet")
}

func (r *repoAPI) Pull(ctx context.Context) (*domain.RepoStatus, error) {
	return nil, errs.New(errs.NotImplemented, "workspace repository pull is not available yet")
}

func (r *repoAPI) Sync(ctx context.Context) (*domain.RepoStatus, error) {
	return nil, errs.New(errs.NotImplemented, "workspace repository sync is not available yet")
}
