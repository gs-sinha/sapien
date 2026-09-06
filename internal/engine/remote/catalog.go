package remote

import (
	"context"
	"net/http"
	"net/url"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// GetOperation maps to GET /v1/operations/{id}.
func (c *catalogAPI) GetOperation(ctx context.Context, id string) (*domain.Operation, error) {
	var out domain.Operation
	if err := c.r().do(ctx, http.MethodGet, "/v1/operations/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResolveOperation maps to GET /v1/operations/resolve?ref=.
func (c *catalogAPI) ResolveOperation(ctx context.Context, ref string) (*domain.Operation, error) {
	q := url.Values{"ref": {ref}}
	var out domain.Operation
	if err := c.r().do(ctx, http.MethodGet, "/v1/operations/resolve", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListOperations maps to GET /v1/operations?service= (no "q", which would
// route to SearchAPI.Operations instead -- see the package doc).
func (c *catalogAPI) ListOperations(ctx context.Context, service string) ([]domain.Operation, error) {
	q := url.Values{}
	if service != "" {
		q.Set("service", service)
	}
	var out []domain.Operation
	if err := c.r().do(ctx, http.MethodGet, "/v1/operations", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Fields maps to GET /v1/operations/{id}/fields.
func (c *catalogAPI) Fields(ctx context.Context, operationID string) ([]domain.Field, error) {
	var out []domain.Field
	if err := c.r().do(ctx, http.MethodGet, "/v1/operations/"+operationID+"/fields", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSchema maps to GET /v1/schemas/{service}/{name}.
func (c *catalogAPI) GetSchema(ctx context.Context, service, name string) (*domain.NamedSchema, error) {
	var out domain.NamedSchema
	if err := c.r().do(ctx, http.MethodGet, "/v1/schemas/"+service+"/"+name, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDocs maps to GET /v1/docs?service= (no "q", which would route to
// SearchAPI.Docs instead -- see the package doc).
func (c *catalogAPI) ListDocs(ctx context.Context, service string) ([]domain.Doc, error) {
	q := url.Values{}
	if service != "" {
		q.Set("service", service)
	}
	var out []domain.Doc
	if err := c.r().do(ctx, http.MethodGet, "/v1/docs", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetDoc maps to GET /v1/docs/{service}/*, where path becomes the trailing
// wildcard segment verbatim (including any "/" or "#" it contains).
func (c *catalogAPI) GetDoc(ctx context.Context, service, path string) (*domain.Doc, error) {
	var out domain.Doc
	if err := c.r().do(ctx, http.MethodGet, "/v1/docs/"+service+"/"+path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

var _ engine.CatalogAPI = (*catalogAPI)(nil)
