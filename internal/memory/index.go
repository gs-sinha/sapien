package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// memoryCols is the column list (in scan order) used by every SELECT against
// the memories table.
const memoryCols = `id, scope, type, file_path, subject_json, tags_json, source_json, status, body, created, updated, hash, resolved_json`

// scanner is satisfied by both *sql.Row and *sql.Rows, letting scanMemory
// serve Get (single row) and List/lookup helpers (many rows) alike.
type scanner interface {
	Scan(dest ...any) error
}

// scanMemory scans one row (selected via memoryCols, in that order) into a
// domain.Memory.
func scanMemory(sc scanner) (domain.Memory, error) {
	var m domain.Memory
	var scope, typ, status, created, updated, subjectJSON, tagsJSON, sourceJSON string
	var filePath, resolvedJSON sql.NullString

	if err := sc.Scan(&m.ID, &scope, &typ, &filePath, &subjectJSON, &tagsJSON, &sourceJSON,
		&status, &m.Text, &created, &updated, &m.Hash, &resolvedJSON); err != nil {
		return domain.Memory{}, err
	}

	m.Scope = domain.MemoryScope(scope)
	m.Type = domain.MemoryType(typ)
	m.Status = domain.MemoryStatus(status)
	m.FilePath = store.StringOrEmpty(filePath)

	_ = store.UnmarshalJSON(subjectJSON, &m.Subject)
	var tags []string
	_ = store.UnmarshalJSON(tagsJSON, &tags)
	m.Tags = tags
	_ = store.UnmarshalJSON(sourceJSON, &m.Source)

	if t, err := time.Parse(time.RFC3339, created); err == nil {
		m.Created = t
	}
	if t, err := time.Parse(time.RFC3339, updated); err == nil {
		m.Updated = t
	}
	if resolvedJSON.Valid && resolvedJSON.String != "" {
		var r domain.ResolvedSubject
		if err := store.UnmarshalJSON(resolvedJSON.String, &r); err == nil {
			m.Resolved = &r
		}
	}
	return m, nil
}

// index upserts m into the memories table and rebuilds its memory_subjects
// and memories_fts rows, all inside one write transaction.
func (s *Store) index(ctx context.Context, m domain.Memory) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if err := upsertMemory(ctx, tx, m); err != nil {
			return err
		}
		if err := reindexSubjects(ctx, tx, m); err != nil {
			return err
		}
		if err := reindexFTS(ctx, tx, m); err != nil {
			return err
		}
		return nil
	})
}

// deindex removes every row (memories, memory_subjects, memories_fts)
// associated with memory id.
func (s *Store) deindex(ctx context.Context, id string) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id); err != nil {
			return fmt.Errorf("memory: delete memories row: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_subjects WHERE memory_id = ?`, id); err != nil {
			return fmt.Errorf("memory: delete memory_subjects rows: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memories_fts WHERE id = ?`, id); err != nil {
			return fmt.Errorf("memory: delete memories_fts row: %w", err)
		}
		return nil
	})
}

func upsertMemory(ctx context.Context, tx *sql.Tx, m domain.Memory) error {
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	subjectJSON, err := store.MarshalJSON(m.Subject)
	if err != nil {
		return fmt.Errorf("memory: marshal subject: %w", err)
	}
	tagsJSON, err := store.MarshalJSON(tags)
	if err != nil {
		return fmt.Errorf("memory: marshal tags: %w", err)
	}
	sourceJSON, err := store.MarshalJSON(m.Source)
	if err != nil {
		return fmt.Errorf("memory: marshal source: %w", err)
	}
	var resolvedJSON sql.NullString
	if m.Resolved != nil {
		s, err := store.MarshalJSON(m.Resolved)
		if err != nil {
			return fmt.Errorf("memory: marshal resolved: %w", err)
		}
		resolvedJSON = sql.NullString{String: s, Valid: true}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO memories (id, scope, type, file_path, subject_json, tags_json, source_json, status, body, created, updated, hash, resolved_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			scope = excluded.scope,
			type = excluded.type,
			file_path = excluded.file_path,
			subject_json = excluded.subject_json,
			tags_json = excluded.tags_json,
			source_json = excluded.source_json,
			status = excluded.status,
			body = excluded.body,
			created = excluded.created,
			updated = excluded.updated,
			hash = excluded.hash,
			resolved_json = excluded.resolved_json
	`,
		m.ID, string(m.Scope), string(m.Type), store.NullString(m.FilePath),
		subjectJSON, tagsJSON, sourceJSON, string(m.Status), m.Text,
		m.Created.UTC().Format(time.RFC3339), m.Updated.UTC().Format(time.RFC3339),
		m.Hash, resolvedJSON,
	)
	if err != nil {
		return fmt.Errorf("memory: upsert memories row: %w", err)
	}
	return nil
}

// subjectRow is one memory_subjects row to be written for a memory.
type subjectRow struct {
	kind     string
	value    string
	resolved *domain.ResolvedSubject
}

// subjectRows expands m.Subject into the memory_subjects rows PLAN.md §15
// describes: one row per non-empty subject key, with field/error values
// composed as "<operation-or-schema>#<field-path>" / "<operation>#<status>#<code>".
func subjectRows(m domain.Memory) []subjectRow {
	subj := m.Subject
	var out []subjectRow
	add := func(kind, value string, resolved *domain.ResolvedSubject) {
		if value == "" {
			return
		}
		out = append(out, subjectRow{kind: kind, value: value, resolved: resolved})
	}

	add("service", subj.Service, nil)
	add("operation", subj.Operation, m.Resolved)
	if subj.Field != "" {
		var key string
		switch {
		case subj.Operation != "":
			key = subj.Operation + "#" + subj.Field
		case subj.Schema != "":
			key = subj.Schema + "#" + subj.Field
		default:
			key = subj.Field
		}
		add("field", key, m.Resolved)
	}
	add("schema", subj.Schema, nil)
	add("flow", subj.Flow, nil)
	add("step", subj.Step, nil)
	add("run", subj.Run, nil)
	add("environment", subj.Environment, nil)
	if subj.Error != nil {
		add("error", fmt.Sprintf("%s#%d#%s", subj.Error.Operation, subj.Error.Status, subj.Error.Code), nil)
	}
	add("concept", subj.Concept, nil)
	return out
}

func reindexSubjects(ctx context.Context, tx *sql.Tx, m domain.Memory) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_subjects WHERE memory_id = ?`, m.ID); err != nil {
		return fmt.Errorf("memory: delete memory_subjects rows: %w", err)
	}
	for _, r := range subjectRows(m) {
		var resolvedJSON sql.NullString
		if r.resolved != nil {
			s, err := store.MarshalJSON(r.resolved)
			if err != nil {
				return fmt.Errorf("memory: marshal subject resolved snapshot: %w", err)
			}
			resolvedJSON = sql.NullString{String: s, Valid: true}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memory_subjects (memory_id, kind, value, resolved_json) VALUES (?,?,?,?)`,
			m.ID, r.kind, r.value, resolvedJSON,
		); err != nil {
			return fmt.Errorf("memory: insert memory_subjects row: %w", err)
		}
	}
	return nil
}

// subjectFTSTokens tokenizes every non-empty subject value (via
// textutil.Tokens, so identifiers split the same way the catalog's own
// indexer splits them) into the memories_fts.subject_text column's content.
func subjectFTSTokens(subj domain.Subject) string {
	var parts []string
	for _, v := range []string{
		subj.Service, subj.Operation, subj.Field, subj.Schema,
		subj.Flow, subj.Step, subj.Run, subj.Environment, subj.Concept,
	} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	if subj.Error != nil {
		if subj.Error.Operation != "" {
			parts = append(parts, subj.Error.Operation)
		}
		if subj.Error.Code != "" {
			parts = append(parts, subj.Error.Code)
		}
	}
	return textutil.Join(textutil.Tokens(strings.Join(parts, " ")))
}

func tagsFTSTokens(tags []string) string {
	return textutil.Join(textutil.Tokens(strings.Join(tags, " ")))
}

func reindexFTS(ctx context.Context, tx *sql.Tx, m domain.Memory) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM memories_fts WHERE id = ?`, m.ID); err != nil {
		return fmt.Errorf("memory: delete memories_fts row: %w", err)
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (id, body, tags, subject_text) VALUES (?,?,?,?)`,
		m.ID, m.Text, tagsFTSTokens(m.Tags), subjectFTSTokens(m.Subject),
	)
	if err != nil {
		return fmt.Errorf("memory: insert memories_fts row: %w", err)
	}
	return nil
}
