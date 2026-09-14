package remote

import (
	"context"
	"net/http"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// repoAPI is the remote view of engine.RepoAPI.
type repoAPI Remote

func (r *repoAPI) r() *Remote { return (*Remote)(r) }

// Repo returns the workspace repository API.
func (r *Remote) Repo() engine.RepoAPI { return (*repoAPI)(r) }

var _ engine.RepoAPI = (*repoAPI)(nil)

// Status maps to GET /v1/workspace/repo.
func (r *repoAPI) Status(ctx context.Context) (*domain.RepoStatus, error) {
	var out domain.RepoStatus
	if err := r.r().do(ctx, http.MethodGet, "/v1/workspace/repo", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Fetch maps to POST /v1/workspace/repo/fetch.
func (r *repoAPI) Fetch(ctx context.Context) (*domain.RepoStatus, error) {
	var out domain.RepoStatus
	if err := r.r().do(ctx, http.MethodPost, "/v1/workspace/repo/fetch", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Pull maps to POST /v1/workspace/repo/pull.
func (r *repoAPI) Pull(ctx context.Context) (*domain.RepoStatus, error) {
	var out domain.RepoStatus
	if err := r.r().do(ctx, http.MethodPost, "/v1/workspace/repo/pull", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Sync maps to POST /v1/workspace/repo/sync.
func (r *repoAPI) Sync(ctx context.Context) (*domain.RepoStatus, error) {
	var out domain.RepoStatus
	if err := r.r().do(ctx, http.MethodPost, "/v1/workspace/repo/sync", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Push maps to POST /v1/workspace/repo/push.
func (r *repoAPI) Push(ctx context.Context) (*domain.RepoStatus, error) {
	var out domain.RepoStatus
	if err := r.r().do(ctx, http.MethodPost, "/v1/workspace/repo/push", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
