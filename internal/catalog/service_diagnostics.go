package catalog

import (
	"context"
	"fmt"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
)

// UpdateServiceDiagnostics replaces the denormalized service document after
// post-index retrieval assertions have run.
func (c *Catalog) UpdateServiceDiagnostics(ctx context.Context, svc domain.Service) error {
	docJSON, err := store.MarshalJSON(svc)
	if err != nil {
		return fmt.Errorf("catalog: marshal service diagnostics: %w", err)
	}
	if _, err := c.db.SQL().ExecContext(ctx, `UPDATE services SET doc_json = ? WHERE id = ?`, docJSON, svc.ID); err != nil {
		return fmt.Errorf("catalog: update service diagnostics %q: %w", svc.ID, err)
	}
	return nil
}
