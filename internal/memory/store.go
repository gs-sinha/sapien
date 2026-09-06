package memory

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/store"
)

// Store is the memory index + CRUD surface: it owns writing/reading a
// memory's canonical file (via Locator) and keeping the SQLite index
// (memories, memory_subjects, memories_fts) in sync with it.
type Store struct {
	db  *store.DB
	loc Locator
	res Resolver
}

// New builds a Store over db, using loc to place non-personal memories' files
// and res (which may be nil, meaning subjects are never resolved against a
// catalog) to resolve subjects and expand structural retrieval.
func New(db *store.DB, loc Locator, res Resolver) *Store {
	return &Store{db: db, loc: loc, res: res}
}

// Create validates m, assigns it an ID and timestamps, applies defaults
// (type=note, status=active, source.kind=user, scope=workspace), resolves its
// subject against the Resolver (if any), writes its file (for every scope but
// personal), and indexes it. It returns the stored memory (a copy of m with
// ID/timestamps/defaults/FilePath/Hash/Resolved filled in).
func (s *Store) Create(ctx context.Context, m domain.Memory) (*domain.Memory, error) {
	applyMemoryDefaults(&m)
	if err := validateMemoryInput(m); err != nil {
		return nil, err
	}

	m.ID = store.NewID("mem")
	now := time.Now().UTC()
	m.Created = now
	m.Updated = now
	m.Resolved = s.resolveSubject(ctx, m.Subject)

	if err := s.writeFileForScope(&m, ""); err != nil {
		return nil, err
	}
	m.Hash = hashBytes(Format(m))

	if err := s.index(ctx, m); err != nil {
		return nil, err
	}
	out := m
	return &out, nil
}

// Get returns the memory with the given ID, or an errs.MemoryNotFound error.
func (s *Store) Get(ctx context.Context, id string) (*domain.Memory, error) {
	var m domain.Memory
	found := false
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		row := conn.QueryRowContext(ctx, `SELECT `+memoryCols+` FROM memories WHERE id = ?`, id)
		mm, err := scanMemory(row)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		m, found = mm, true
		return nil
	})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "get memory %s", id)
	}
	if !found {
		return nil, errs.New(errs.MemoryNotFound, "memory %s not found", id)
	}
	return &m, nil
}

// Update validates m (which must have an ID naming an existing memory),
// preserves Created, bumps Updated, re-resolves its subject, rewrites its
// file (moving it if the scope changed, removing the old file if it moved to
// or from personal scope), and reindexes it.
func (s *Store) Update(ctx context.Context, m domain.Memory) (*domain.Memory, error) {
	if m.ID == "" {
		return nil, errs.New(errs.Invalid, "memory: update requires an id")
	}
	existing, err := s.Get(ctx, m.ID)
	if err != nil {
		return nil, err
	}

	if m.Type == "" {
		m.Type = existing.Type
	}
	if m.Status == "" {
		m.Status = existing.Status
	}
	if m.Scope == "" {
		m.Scope = existing.Scope
	}
	if m.Source.Kind == "" {
		m.Source = existing.Source
	}
	if err := validateMemoryInput(m); err != nil {
		return nil, err
	}

	m.Created = existing.Created
	m.Updated = time.Now().UTC()
	m.Resolved = s.resolveSubject(ctx, m.Subject)

	if err := s.writeFileForScope(&m, existing.FilePath); err != nil {
		return nil, err
	}
	m.Hash = hashBytes(Format(m))

	if err := s.index(ctx, m); err != nil {
		return nil, err
	}
	out := m
	return &out, nil
}

// writeFileForScope resolves m's file path from its (possibly just-changed)
// scope/subject, writes its file for every scope but personal, and removes
// oldPath if the memory moved away from it (a different path, or a move to
// personal scope leaving no file at all).
func (s *Store) writeFileForScope(m *domain.Memory, oldPath string) error {
	if m.Scope == domain.ScopePersonal {
		m.FilePath = ""
	} else {
		dir, err := s.loc.DirFor(*m)
		if err != nil {
			return err
		}
		m.FilePath = filepath.Join(dir, FileName(m.ID))
		if err := WriteFile(*m); err != nil {
			return err
		}
	}
	if oldPath != "" && oldPath != m.FilePath {
		_ = os.Remove(oldPath)
	}
	return nil
}

// Delete removes the memory's file (if any) and its index rows (memories,
// memory_subjects, memories_fts).
func (s *Store) Delete(ctx context.Context, id string) error {
	existing, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if existing.FilePath != "" {
		_ = os.Remove(existing.FilePath)
	}
	return s.deindex(ctx, id)
}

// List returns memories matching q, ordered by Updated descending, up to
// q.Limit (default 50). Unlike Search/Relevant, List does not exclude
// inactive (superseded/deprecated/etc.) memories.
func (s *Store) List(ctx context.Context, q domain.MemoryQuery) ([]domain.Memory, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	var where []string
	var args []any
	if q.Scope != "" {
		where = append(where, "scope = ?")
		args = append(args, string(q.Scope))
	}
	if q.Type != "" {
		where = append(where, "type = ?")
		args = append(args, string(q.Type))
	}
	if q.Service != "" {
		where = append(where, `id IN (
			SELECT memory_id FROM memory_subjects
			WHERE (kind = 'service' AND value = ?)
			   OR (kind = 'operation' AND (value = ? OR value LIKE ?))
		)`)
		args = append(args, q.Service, q.Service, q.Service+".%")
	}
	if q.Operation != "" {
		where = append(where, `id IN (SELECT memory_id FROM memory_subjects WHERE kind = 'operation' AND value = ?)`)
		args = append(args, q.Operation)
	}
	if q.Flow != "" {
		where = append(where, `id IN (SELECT memory_id FROM memory_subjects WHERE kind = 'flow' AND value = ?)`)
		args = append(args, q.Flow)
	}

	query := `SELECT ` + memoryCols + ` FROM memories`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated DESC LIMIT ?"
	args = append(args, limit)

	var out []domain.Memory
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMemory(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "list memories")
	}
	return out, nil
}

// Reindex rebuilds the SQLite index for every non-personal memory from its
// file: it reads every file Locator.Files finds, upserts each by ID, and
// deletes any non-personal index row whose file no longer exists. Personal
// memories (file_path NULL) are never touched. It returns the number of
// files successfully indexed.
func (s *Store) Reindex(ctx context.Context) (int, error) {
	files, err := s.loc.Files()
	if err != nil {
		return 0, err
	}

	seen := map[string]bool{}
	count := 0
	for _, f := range files {
		m, err := ReadFile(f)
		if err != nil {
			// A malformed file keeps whatever was last indexed for it (mirrors
			// PLAN §17: "parse errors keep the last good catalog"); skip it
			// rather than failing the whole reindex.
			continue
		}
		m.Resolved = s.resolveSubject(ctx, m.Subject)
		if err := s.index(ctx, m); err != nil {
			return count, err
		}
		seen[m.ID] = true
		count++
	}

	var stale []string
	err = s.db.Read(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `SELECT id FROM memories WHERE scope != 'personal'`)
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
		return count, errs.Wrap(errs.Internal, err, "reindex: find stale memory rows")
	}

	for _, id := range stale {
		if err := s.deindex(ctx, id); err != nil {
			return count, err
		}
	}
	return count, nil
}

// IndexOne (P)arses and (re)indexes the memory file at path. Used by the file
// watcher when a memory file is created or changed.
func (s *Store) IndexOne(ctx context.Context, path string) error {
	m, err := ReadFile(path)
	if err != nil {
		return err
	}
	m.Resolved = s.resolveSubject(ctx, m.Subject)
	return s.index(ctx, m)
}

// RemovePath removes the index rows for whichever memory is recorded with
// file_path = path (a no-op if none is). Used by the file watcher when a
// memory file is deleted.
func (s *Store) RemovePath(ctx context.Context, path string) error {
	var id string
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		row := conn.QueryRowContext(ctx, `SELECT id FROM memories WHERE file_path = ?`, path)
		err := row.Scan(&id)
		if err == sql.ErrNoRows {
			id = ""
			return nil
		}
		return err
	})
	if err != nil {
		return errs.Wrap(errs.Internal, err, "find memory for path %s", path)
	}
	if id == "" {
		return nil
	}
	return s.deindex(ctx, id)
}

// resolveSubject resolves subj.Operation against the Resolver (if any),
// returning the ResolvedSubject snapshot PLAN §11 describes. It returns nil
// when there's no Resolver or no operation to resolve (nothing to snapshot).
// When the operation is known to the catalog but subj.Field isn't one of its
// fields, the snapshot is still returned but flagged Unresolved.
func (s *Store) resolveSubject(ctx context.Context, subj domain.Subject) *domain.ResolvedSubject {
	if s.res == nil || subj.Operation == "" {
		return nil
	}
	now := time.Now().UTC()
	info, ok := s.res.Operation(ctx, subj.Operation)
	if !ok {
		return &domain.ResolvedSubject{Unresolved: true, ResolvedAt: now}
	}
	unresolved := subj.Field != "" && !s.res.FieldExists(ctx, subj.Operation, subj.Field)
	return &domain.ResolvedSubject{
		Method:        info.Method,
		Path:          info.Path,
		OperationHash: info.Hash,
		ResolvedAt:    now,
		Unresolved:    unresolved,
	}
}

// applyMemoryDefaults fills in the defaults PLAN.md §10 describes for a
// newly created memory: type=note, status=active, scope=workspace,
// source.kind=user.
func applyMemoryDefaults(m *domain.Memory) {
	if m.Type == "" {
		m.Type = domain.MemoryNote
	}
	if m.Status == "" {
		m.Status = domain.MemoryActive
	}
	if m.Scope == "" {
		m.Scope = domain.ScopeWorkspace
	}
	if m.Source.Kind == "" {
		m.Source = domain.MemorySource{Kind: "user"}
	}
}

// validateMemoryInput checks the invariants Create/Update both enforce: a
// non-empty body, and subject.field requiring subject.operation or
// subject.schema (PLAN §11: a field path "needs operation or schema").
func validateMemoryInput(m domain.Memory) error {
	if strings.TrimSpace(m.Text) == "" {
		return errs.New(errs.Invalid, "memory: text is required")
	}
	if m.Subject.Field != "" && m.Subject.Operation == "" && m.Subject.Schema == "" {
		return errs.New(errs.Invalid, "memory: subject.field requires subject.operation or subject.schema")
	}
	return nil
}
