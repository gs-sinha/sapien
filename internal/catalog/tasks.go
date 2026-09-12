package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
)

func replaceTasksTx(ctx context.Context, tx *sql.Tx, serviceID string, tasks []domain.Task, operations []domain.Operation) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE service_id = ?`, serviceID)
	if err != nil {
		return fmt.Errorf("catalog: query tasks for %q: %w", serviceID, err)
	}
	var oldIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("catalog: scan task id: %w", err)
		}
		oldIDs = append(oldIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("catalog: iterate tasks: %w", err)
	}
	rows.Close()
	for _, id := range oldIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks_fts WHERE task_id = ?`, id); err != nil {
			return fmt.Errorf("catalog: delete tasks_fts %q: %w", id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE service_id = ?`, serviceID); err != nil {
		return fmt.Errorf("catalog: delete tasks for %q: %w", serviceID, err)
	}

	knownOps := make(map[string]bool, len(operations))
	for _, op := range operations {
		knownOps[op.ID] = true
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		id := serviceID + "." + task.ID
		if seen[id] {
			continue
		}
		seen[id] = true
		docJSON, err := store.MarshalJSON(task)
		if err != nil {
			return fmt.Errorf("catalog: marshal task %q: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, service_id, raw_id, doc_json) VALUES (?, ?, ?, ?)`, id, serviceID, task.ID, docJSON); err != nil {
			return fmt.Errorf("catalog: insert task %q: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks_fts (task_id, service, phrases) VALUES (?, ?, ?)`, id, serviceID, strings.Join(task.Phrases, " ")); err != nil {
			return fmt.Errorf("catalog: insert tasks_fts %q: %w", id, err)
		}
		for ord, target := range task.Targets {
			if !knownOps[target.Operation] {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_targets (task_id, operation_id, when_text, ord) VALUES (?, ?, ?, ?)`, id, target.Operation, target.When, ord); err != nil {
				return fmt.Errorf("catalog: insert task target %q/%q: %w", id, target.Operation, err)
			}
		}
	}
	return nil
}

// ListTasks returns normalized task contracts for a service.
func (c *Catalog) ListTasks(ctx context.Context, service string) ([]domain.Task, error) {
	q := `SELECT doc_json FROM tasks`
	var args []any
	if service != "" {
		q += ` WHERE service_id = ?`
		args = append(args, service)
	}
	q += ` ORDER BY service_id, raw_id`
	rows, err := c.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list tasks: %w", err)
	}
	defer rows.Close()
	var out []domain.Task
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("catalog: scan task: %w", err)
		}
		var task domain.Task
		if err := store.UnmarshalJSON(raw, &task); err != nil {
			return nil, fmt.Errorf("catalog: decode task: %w", err)
		}
		out = append(out, task)
	}
	return out, rows.Err()
}
