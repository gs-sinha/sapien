package example

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/store"
)

// Store is the example index + CRUD surface: it owns writing/reading an
// example's canonical "<id>.example.yaml" file (via Locator) and keeping
// the SQLite index (the examples table) in sync with it.
type Store struct {
	db  *store.DB
	loc Locator
}

// New builds a Store over db, using loc to place examples' files.
func New(db *store.DB, loc Locator) *Store {
	return &Store{db: db, loc: loc}
}

// Create validates ex, applies defaults (version=1, scope=workspace,
// created/updated=now), derives Service from Operation, writes its file,
// and indexes it. It returns errs.Conflict if an example with the same ID
// already exists, and errs.Invalid if ex.ID fails validation or
// ex.Operation is empty or not "<service>.<operationId>" shaped.
func (s *Store) Create(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	if err := validateID(ex.ID); err != nil {
		return nil, err
	}
	if err := validateOperation(ex.Operation); err != nil {
		return nil, err
	}

	if ex.Version == 0 {
		ex.Version = 1
	}
	if ex.Scope == "" {
		ex.Scope = domain.ExampleScopeWorkspace
	}
	ex.Service = serviceFromOperation(ex.Operation)

	now := time.Now().UTC()
	ex.Created = now
	ex.Updated = now

	exists, err := s.exists(ctx, ex.ID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errs.New(errs.Conflict, "example %q already exists", ex.ID).WithDetail("id", ex.ID)
	}

	path, err := s.loc.PathFor(ex)
	if err != nil {
		return nil, err
	}
	ex.Path = path
	// Re-derived from the resulting path rather than left as whatever the
	// caller passed (typically ""): PathFor treats an unset Tier as "local
	// tier" for workspace scope, so this turns that default into an
	// explicit domain.TierLocal on the record (PLAN §7b), mirroring
	// memory.Store.writeFileForScope.
	ex.Tier = s.loc.tierOfPath(path)

	if err := writeFile(&ex); err != nil {
		return nil, err
	}
	if err := s.index(ctx, ex); err != nil {
		return nil, err
	}

	out := ex
	return &out, nil
}

// Get returns the example with the given ID: it looks up the example's file
// path in the index, then reads and parses the file itself (the index does
// not carry the full body/input/headers/expect/verified content). It
// returns an errs.ExampleNotFound error with Details{"id": id} when no such
// example is indexed.
func (s *Store) Get(ctx context.Context, id string) (*domain.SavedExample, error) {
	row, err := s.getIndexRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errs.New(errs.ExampleNotFound, "example %q not found", id).WithDetail("id", id)
	}
	ex, err := readFile(row.path, row.scope)
	if err != nil {
		return nil, err
	}
	ex.Tier = s.loc.tierOfPath(ex.Path)
	return ex, nil
}

// Update validates ex (which must have an ID naming an existing example),
// preserves Created, bumps Updated, rewrites its file (moving it, and
// removing the old file, if the scope changed), and reindexes it.
func (s *Store) Update(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	if ex.ID == "" {
		return nil, errs.New(errs.Invalid, "example: update requires an id")
	}
	existing, err := s.Get(ctx, ex.ID)
	if err != nil {
		return nil, err
	}

	if ex.Version == 0 {
		ex.Version = existing.Version
	}
	if ex.Scope == "" {
		ex.Scope = existing.Scope
	}
	// A caller that doesn't name a tier keeps the example where it already
	// is (PLAN §7b) -- see memory.Store.Update's identical guard against
	// PathFor's "unset Tier defaults to local" reading a plain field edit
	// as a request to move it back to local.
	if ex.Tier == "" {
		ex.Tier = existing.Tier
	}
	if ex.Operation == "" {
		ex.Operation = existing.Operation
	}
	if err := validateOperation(ex.Operation); err != nil {
		return nil, err
	}
	ex.Service = serviceFromOperation(ex.Operation)

	ex.Created = existing.Created
	ex.Updated = time.Now().UTC()

	oldPath := existing.Path
	newPath, err := s.loc.PathFor(ex)
	if err != nil {
		return nil, err
	}
	ex.Path = newPath
	ex.Tier = s.loc.tierOfPath(newPath)

	if err := writeFile(&ex); err != nil {
		return nil, err
	}
	if oldPath != "" && oldPath != ex.Path {
		_ = os.Remove(oldPath)
	}

	if err := s.index(ctx, ex); err != nil {
		return nil, err
	}

	out := ex
	return &out, nil
}

// Delete removes the example's file and its index row.
func (s *Store) Delete(ctx context.Context, id string) error {
	existing, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if existing.Path != "" {
		_ = os.Remove(existing.Path)
	}
	return s.deindex(ctx, id)
}

// List returns examples matching q, ordered by Updated descending then ID
// ascending, up to q.Limit (default 50). Filters: Operation and Service
// match exactly, Tag matches exactly against one of the example's tags,
// Text is a case-insensitive substring match over id, description, and
// tags. Matching files that can no longer be read or parsed are skipped
// (mirrors Reindex's "skip bad files" policy).
func (s *Store) List(ctx context.Context, q domain.ExampleQuery) ([]domain.SavedExample, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	var where []string
	var args []any
	if q.Operation != "" {
		where = append(where, "operation = ?")
		args = append(args, q.Operation)
	}
	if q.Service != "" {
		where = append(where, "service = ?")
		args = append(args, q.Service)
	}
	if q.Tag != "" {
		where = append(where, "tags LIKE ?")
		args = append(args, `%"`+q.Tag+`"%`)
	}
	if q.Text != "" {
		where = append(where, "(LOWER(id) LIKE LOWER(?) OR LOWER(description) LIKE LOWER(?) OR LOWER(tags) LIKE LOWER(?))")
		pat := "%" + q.Text + "%"
		args = append(args, pat, pat, pat)
	}

	query := `SELECT ` + exampleCols + ` FROM examples`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated DESC, id ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.readIndexRows(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return s.hydrate(rows), nil
}

// ForOperations returns up to limit (default 50) examples of the given
// operation IDs, verified examples first, then Updated descending. Used by
// the context builder to show an agent known-good payloads for the
// operations it selected.
func (s *Store) ForOperations(ctx context.Context, operationIDs []string, limit int) ([]domain.SavedExample, error) {
	if len(operationIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	placeholders := make([]string, len(operationIDs))
	args := make([]any, len(operationIDs), len(operationIDs)+1)
	for i, op := range operationIDs {
		placeholders[i] = "?"
		args[i] = op
	}
	args = append(args, limit)

	query := `SELECT ` + exampleCols + ` FROM examples WHERE operation IN (` +
		strings.Join(placeholders, ",") + `) ORDER BY verified DESC, updated DESC LIMIT ?`

	rows, err := s.readIndexRows(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return s.hydrate(rows), nil
}

// Reindex rebuilds the SQLite index for every example file: it reads every
// file Locator.files finds, upserts each by ID, and deletes any index row
// whose file no longer exists. It returns the number of files successfully
// indexed; when one or more files could not be read or parsed, it also
// returns a non-nil error (code errs.Invalid) listing them in its "files"
// detail, without failing the rest of the reindex.
func (s *Store) Reindex(ctx context.Context) (int, error) {
	files, err := s.loc.files()
	if err != nil {
		return 0, err
	}

	seen := map[string]bool{}
	var badFiles []string
	count := 0
	for _, f := range files {
		ex, err := readFile(f.path, f.scope)
		if err != nil {
			// A malformed (or unreadable) file keeps whatever was last
			// indexed for it; skip it rather than failing the whole
			// reindex (mirrors memory.Store.Reindex).
			badFiles = append(badFiles, f.path)
			continue
		}
		if err := s.index(ctx, *ex); err != nil {
			return count, err
		}
		seen[ex.ID] = true
		count++
	}

	var stale []string
	err = s.db.Read(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `SELECT id FROM examples`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if !seen[id] {
				stale = append(stale, id)
			}
		}
		return rows.Err()
	})
	if err != nil {
		return count, errs.Wrap(errs.Internal, err, "reindex: find stale example rows")
	}
	for _, id := range stale {
		if err := s.deindex(ctx, id); err != nil {
			return count, err
		}
	}

	if len(badFiles) > 0 {
		return count, errs.New(errs.Invalid, "reindex: %d example file(s) could not be read or parsed", len(badFiles)).
			WithDetail("files", badFiles)
	}
	return count, nil
}

// IndexOne parses and (re)indexes the example file at path, determining its
// scope from which known directory it lives under (Locator.scopeForPath).
// Used by a file watcher when an example file is created or changed.
func (s *Store) IndexOne(ctx context.Context, path string) error {
	ex, err := readFile(path, s.loc.scopeForPath(path))
	if err != nil {
		return err
	}
	return s.index(ctx, *ex)
}

// RemovePath removes the index row for whichever example is recorded with
// path = path (a no-op if none is). Used by a file watcher when an example
// file is deleted.
func (s *Store) RemovePath(ctx context.Context, path string) error {
	var id string
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		row := conn.QueryRowContext(ctx, `SELECT id FROM examples WHERE path = ?`, path)
		err := row.Scan(&id)
		if err == sql.ErrNoRows {
			id = ""
			return nil
		}
		return err
	})
	if err != nil {
		return errs.Wrap(errs.Internal, err, "find example for path %s", path)
	}
	if id == "" {
		return nil
	}
	return s.deindex(ctx, id)
}

// exists reports whether an example with the given ID is already indexed.
func (s *Store) exists(ctx context.Context, id string) (bool, error) {
	row, err := s.getIndexRow(ctx, id)
	if err != nil {
		return false, err
	}
	return row != nil, nil
}

// getIndexRow returns the examples row for id, or nil (with a nil error) if
// none is indexed.
func (s *Store) getIndexRow(ctx context.Context, id string) (*indexRow, error) {
	var row indexRow
	found := false
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		r := conn.QueryRowContext(ctx, `SELECT `+exampleCols+` FROM examples WHERE id = ?`, id)
		ir, err := scanIndexRow(r)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		row, found = ir, true
		return nil
	})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "get example %s", id)
	}
	if !found {
		return nil, nil
	}
	return &row, nil
}

// readIndexRows runs query (built by List/ForOperations against exampleCols)
// and scans every resulting row.
func (s *Store) readIndexRows(ctx context.Context, query string, args ...any) ([]indexRow, error) {
	var out []indexRow
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanIndexRow(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "query examples")
	}
	return out, nil
}

// hydrate reads and parses each row's file, in order, skipping any that can
// no longer be read or parsed (its backing file was deleted or hand-edited
// into something invalid since it was last indexed), and setting Tier from
// the Locator (there is no tier column to read it back from; see
// Locator.tierOfPath).
func (s *Store) hydrate(rows []indexRow) []domain.SavedExample {
	out := make([]domain.SavedExample, 0, len(rows))
	for _, r := range rows {
		ex, err := readFile(r.path, r.scope)
		if err != nil {
			continue
		}
		ex.Tier = s.loc.tierOfPath(ex.Path)
		out = append(out, *ex)
	}
	return out
}
