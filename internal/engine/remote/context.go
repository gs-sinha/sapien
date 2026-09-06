package remote

import (
	"context"
	"net/http"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// Build maps to POST /v1/context.
func (c *contextAPI) Build(ctx context.Context, req domain.ContextRequest) (*domain.ContextBundle, error) {
	var out domain.ContextBundle
	if err := c.r().do(ctx, http.MethodPost, "/v1/context", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

var _ engine.ContextAPI = (*contextAPI)(nil)
