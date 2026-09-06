package remote

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// exampleAPI maps engine.ExampleAPI onto the daemon's /v1/examples routes
// (PLAN §34b):
//
//	GET    /v1/examples?operation=&service=&tag=&text=&limit=   List
//	GET    /v1/examples/for-operations?op=a&op=b&limit=         ForOperations
//	GET    /v1/examples/{id}                                    Get
//	POST   /v1/examples                                         Create
//	PUT    /v1/examples/{id}                                    Update
//	DELETE /v1/examples/{id}                                    Delete
//	POST   /v1/examples/from-run                                FromRun
//	POST   /v1/examples/reindex                                 Reindex
type exampleAPI Remote

func (e *exampleAPI) r() *Remote { return (*Remote)(e) }

func (e *exampleAPI) List(ctx context.Context, q domain.ExampleQuery) ([]domain.SavedExample, error) {
	query := url.Values{}
	if q.Operation != "" {
		query.Set("operation", q.Operation)
	}
	if q.Service != "" {
		query.Set("service", q.Service)
	}
	if q.Tag != "" {
		query.Set("tag", q.Tag)
	}
	if q.Text != "" {
		query.Set("text", q.Text)
	}
	if q.Limit > 0 {
		query.Set("limit", strconv.Itoa(q.Limit))
	}
	var out []domain.SavedExample
	if err := e.r().do(ctx, http.MethodGet, "/v1/examples", query, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (e *exampleAPI) Get(ctx context.Context, id string) (*domain.SavedExample, error) {
	var out domain.SavedExample
	if err := e.r().do(ctx, http.MethodGet, "/v1/examples/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (e *exampleAPI) Create(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	var out domain.SavedExample
	if err := e.r().do(ctx, http.MethodPost, "/v1/examples", nil, ex, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (e *exampleAPI) Update(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	var out domain.SavedExample
	if err := e.r().do(ctx, http.MethodPut, "/v1/examples/"+url.PathEscape(ex.ID), nil, ex, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (e *exampleAPI) Delete(ctx context.Context, id string) error {
	return e.r().do(ctx, http.MethodDelete, "/v1/examples/"+url.PathEscape(id), nil, nil, nil)
}

func (e *exampleAPI) FromRun(ctx context.Context, req engine.ExampleFromRun) (*domain.SavedExample, error) {
	var out domain.SavedExample
	if err := e.r().do(ctx, http.MethodPost, "/v1/examples/from-run", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (e *exampleAPI) ForOperations(ctx context.Context, operationIDs []string, limit int) ([]domain.SavedExample, error) {
	query := url.Values{}
	for _, id := range operationIDs {
		query.Add("op", id)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var out []domain.SavedExample
	if err := e.r().do(ctx, http.MethodGet, "/v1/examples/for-operations", query, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (e *exampleAPI) Reindex(ctx context.Context) error {
	return e.r().do(ctx, http.MethodPost, "/v1/examples/reindex", nil, nil, nil)
}
