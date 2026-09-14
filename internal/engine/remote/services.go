package remote

import (
	"context"
	"net/http"
	"net/url"

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

type bindServiceRequest struct {
	Path  string `json:"path"`
	Force bool   `json:"force,omitempty"`
}

// Bind maps to PUT /v1/services/{id}/binding.
func (s *serviceAPI) Bind(ctx context.Context, name, path string) (*domain.Service, error) {
	var out domain.Service
	if err := s.r().do(ctx, http.MethodPut, "/v1/services/"+name+"/binding", nil, bindServiceRequest{Path: path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Unbind maps to DELETE /v1/services/{id}/binding.
func (s *serviceAPI) Unbind(ctx context.Context, name string) (*domain.Service, error) {
	var out domain.Service
	if err := s.r().do(ctx, http.MethodDelete, "/v1/services/"+name+"/binding", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Binding maps to GET /v1/services/{id}/binding.
func (s *serviceAPI) Binding(ctx context.Context, name string) (*engine.BindingInfo, error) {
	var out engine.BindingInfo
	if err := s.r().do(ctx, http.MethodGet, "/v1/services/"+name+"/binding", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BindWith maps to PUT /v1/services/{id}/binding with force; an empty name
// goes to PUT /v1/services/binding, which infers the service from the
// checkout's origin.
func (s *serviceAPI) BindWith(ctx context.Context, name, path string, opts engine.BindOptions) (*domain.Service, error) {
	var out domain.Service
	p := "/v1/services/binding"
	if name != "" {
		p = "/v1/services/" + name + "/binding"
	}
	if err := s.r().do(ctx, http.MethodPut, p, nil, bindServiceRequest{Path: path, Force: opts.Force}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BrowseCheckouts maps to GET /v1/services/{id}/checkouts?path=.
func (s *serviceAPI) BrowseCheckouts(ctx context.Context, name, dir string) (*engine.DirListing, error) {
	var out engine.DirListing
	q := url.Values{}
	if dir != "" {
		q.Set("path", dir)
	}
	if err := s.r().do(ctx, http.MethodGet, "/v1/services/"+name+"/checkouts", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type addFromCheckoutRequest struct {
	Name  string `json:"name,omitempty"`
	Path  string `json:"path"`
	Ref   string `json:"ref,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// AddFromCheckout maps to POST /v1/services/from-checkout.
func (s *serviceAPI) AddFromCheckout(ctx context.Context, name, path string, opts engine.AddFromCheckoutOptions) (*domain.Service, error) {
	var out domain.Service
	body := addFromCheckoutRequest{Name: name, Path: path, Ref: opts.Ref, Force: opts.Force}
	if err := s.r().do(ctx, http.MethodPost, "/v1/services/from-checkout", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
