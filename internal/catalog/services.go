package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/store"
)

// ListServices returns every registered service, ordered by name.
func (c *Catalog) ListServices(ctx context.Context) ([]domain.Service, error) {
	rows, err := c.db.SQL().QueryContext(ctx, `SELECT doc_json FROM services ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list services: %w", err)
	}
	defer rows.Close()

	var out []domain.Service
	for rows.Next() {
		var docJSON string
		if err := rows.Scan(&docJSON); err != nil {
			return nil, fmt.Errorf("catalog: scan service: %w", err)
		}
		var svc domain.Service
		if err := store.UnmarshalJSON(docJSON, &svc); err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate services: %w", err)
	}
	return out, nil
}

// GetService returns the service with the given id (its name), or
// errs.ServiceNotFound.
func (c *Catalog) GetService(ctx context.Context, id string) (*domain.Service, error) {
	var docJSON string
	err := c.db.SQL().QueryRowContext(ctx, `SELECT doc_json FROM services WHERE id = ?`, id).Scan(&docJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get service %q: %w", id, err)
	}
	var svc domain.Service
	if err := store.UnmarshalJSON(docJSON, &svc); err != nil {
		return nil, err
	}
	return &svc, nil
}
