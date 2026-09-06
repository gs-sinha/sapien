package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/store"
)

// GetSchema returns the named component schema for service, or (nil, nil) if
// it doesn't exist.
func (c *Catalog) GetSchema(ctx context.Context, service, name string) (*domain.NamedSchema, error) {
	var docJSON string
	err := c.db.SQL().QueryRowContext(ctx,
		`SELECT doc_json FROM schemas WHERE service_id = ? AND name = ?`, service, name).Scan(&docJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get schema %q/%q: %w", service, name, err)
	}
	var s domain.NamedSchema
	if err := store.UnmarshalJSON(docJSON, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSchemas returns every schema for service (or every schema, if service
// is ""), ordered by service then name.
func (c *Catalog) ListSchemas(ctx context.Context, service string) ([]domain.NamedSchema, error) {
	query := `SELECT doc_json FROM schemas`
	var args []any
	if service != "" {
		query += ` WHERE service_id = ?`
		args = append(args, service)
	}
	query += ` ORDER BY service_id, name`

	rows, err := c.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list schemas: %w", err)
	}
	defer rows.Close()

	var out []domain.NamedSchema
	for rows.Next() {
		var docJSON string
		if err := rows.Scan(&docJSON); err != nil {
			return nil, fmt.Errorf("catalog: scan schema: %w", err)
		}
		var s domain.NamedSchema
		if err := store.UnmarshalJSON(docJSON, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate schemas: %w", err)
	}
	return out, nil
}
