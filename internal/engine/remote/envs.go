package remote

import (
	"context"
	"net/http"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// List maps to GET /v1/environments.
func (e *envAPI) List(ctx context.Context) ([]domain.Environment, error) {
	var out []domain.Environment
	if err := e.r().do(ctx, http.MethodGet, "/v1/environments", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Get maps to GET /v1/environments/{name}.
func (e *envAPI) Get(ctx context.Context, name string) (*domain.Environment, error) {
	var out domain.Environment
	if err := e.r().do(ctx, http.MethodGet, "/v1/environments/"+name, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type defaultEnvironmentRequest struct {
	Name string `json:"name"`
}

type defaultEnvironmentResponse struct {
	Name string `json:"name"`
}

// Default maps to GET /v1/environments/default.
func (e *envAPI) Default(ctx context.Context) (string, error) {
	var out defaultEnvironmentResponse
	if err := e.r().do(ctx, http.MethodGet, "/v1/environments/default", nil, nil, &out); err != nil {
		return "", err
	}
	return out.Name, nil
}

// SetDefault maps to PUT /v1/environments/default.
func (e *envAPI) SetDefault(ctx context.Context, name string) error {
	return e.r().do(ctx, http.MethodPut, "/v1/environments/default", nil, defaultEnvironmentRequest{Name: name}, nil)
}

type setSecretRequest struct {
	Value string `json:"value"`
}

// SetSecret maps to PUT /v1/secrets/{name}.
func (e *envAPI) SetSecret(ctx context.Context, name, value string) error {
	return e.r().do(ctx, http.MethodPut, "/v1/secrets/"+name, nil, setSecretRequest{Value: value}, nil)
}

// ListSecrets maps to GET /v1/secrets. Values are never returned.
func (e *envAPI) ListSecrets(ctx context.Context) ([]string, error) {
	var out []string
	if err := e.r().do(ctx, http.MethodGet, "/v1/secrets", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteSecret maps to DELETE /v1/secrets/{name}.
func (e *envAPI) DeleteSecret(ctx context.Context, name string) error {
	return e.r().do(ctx, http.MethodDelete, "/v1/secrets/"+name, nil, nil, nil)
}

var _ engine.EnvAPI = (*envAPI)(nil)
