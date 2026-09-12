package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
)

// docTextBodyChars is how much of a doc section's body contributes to an
// operation's doc_text, per section (task spec: "the first 300 characters
// of its body").
const docTextBodyChars = 300

// memoryTextBodyChars is the equivalent cap for one memory's body.
const memoryTextBodyChars = 300

// knowledgeColumnCapBytes is the per-operation cap on doc_text and on
// memory_text (task spec: "capped at 2 KB per operation"), applied after
// joining every contributing section/memory.
const knowledgeColumnCapBytes = 2048

// querier is satisfied by both *sql.Tx and *sql.DB (via db.SQL()), letting
// the read helpers below run either inside Apply's existing transaction or
// standalone (RefreshOperationKnowledge, Reindex).
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// RefreshOperationKnowledge recomputes operations_fts.doc_text and
// .memory_text for opIDs (every operation, if opIDs is empty) from the
// doc_refs/doc_sections and memories/memory_subjects tables already in
// this workspace's database. Called by Apply right after it replaces a
// service's docs, and by internal/engine/local's memory-write hooks
// (Create/Update/Delete/Reindex — a rescope is an Update).
func (c *Catalog) RefreshOperationKnowledge(ctx context.Context, opIDs []string) error {
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		return c.refreshOperationKnowledgeTx(ctx, tx, opIDs)
	})
}

// knowledgeRefreshBatchSize bounds how many operations refreshOperationKnowledgeTx
// touches per DELETE/re-INSERT round trip. operations_fts.id is UNINDEXED
// (see 001_init.sql: fts5 gives it no secondary index), so both a
// WHERE id IN (...) SELECT and the matching DELETE cost one linear scan of
// the whole table regardless of how many ids are in the batch — batching
// turns what would otherwise be one such scan *per operation* (quadratic
// for a large Apply) into one scan per batch.
const knowledgeRefreshBatchSize = 200

// refreshOperationKnowledgeTx is RefreshOperationKnowledge's transactional
// core, reused by Apply (which already holds a write transaction) and by
// Reindex.
func (c *Catalog) refreshOperationKnowledgeTx(ctx context.Context, tx *sql.Tx, opIDs []string) error {
	ids := opIDs
	if len(ids) == 0 {
		all, err := queryIDsVia(ctx, tx, `SELECT id FROM operations`)
		if err != nil {
			return fmt.Errorf("catalog: list operations for knowledge refresh: %w", err)
		}
		ids = all
	}

	for start := 0; start < len(ids); start += knowledgeRefreshBatchSize {
		end := start + knowledgeRefreshBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := c.refreshKnowledgeBatchTx(ctx, tx, ids[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// ftsRowFull is every operations_fts column except doc_text/memory_text,
// read back so refreshKnowledgeBatchTx can delete-and-reinsert a batch of
// rows (the pattern 001_init.sql's header comment establishes for every
// other FTS update in this package) without losing the other columns.
type ftsRowFull struct {
	id, service, opID, pathTokens, summary, description, tags, paramNames, fieldNames string
}

// refreshKnowledgeBatchTx recomputes doc_text/memory_text for one batch of
// operation ids: read each row's other columns, delete the batch, then
// reinsert each row with a freshly computed doc_text/memory_text. An id
// with no existing operations_fts row (shouldn't normally happen; defensive
// only) is silently skipped rather than inserting a partial row.
func (c *Catalog) refreshKnowledgeBatchTx(ctx context.Context, tx *sql.Tx, batch []string) error {
	placeholders, args := placeholdersFor(batch)

	rows, err := tx.QueryContext(ctx, `
		SELECT id, service, op_id, path_tokens, summary, description, tags, param_names, field_names
		FROM operations_fts WHERE id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return fmt.Errorf("catalog: knowledge refresh: query existing operations_fts rows: %w", err)
	}
	existing := make(map[string]ftsRowFull, len(batch))
	for rows.Next() {
		var r ftsRowFull
		if err := rows.Scan(&r.id, &r.service, &r.opID, &r.pathTokens, &r.summary, &r.description, &r.tags, &r.paramNames, &r.fieldNames); err != nil {
			rows.Close()
			return fmt.Errorf("catalog: knowledge refresh: scan existing operations_fts row: %w", err)
		}
		existing[r.id] = r
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("catalog: knowledge refresh: iterate existing operations_fts rows: %w", err)
	}
	rows.Close()

	if _, err := tx.ExecContext(ctx, `DELETE FROM operations_fts WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("catalog: knowledge refresh: delete operations_fts batch: %w", err)
	}

	for _, id := range batch {
		r, ok := existing[id]
		if !ok {
			continue
		}
		docText, err := docTextForOperation(ctx, tx, id)
		if err != nil {
			return err
		}
		memText, err := memoryTextForOperation(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO operations_fts (id, service, op_id, path_tokens, summary, description, tags, param_names, field_names, doc_text, memory_text)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, r.id, r.service, r.opID, r.pathTokens, r.summary, r.description, r.tags, r.paramNames, r.fieldNames, docText, memText); err != nil {
			return fmt.Errorf("catalog: knowledge refresh: reinsert operations_fts %q: %w", id, err)
		}
	}
	return nil
}

// placeholdersFor returns a "?,?,...,?" placeholder list and the matching
// args slice for ids, for use in an "IN (...)" clause.
func placeholdersFor(ids []string) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

// docTextForOperation builds opID's doc_text: heading + first
// docTextBodyChars characters of body, for every doc section whose
// doc_refs reference opID (kind "operation"), ordered by doc then section
// order for determinism, joined with a blank line and capped at
// knowledgeColumnCapBytes.
func docTextForOperation(ctx context.Context, q querier, opID string) (string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT COALESCE(ds.heading, ''), ds.body
		FROM doc_refs r
		JOIN doc_sections ds ON ds.id = r.section_id
		WHERE r.kind = 'operation' AND r.value = ?
		ORDER BY ds.doc_id, ds.ord
	`, opID)
	if err != nil {
		return "", fmt.Errorf("catalog: query doc sections for %q: %w", opID, err)
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var heading, body string
		if err := rows.Scan(&heading, &body); err != nil {
			return "", fmt.Errorf("catalog: scan doc section for %q: %w", opID, err)
		}
		body = truncateRunes(body, docTextBodyChars)
		switch {
		case heading != "" && body != "":
			parts = append(parts, heading+"\n"+body)
		case heading != "":
			parts = append(parts, heading)
		default:
			parts = append(parts, body)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("catalog: iterate doc sections for %q: %w", opID, err)
	}
	return capBytes(joinNonEmpty(parts, "\n\n"), knowledgeColumnCapBytes), nil
}

// memoryTextForOperation builds opID's memory_text: the first
// memoryTextBodyChars characters of every active memory whose subject
// names opID directly, or whose subject field belongs to opID (a
// memory_subjects "field" row keyed "<operation>#<field-path>" — see
// internal/memory's subjectRows), most recently updated first, joined with
// a blank line and capped at knowledgeColumnCapBytes.
func memoryTextForOperation(ctx context.Context, q querier, opID string) (string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT DISTINCT m.id, m.body, m.updated
		FROM memories m
		JOIN memory_subjects ms ON ms.memory_id = m.id
		WHERE m.status = 'active' AND (
			(ms.kind = 'operation' AND ms.value = ?)
			OR (ms.kind = 'field' AND ms.value LIKE ? || '#%')
		)
		ORDER BY m.updated DESC, m.id
	`, opID, opID)
	if err != nil {
		return "", fmt.Errorf("catalog: query memories for %q: %w", opID, err)
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var id, body, updated string
		if err := rows.Scan(&id, &body, &updated); err != nil {
			return "", fmt.Errorf("catalog: scan memory for %q: %w", opID, err)
		}
		parts = append(parts, truncateRunes(body, memoryTextBodyChars))
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("catalog: iterate memories for %q: %w", opID, err)
	}
	return capBytes(joinNonEmpty(parts, "\n\n"), knowledgeColumnCapBytes), nil
}

// Reindex rebuilds operations_fts and operations_trigram for every
// operation in the catalog, entirely from data already stored in this
// workspace's database (operations.doc_json, fields, doc_refs/
// doc_sections, memories/memory_subjects) — no source contract or
// registry access is needed or performed. Used by New to repair
// operations_fts after migration 006 dropped and recreated it, and
// available as a general "rebuild the search index from what's on disk"
// operation.
func (c *Catalog) Reindex(ctx context.Context) error {
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM operations_fts`); err != nil {
			return fmt.Errorf("catalog: reindex: clear operations_fts: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM operations_trigram`); err != nil {
			return fmt.Errorf("catalog: reindex: clear operations_trigram: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks_fts`); err != nil {
			return fmt.Errorf("catalog: reindex: clear tasks_fts: %w", err)
		}

		rows, err := tx.QueryContext(ctx, `
			SELECT o.id, o.doc_json, sv.name
			FROM operations o
			JOIN services sv ON sv.id = o.service_id
		`)
		if err != nil {
			return fmt.Errorf("catalog: reindex: list operations: %w", err)
		}
		type opRow struct {
			id, docJSON, serviceName string
		}
		var opRows []opRow
		for rows.Next() {
			var r opRow
			if err := rows.Scan(&r.id, &r.docJSON, &r.serviceName); err != nil {
				rows.Close()
				return fmt.Errorf("catalog: reindex: scan operation: %w", err)
			}
			opRows = append(opRows, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("catalog: reindex: iterate operations: %w", err)
		}
		rows.Close()

		ids := make([]string, 0, len(opRows))
		for _, r := range opRows {
			var op domain.Operation
			if err := store.UnmarshalJSON(r.docJSON, &op); err != nil {
				return fmt.Errorf("catalog: reindex: unmarshal operation %q: %w", r.id, err)
			}
			fieldRows, err := tx.QueryContext(ctx, `SELECT doc_json FROM fields WHERE operation_id = ? ORDER BY field_path`, r.id)
			if err != nil {
				return fmt.Errorf("catalog: reindex: query fields for %q: %w", r.id, err)
			}
			fields, err := scanFields(fieldRows)
			fieldRows.Close()
			if err != nil {
				return fmt.Errorf("catalog: reindex: scan fields for %q: %w", r.id, err)
			}

			ftsRow := buildOperationsFTSRow(r.serviceName, op, fields)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO operations_fts (id, service, op_id, path_tokens, summary, description, tags, param_names, field_names, doc_text, memory_text)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')
			`, ftsRow.ID, ftsRow.Service, ftsRow.OpID, ftsRow.PathTokens, ftsRow.Summary, ftsRow.Description, ftsRow.Tags, ftsRow.ParamNames, ftsRow.FieldNames); err != nil {
				return fmt.Errorf("catalog: reindex: insert operations_fts %q: %w", r.id, err)
			}

			trigramRow := buildOperationsTrigramRow(op)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO operations_trigram (id, op_id, path) VALUES (?, ?, ?)`,
				trigramRow.ID, trigramRow.OpID, trigramRow.Path,
			); err != nil {
				return fmt.Errorf("catalog: reindex: insert operations_trigram %q: %w", r.id, err)
			}

			ids = append(ids, r.id)
		}

		taskRows, err := tx.QueryContext(ctx, `SELECT t.id, t.doc_json, sv.name FROM tasks t JOIN services sv ON sv.id = t.service_id`)
		if err != nil {
			return fmt.Errorf("catalog: reindex: list tasks: %w", err)
		}
		for taskRows.Next() {
			var id, docJSON, serviceName string
			if err := taskRows.Scan(&id, &docJSON, &serviceName); err != nil {
				taskRows.Close()
				return fmt.Errorf("catalog: reindex: scan task: %w", err)
			}
			var task domain.Task
			if err := store.UnmarshalJSON(docJSON, &task); err != nil {
				taskRows.Close()
				return fmt.Errorf("catalog: reindex: unmarshal task %q: %w", id, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO tasks_fts (task_id, service, phrases) VALUES (?, ?, ?)`, id, serviceName, strings.Join(task.Phrases, " ")); err != nil {
				taskRows.Close()
				return fmt.Errorf("catalog: reindex: insert tasks_fts %q: %w", id, err)
			}
		}
		if err := taskRows.Err(); err != nil {
			taskRows.Close()
			return fmt.Errorf("catalog: reindex: iterate tasks: %w", err)
		}
		taskRows.Close()

		return c.refreshOperationKnowledgeTx(ctx, tx, ids)
	})
}

// truncateRunes returns s truncated to at most n runes (the exact rune
// count, not bytes, matching the task spec's "first N characters").
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// capBytes returns s truncated to at most maxBytes bytes, backing off to
// the nearest earlier rune boundary so the result is always valid UTF-8.
func capBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := s[:maxBytes]
	for len(b) > 0 && !utf8.ValidString(b) {
		b = b[:len(b)-1]
	}
	return b
}

// joinNonEmpty joins parts (skipping any empty string) with sep.
func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// queryIDsVia runs query (which must SELECT exactly one text column)
// through q (a *sql.Tx or db.SQL()) and returns the matched values in row
// order. Distinct from *Catalog.queryIDs in operations.go, which always
// reads through c.db.SQL() and can't be used inside an existing
// transaction.
func queryIDsVia(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: query ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("catalog: scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate ids: %w", err)
	}
	return ids, nil
}
