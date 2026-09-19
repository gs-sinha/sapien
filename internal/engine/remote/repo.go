package remote

import (
	"context"
	"net/http"
	"net/url"

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

// Changes maps to GET /v1/workspace/repo/changes.
func (r *repoAPI) Changes(ctx context.Context) (*domain.RepoChanges, error) {
	var out domain.RepoChanges
	if err := r.r().do(ctx, http.MethodGet, "/v1/workspace/repo/changes", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Diff maps to GET /v1/workspace/repo/diff?path=.
func (r *repoAPI) Diff(ctx context.Context, path string) (*domain.RepoDiff, error) {
	var out domain.RepoDiff
	q := url.Values{"path": {path}}
	if err := r.r().do(ctx, http.MethodGet, "/v1/workspace/repo/diff", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// commitRepoRequest is POST /v1/workspace/repo/commit's body.
type commitRepoRequest struct {
	Paths   []string `json:"paths"`
	Message string   `json:"message"`
}

// Commit maps to POST /v1/workspace/repo/commit.
func (r *repoAPI) Commit(ctx context.Context, paths []string, message string) (*domain.RepoCommitResult, error) {
	var out domain.RepoCommitResult
	body := commitRepoRequest{Paths: paths, Message: message}
	if err := r.r().do(ctx, http.MethodPost, "/v1/workspace/repo/commit", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
