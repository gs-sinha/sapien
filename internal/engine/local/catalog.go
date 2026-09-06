package local

import (
	"context"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// catalogAPI implements engine.CatalogAPI as a direct pass-through to
// internal/catalog; there is no business logic on this side of the seam.
type catalogAPI struct{ l *Local }

var _ engine.CatalogAPI = (*catalogAPI)(nil)

func (c *catalogAPI) GetOperation(ctx context.Context, id string) (*domain.Operation, error) {
	op, err := c.l.cat.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	// Usage feedback (search ranking tuning task, part 2): an agent or the
	// CLI inspecting an operation counts as "using" it.
	noteOperationUse(ctx, c.l, op.ID)
	return op, nil
}

func (c *catalogAPI) ResolveOperation(ctx context.Context, ref string) (*domain.Operation, error) {
	op, err := c.l.cat.ResolveOperation(ctx, ref)
	if err != nil {
		return nil, err
	}
	noteOperationUse(ctx, c.l, op.ID)
	return op, nil
}

func (c *catalogAPI) ListOperations(ctx context.Context, service string) ([]domain.Operation, error) {
	return c.l.cat.ListOperations(ctx, service)
}

func (c *catalogAPI) Fields(ctx context.Context, operationID string) ([]domain.Field, error) {
	return c.l.cat.Fields(ctx, operationID)
}

func (c *catalogAPI) GetSchema(ctx context.Context, service, name string) (*domain.NamedSchema, error) {
	return c.l.cat.GetSchema(ctx, service, name)
}

func (c *catalogAPI) ListDocs(ctx context.Context, service string) ([]domain.Doc, error) {
	return c.l.cat.ListDocs(ctx, service)
}

func (c *catalogAPI) GetDoc(ctx context.Context, service, path string) (*domain.Doc, error) {
	return c.l.cat.GetDoc(ctx, service, path)
}
