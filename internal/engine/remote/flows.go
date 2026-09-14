package remote

import (
	"context"
	"net/http"
	"net/url"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

type flowYAMLRequest struct {
	YAML string `json:"yaml"`
	Path string `json:"path,omitempty"`
}

// List maps to GET /v1/flows?q=.
func (fl *flowAPI) List(ctx context.Context, query string) ([]domain.FlowSummary, error) {
	q := url.Values{}
	if query != "" {
		q.Set("q", query)
	}
	var out []domain.FlowSummary
	if err := fl.r().do(ctx, http.MethodGet, "/v1/flows", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Get maps to GET /v1/flows/{id}.
func (fl *flowAPI) Get(ctx context.Context, id string) (*domain.Flow, error) {
	var out domain.Flow
	if err := fl.r().do(ctx, http.MethodGet, "/v1/flows/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Parse maps to POST /v1/flows/parse.
func (fl *flowAPI) Parse(ctx context.Context, yamlSrc string) (*domain.Flow, error) {
	var out domain.Flow
	if err := fl.r().do(ctx, http.MethodPost, "/v1/flows/parse", nil, flowYAMLRequest{YAML: yamlSrc}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Validate maps to POST /v1/flows/validate.
func (fl *flowAPI) Validate(ctx context.Context, yamlSrc string) (*domain.ValidationResult, error) {
	var out domain.ValidationResult
	if err := fl.r().do(ctx, http.MethodPost, "/v1/flows/validate", nil, flowYAMLRequest{YAML: yamlSrc}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create maps to POST /v1/flows.
func (fl *flowAPI) Create(ctx context.Context, yamlSrc string, path string) (*domain.Flow, error) {
	var out domain.Flow
	body := flowYAMLRequest{YAML: yamlSrc, Path: path}
	if err := fl.r().do(ctx, http.MethodPost, "/v1/flows", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update maps to PUT /v1/flows/{id}.
func (fl *flowAPI) Update(ctx context.Context, id string, yamlSrc string) (*domain.Flow, error) {
	var out domain.Flow
	if err := fl.r().do(ctx, http.MethodPut, "/v1/flows/"+id, nil, flowYAMLRequest{YAML: yamlSrc}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete maps to DELETE /v1/flows/{id}.
func (fl *flowAPI) Delete(ctx context.Context, id string) error {
	return fl.r().do(ctx, http.MethodDelete, "/v1/flows/"+id, nil, nil, nil)
}

// Reference maps to GET /v1/flows/reference?topic=.
func (fl *flowAPI) Reference(ctx context.Context, topic string) (string, error) {
	q := url.Values{"topic": {topic}}
	var out string
	if err := fl.r().do(ctx, http.MethodGet, "/v1/flows/reference", q, nil, &out); err != nil {
		return "", err
	}
	return out, nil
}

var _ engine.FlowAPI = (*flowAPI)(nil)

// createFlowRequest is POST /v1/flows' body: the YAML plus where it goes.
type createFlowRequest struct {
	YAML      string `json:"yaml"`
	Path      string `json:"path,omitempty"`
	OwnerKind string `json:"owner_kind,omitempty"`
	OwnerID   string `json:"owner_id,omitempty"`
}

// CreateIn maps to POST /v1/flows with owner_kind/owner_id.
func (fl *flowAPI) CreateIn(ctx context.Context, yamlSrc string, opts engine.CreateFlowOptions) (*domain.Flow, error) {
	var out domain.Flow
	body := createFlowRequest{YAML: yamlSrc, Path: opts.Path, OwnerKind: opts.OwnerKind, OwnerID: opts.OwnerID}
	if err := fl.r().do(ctx, http.MethodPost, "/v1/flows", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type rescopeFlowRequest struct {
	OwnerKind string `json:"owner_kind"`
	OwnerID   string `json:"owner_id,omitempty"`
}

// Rescope maps to POST /v1/flows/{id}/rescope.
func (fl *flowAPI) Rescope(ctx context.Context, id string, ownerKind, ownerID string) (*domain.Flow, error) {
	var out domain.Flow
	body := rescopeFlowRequest{OwnerKind: ownerKind, OwnerID: ownerID}
	if err := fl.r().do(ctx, http.MethodPost, "/v1/flows/"+id+"/rescope", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
