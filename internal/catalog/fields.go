package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
)

// Fields returns every field of operationID, ordered by field path.
func (c *Catalog) Fields(ctx context.Context, operationID string) ([]domain.Field, error) {
	rows, err := c.db.SQL().QueryContext(ctx,
		`SELECT doc_json FROM fields WHERE operation_id = ? ORDER BY field_path`, operationID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list fields for %q: %w", operationID, err)
	}
	defer rows.Close()
	return scanFields(rows)
}

// FieldsByName returns every field of service whose leaf name (its field
// path's last "."-segment, with any trailing "[]" stripped) matches
// leafNameArg, case-insensitively. Used to join doc/memory text mentions of a
// field name back to the operations that carry it.
func (c *Catalog) FieldsByName(ctx context.Context, service, leafNameArg string) ([]domain.Field, error) {
	rows, err := c.db.SQL().QueryContext(ctx, `
		SELECT f.doc_json
		FROM fields f
		JOIN operations o ON o.id = f.operation_id
		WHERE o.service_id = ? AND f.leaf_name = ?
		ORDER BY f.operation_id, f.field_path
	`, service, strings.ToLower(leafNameArg))
	if err != nil {
		return nil, fmt.Errorf("catalog: fields by name %q/%q: %w", service, leafNameArg, err)
	}
	defer rows.Close()
	return scanFields(rows)
}

type rowsScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanFields(rows rowsScanner) ([]domain.Field, error) {
	var out []domain.Field
	for rows.Next() {
		var docJSON string
		if err := rows.Scan(&docJSON); err != nil {
			return nil, fmt.Errorf("catalog: scan field: %w", err)
		}
		var f domain.Field
		if err := store.UnmarshalJSON(docJSON, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate fields: %w", err)
	}
	return out, nil
}
