package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/store"
)

// ListFlows returns the flows owned by (ownerKind, ownerID). ownerKind == ""
// returns every flow regardless of owner; ownerKind != "" with ownerID == ""
// returns every flow of that kind (e.g. every "service"-owned flow across
// every service).
func (c *Catalog) ListFlows(ctx context.Context, ownerKind, ownerID string) ([]domain.FlowSummary, error) {
	query := `SELECT id, owner_kind, owner_id, path, name, tags_json, ops_json, step_count, hash, updated FROM flows`
	var args []any
	if ownerKind != "" {
		query += ` WHERE owner_kind = ?`
		args = append(args, ownerKind)
		if ownerID != "" {
			query += ` AND owner_id = ?`
			args = append(args, ownerID)
		}
	}
	query += ` ORDER BY owner_kind, owner_id, path`

	rows, err := c.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list flows: %w", err)
	}
	defer rows.Close()

	var out []domain.FlowSummary
	for rows.Next() {
		f, err := scanFlowRow(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog: scan flow: %w", err)
		}
		out = append(out, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate flows: %w", err)
	}
	return out, nil
}

// UpsertFlows replaces every flow owned by (ownerKind, ownerID) with flows.
// Workspace flows use ownerKind "workspace", ownerID "".
func (c *Catalog) UpsertFlows(ctx context.Context, ownerKind, ownerID string, flows []domain.FlowSummary) error {
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		return upsertFlowsTx(ctx, tx, ownerKind, ownerID, flows, time.Now().UTC())
	})
}

func upsertFlowsTx(ctx context.Context, tx *sql.Tx, ownerKind, ownerID string, flows []domain.FlowSummary, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM flows WHERE owner_kind = ? AND owner_id = ?`, ownerKind, ownerID); err != nil {
		return fmt.Errorf("catalog: delete flows for %s/%s: %w", ownerKind, ownerID, err)
	}
	for _, f := range flows {
		tagsJSON, err := store.MarshalJSON(f.Tags)
		if err != nil {
			return fmt.Errorf("catalog: marshal flow tags %q: %w", f.ID, err)
		}
		ops := f.Operations
		if ops == nil {
			ops = []string{}
		}
		opsJSON, err := store.MarshalJSON(ops)
		if err != nil {
			return fmt.Errorf("catalog: marshal flow operations %q: %w", f.ID, err)
		}
		updated := f.Updated
		if updated.IsZero() {
			updated = now
		}
		id := f.ID
		if id == "" {
			id = store.NewID("flow")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO flows (id, owner_kind, owner_id, path, name, tags_json, ops_json, step_count, hash, updated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, ownerKind, ownerID, f.Path, f.Name, tagsJSON, opsJSON, f.StepCount, f.Hash, updated.Format(time.RFC3339)); err != nil {
			return fmt.Errorf("catalog: insert flow %q: %w", id, err)
		}
	}
	return nil
}

// GetFlowSummary returns the flow with the given ID, or (nil, nil) if it
// doesn't exist.
func (c *Catalog) GetFlowSummary(ctx context.Context, id string) (*domain.FlowSummary, error) {
	row := c.db.SQL().QueryRowContext(ctx,
		`SELECT id, owner_kind, owner_id, path, name, tags_json, ops_json, step_count, hash, updated FROM flows WHERE id = ?`, id)
	f, err := scanFlowRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get flow %q: %w", id, err)
	}
	return f, nil
}

// FlowsUsingOperation returns every flow whose Operations include opID.
func (c *Catalog) FlowsUsingOperation(ctx context.Context, opID string) ([]domain.FlowSummary, error) {
	rows, err := c.db.SQL().QueryContext(ctx,
		`SELECT id, owner_kind, owner_id, path, name, tags_json, ops_json, step_count, hash, updated FROM flows ORDER BY owner_kind, owner_id, path`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list flows: %w", err)
	}
	defer rows.Close()

	var out []domain.FlowSummary
	for rows.Next() {
		f, err := scanFlowRow(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog: scan flow: %w", err)
		}
		for _, op := range f.Operations {
			if op == opID {
				out = append(out, *f)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate flows: %w", err)
	}
	return out, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFlowRow(s rowScanner) (*domain.FlowSummary, error) {
	var f domain.FlowSummary
	var tagsJSON, opsJSON, updated string
	if err := s.Scan(&f.ID, &f.OwnerKind, &f.OwnerID, &f.Path, &f.Name, &tagsJSON, &opsJSON, &f.StepCount, &f.Hash, &updated); err != nil {
		return nil, err
	}
	if err := store.UnmarshalJSON(tagsJSON, &f.Tags); err != nil {
		return nil, err
	}
	if err := store.UnmarshalJSON(opsJSON, &f.Operations); err != nil {
		return nil, err
	}
	if updated != "" {
		t, err := time.Parse(time.RFC3339, updated)
		if err == nil {
			f.Updated = t
		}
	}
	return &f, nil
}
