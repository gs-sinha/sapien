package remote

import (
	"context"
	"net/http"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

type addServiceRequest struct {
	Name   string        `json:"name"`
	Source domain.Source `json:"source"`
}

// List maps to GET /v1/services.
func (s *serviceAPI) List(ctx context.Context) ([]domain.Service, error) {
	var out []domain.Service
	if err := s.r().do(ctx, http.MethodGet, "/v1/services", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Get maps to GET /v1/services/{id}.
func (s *serviceAPI) Get(ctx context.Context, name string) (*domain.Service, error) {
	var out domain.Service
	if err := s.r().do(ctx, http.MethodGet, "/v1/services/"+name, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Add maps to POST /v1/services.
func (s *serviceAPI) Add(ctx context.Context, name string, src domain.Source) (*domain.Service, error) {
	var out domain.Service
	body := addServiceRequest{Name: name, Source: src}
	if err := s.r().do(ctx, http.MethodPost, "/v1/services", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Remove maps to DELETE /v1/services/{id}.
func (s *serviceAPI) Remove(ctx context.Context, name string) error {
	return s.r().do(ctx, http.MethodDelete, "/v1/services/"+name, nil, nil, nil)
}

// Sync maps to POST /v1/services/sync (name == "") or
// POST /v1/services/{id}/sync (name != "").
func (s *serviceAPI) Sync(ctx context.Context, name string) ([]domain.Service, error) {
	path := "/v1/services/sync"
	if name != "" {
		path = "/v1/services/" + name + "/sync"
	}
	var out []domain.Service
	if err := s.r().do(ctx, http.MethodPost, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Reindex maps to POST /v1/services/reindex.
func (s *serviceAPI) Reindex(ctx context.Context) error {
	return s.r().do(ctx, http.MethodPost, "/v1/services/reindex", nil, nil, nil)
}

var _ engine.ServiceAPI = (*serviceAPI)(nil)
