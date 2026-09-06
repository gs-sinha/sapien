package example

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
)

// exampleCols is the column list (in scan order) used by every SELECT
// against the examples table.
const exampleCols = `id, operation, service, scope, description, tags, verified, env, path, created, updated`

// scanner is satisfied by both *sql.Row and *sql.Rows, letting scanIndexRow
// serve single-row (Get) and multi-row (List/ForOperations) lookups alike.
type scanner interface {
	Scan(dest ...any) error
}

// indexRow is one examples table row: just enough metadata to filter, order,
// and locate an example's file. It does not carry the example's full
// content (body/input/headers/expect/verified detail); that lives only in
// the file, read via readFile.
type indexRow struct {
	id          string
	operation   string
	service     string
	scope       domain.ExampleScope
	description string
	tags        []string
	verified    bool
	env         string
	path        string
	created     time.Time
	updated     time.Time
}

// scanIndexRow scans one row (selected via exampleCols, in that order) into
// an indexRow.
func scanIndexRow(sc scanner) (indexRow, error) {
	var r indexRow
	var scope, tagsJSON string
	var description, env sql.NullString
	var verifiedInt int
	var created, updated sql.NullString

	if err := sc.Scan(&r.id, &r.operation, &r.service, &scope, &description, &tagsJSON,
		&verifiedInt, &env, &r.path, &created, &updated); err != nil {
		return indexRow{}, err
	}

	r.scope = domain.ExampleScope(scope)
	r.description = description.String
	var tags []string
	_ = store.UnmarshalJSON(tagsJSON, &tags)
	r.tags = tags
	r.verified = verifiedInt != 0
	r.env = env.String
	if created.Valid {
		if t, err := time.Parse(time.RFC3339, created.String); err == nil {
			r.created = t
		}
	}
	if updated.Valid {
		if t, err := time.Parse(time.RFC3339, updated.String); err == nil {
			r.updated = t
		}
	}
	return r, nil
}

// index upserts ex's metadata into the examples table.
func (s *Store) index(ctx context.Context, ex domain.SavedExample) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		return upsertExample(ctx, tx, ex)
	})
}

// deindex removes the examples row for id.
func (s *Store) deindex(ctx context.Context, id string) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM examples WHERE id = ?`, id); err != nil {
			return fmt.Errorf("example: delete examples row: %w", err)
		}
		return nil
	})
}

func upsertExample(ctx context.Context, tx *sql.Tx, ex domain.SavedExample) error {
	tags := ex.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := store.MarshalJSON(tags)
	if err != nil {
		return fmt.Errorf("example: marshal tags: %w", err)
	}

	verified := 0
	var env sql.NullString
	if ex.Verified != nil {
		verified = 1
		env = store.NullString(ex.Verified.Env)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO examples (id, operation, service, scope, description, tags, verified, env, path, created, updated)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			operation = excluded.operation,
			service = excluded.service,
			scope = excluded.scope,
			description = excluded.description,
			tags = excluded.tags,
			verified = excluded.verified,
			env = excluded.env,
			path = excluded.path,
			created = excluded.created,
			updated = excluded.updated
	`,
		ex.ID, ex.Operation, ex.Service, string(ex.Scope), store.NullString(ex.Description), tagsJSON,
		verified, env, ex.Path, timeOrNull(ex.Created), timeOrNull(ex.Updated),
	)
	if err != nil {
		return fmt.Errorf("example: upsert examples row: %w", err)
	}
	return nil
}

// timeOrNull formats t as RFC3339 (UTC), or SQL NULL when t is zero.
func timeOrNull(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
}
