package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
)

// Apply upserts snap.Service and replaces its catalog contents in one
// transaction. Operations are diffed by (id, hash): unchanged rows are left
// untouched, changed rows are rewritten (operations, operation_aliases,
// fields, and the two operations_* FTS tables), and vanished rows are
// deleted (including their FTS rows). Schemas, docs, and the service's own
// flows are replaced wholesale (they are small; a diff isn't worth it).
//
// Apply always marks the service "ok": status, last_indexed, operation_count
// and doc_json are set from snap regardless of what snap.Service.Status
// carried in. Use MarkServiceError to record a failed ingest instead.
func (c *Catalog) Apply(ctx context.Context, snap Snapshot) (domain.CatalogChange, error) {
	now := time.Now().UTC()

	svc := snap.Service
	svc.Status = domain.SyncOK
	svc.Error = ""
	svc.LastIndexed = now
	svc.OperationCount = len(snap.Operations)
	if snap.ContractFiles != nil {
		paths := make([]string, 0, len(snap.ContractFiles))
		for p := range snap.ContractFiles {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		svc.ContractFiles = paths
	}

	change := domain.CatalogChange{Service: svc.ID}

	err := c.db.Write(ctx, func(tx *sql.Tx) error {
		if err := upsertServiceTx(ctx, tx, svc); err != nil {
			return err
		}

		added, removed, changed, err := applyOperationsTx(ctx, tx, svc, snap)
		if err != nil {
			return err
		}
		change.Added, change.Removed, change.Changed = added, removed, changed

		if err := replaceSchemasTx(ctx, tx, svc.ID, snap.Schemas); err != nil {
			return err
		}
		if err := replaceDocsTx(ctx, tx, svc.ID, snap.Docs, now); err != nil {
			return err
		}
		if err := upsertFlowsTx(ctx, tx, "service", svc.ID, snap.Flows, now); err != nil {
			return err
		}
		if err := replaceContractFilesTx(ctx, tx, svc.ID, snap.ContractFiles, now); err != nil {
			return err
		}

		// Refresh doc_text (and memory_text, a cheap bonus from whatever
		// memories already exist in this same database) for every operation
		// still standing for this service — not just added/changed ones,
		// since a service's docs are replaced wholesale above and a doc
		// section can start or stop referencing an operation whose own
		// hash never changed.
		opIDs, err := queryIDsVia(ctx, tx, `SELECT id FROM operations WHERE service_id = ?`, svc.ID)
		if err != nil {
			return fmt.Errorf("catalog: list operations for knowledge refresh: %w", err)
		}
		if err := c.refreshOperationKnowledgeTx(ctx, tx, opIDs); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return domain.CatalogChange{}, err
	}
	return change, nil
}

func upsertServiceTx(ctx context.Context, tx *sql.Tx, svc domain.Service) error {
	sourceJSON, err := store.MarshalJSON(svc.Source)
	if err != nil {
		return fmt.Errorf("catalog: marshal service source: %w", err)
	}
	docJSON, err := store.MarshalJSON(svc)
	if err != nil {
		return fmt.Errorf("catalog: marshal service: %w", err)
	}
	lastIndexed := ""
	if !svc.LastIndexed.IsZero() {
		lastIndexed = svc.LastIndexed.Format(time.RFC3339)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO services (id, name, source_json, status, last_indexed, error_report, doc_json, operation_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			source_json = excluded.source_json,
			status = excluded.status,
			last_indexed = excluded.last_indexed,
			error_report = excluded.error_report,
			doc_json = excluded.doc_json,
			operation_count = excluded.operation_count
	`, svc.ID, svc.Name, sourceJSON, string(svc.Status), lastIndexed, svc.Error, docJSON, svc.OperationCount)
	if err != nil {
		return fmt.Errorf("catalog: upsert service %q: %w", svc.ID, err)
	}
	return nil
}

// applyOperationsTx diffs snap.Operations against what is stored for
// svc.ID by (id, hash) and rewrites only what changed. It returns the added,
// removed, and changed operation IDs, each sorted.
func applyOperationsTx(ctx context.Context, tx *sql.Tx, svc domain.Service, snap Snapshot) (added, removed, changed []string, err error) {
	existing := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT id, hash FROM operations WHERE service_id = ?`, svc.ID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("catalog: query existing operations: %w", err)
	}
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			rows.Close()
			return nil, nil, nil, fmt.Errorf("catalog: scan existing operation: %w", err)
		}
		existing[id] = hash
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, nil, fmt.Errorf("catalog: iterate existing operations: %w", err)
	}
	rows.Close()

	fieldsByOp := map[string][]domain.Field{}
	for _, f := range snap.Fields {
		fieldsByOp[f.OperationID] = append(fieldsByOp[f.OperationID], f)
	}
	aliasesByOp := map[string][]domain.Alias{}
	for _, a := range snap.Aliases {
		aliasesByOp[a.OperationID] = append(aliasesByOp[a.OperationID], a)
	}

	newIDs := make(map[string]bool, len(snap.Operations))
	dirty := make(map[string]bool, len(snap.Operations))
	for _, op := range snap.Operations {
		newIDs[op.ID] = true
		oldHash, ok := existing[op.ID]
		switch {
		case !ok:
			added = append(added, op.ID)
			dirty[op.ID] = true
		case oldHash != op.Hash:
			changed = append(changed, op.ID)
			dirty[op.ID] = true
		}
	}
	for id := range existing {
		if !newIDs[id] {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)

	for _, id := range removed {
		if err := deleteOperationTx(ctx, tx, id); err != nil {
			return nil, nil, nil, err
		}
	}

	for _, op := range snap.Operations {
		if !dirty[op.ID] {
			continue
		}
		// Delete any prior row for this id (no-op for "added") plus its FTS
		// rows, which are never touched by SQLite's ON DELETE CASCADE.
		if err := deleteOperationTx(ctx, tx, op.ID); err != nil {
			return nil, nil, nil, err
		}
		fields := fieldsByOp[op.ID]
		if err := insertOperationTx(ctx, tx, svc, op, fields, aliasesByOp[op.ID]); err != nil {
			return nil, nil, nil, err
		}
	}

	return added, removed, changed, nil
}

func deleteOperationTx(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM operations WHERE id = ?`, id); err != nil {
		return fmt.Errorf("catalog: delete operation %q: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operations_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("catalog: delete operations_fts %q: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operations_trigram WHERE id = ?`, id); err != nil {
		return fmt.Errorf("catalog: delete operations_trigram %q: %w", id, err)
	}
	return nil
}

// bareOperationID returns op's "operationId"-equivalent: RawOpID if the
// contract had one, else the suffix of op.ID after "<serviceID>.".
func bareOperationID(serviceID string, op domain.Operation) string {
	if op.RawOpID != "" {
		return op.RawOpID
	}
	return strings.TrimPrefix(op.ID, serviceID+".")
}

func insertOperationTx(ctx context.Context, tx *sql.Tx, svc domain.Service, op domain.Operation, fields []domain.Field, aliases []domain.Alias) error {
	docJSON, err := store.MarshalJSON(op)
	if err != nil {
		return fmt.Errorf("catalog: marshal operation %q: %w", op.ID, err)
	}
	tagsJSON, err := store.MarshalJSON(op.Tags)
	if err != nil {
		return fmt.Errorf("catalog: marshal operation tags %q: %w", op.ID, err)
	}
	rawOpIDLC := strings.ToLower(bareOperationID(svc.ID, op))

	_, err = tx.ExecContext(ctx, `
		INSERT INTO operations
			(id, service_id, protocol, method, path, raw_op_id, summary, description,
			 tags_json, deprecated, source_file, source_pointer, source_line, hash, doc_json, raw_op_id_lc)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, op.ID, svc.ID, string(op.Protocol), httpMethod(op), httpPath(op), op.RawOpID, op.Summary, op.Description,
		tagsJSON, boolToInt(op.Deprecated), op.Source.File, op.Source.Pointer, op.Source.Line, op.Hash, docJSON, rawOpIDLC)
	if err != nil {
		return fmt.Errorf("catalog: insert operation %q: %w", op.ID, err)
	}

	for _, a := range aliases {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO operation_aliases (method, path, operation_id) VALUES (?, ?, ?)`,
			strings.ToUpper(a.Method), a.Path, op.ID,
		); err != nil {
			return fmt.Errorf("catalog: insert alias for %q: %w", op.ID, err)
		}
	}

	for _, f := range fields {
		fieldJSON, err := store.MarshalJSON(f)
		if err != nil {
			return fmt.Errorf("catalog: marshal field %q/%q: %w", op.ID, f.Path, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO fields (operation_id, field_path, type, description, doc_json, leaf_name) VALUES (?, ?, ?, ?, ?, ?)`,
			op.ID, f.Path, f.Type, f.Description, fieldJSON, strings.ToLower(leafName(f.Path)),
		); err != nil {
			return fmt.Errorf("catalog: insert field %q/%q: %w", op.ID, f.Path, err)
		}
	}

	// doc_text/memory_text start empty here and are filled in by
	// refreshOperationKnowledgeTx below, once this service's docs have been
	// (re)written in the same transaction — see Apply.
	ftsRow := buildOperationsFTSRow(svc.Name, op, fields)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO operations_fts (id, service, op_id, path_tokens, summary, description, tags, param_names, field_names, doc_text, memory_text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')
	`, ftsRow.ID, ftsRow.Service, ftsRow.OpID, ftsRow.PathTokens, ftsRow.Summary, ftsRow.Description, ftsRow.Tags, ftsRow.ParamNames, ftsRow.FieldNames); err != nil {
		return fmt.Errorf("catalog: insert operations_fts %q: %w", op.ID, err)
	}

	trigramRow := buildOperationsTrigramRow(op)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO operations_trigram (id, op_id, path) VALUES (?, ?, ?)`,
		trigramRow.ID, trigramRow.OpID, trigramRow.Path,
	); err != nil {
		return fmt.Errorf("catalog: insert operations_trigram %q: %w", op.ID, err)
	}

	return nil
}

func replaceSchemasTx(ctx context.Context, tx *sql.Tx, serviceID string, schemas []domain.NamedSchema) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM schemas WHERE service_id = ?`, serviceID); err != nil {
		return fmt.Errorf("catalog: delete schemas for %q: %w", serviceID, err)
	}
	for _, s := range schemas {
		docJSON, err := store.MarshalJSON(s)
		if err != nil {
			return fmt.Errorf("catalog: marshal schema %q: %w", s.Name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schemas (service_id, name, hash, doc_json) VALUES (?, ?, ?, ?)`,
			serviceID, s.Name, s.Hash, docJSON,
		); err != nil {
			return fmt.Errorf("catalog: insert schema %q: %w", s.Name, err)
		}
	}
	return nil
}

func replaceDocsTx(ctx context.Context, tx *sql.Tx, serviceID string, docs []domain.Doc, now time.Time) error {
	// docs_fts is not FK-linked to doc_sections, so its rows must be deleted
	// explicitly before the cascade removes the sections themselves.
	rows, err := tx.QueryContext(ctx, `
		SELECT ds.id FROM doc_sections ds JOIN docs d ON d.id = ds.doc_id WHERE d.service_id = ?
	`, serviceID)
	if err != nil {
		return fmt.Errorf("catalog: query existing doc sections for %q: %w", serviceID, err)
	}
	var oldSectionIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("catalog: scan existing doc section: %w", err)
		}
		oldSectionIDs = append(oldSectionIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("catalog: iterate existing doc sections: %w", err)
	}
	rows.Close()

	for _, id := range oldSectionIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM docs_fts WHERE section_id = ?`, id); err != nil {
			return fmt.Errorf("catalog: delete docs_fts %q: %w", id, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM docs WHERE service_id = ?`, serviceID); err != nil {
		return fmt.Errorf("catalog: delete docs for %q: %w", serviceID, err)
	}

	updated := now.Format(time.RFC3339)
	for _, d := range docs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO docs (id, service_id, path, title, source, hash, updated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			d.ID, serviceID, d.Path, d.Title, string(d.Source), d.Hash, updated,
		); err != nil {
			return fmt.Errorf("catalog: insert doc %q: %w", d.ID, err)
		}
		for _, sec := range d.Sections {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO doc_sections (id, doc_id, ord, heading, level, body) VALUES (?, ?, ?, ?, ?, ?)`,
				sec.ID, d.ID, sec.Ord, sec.Heading, sec.Level, sec.Body,
			); err != nil {
				return fmt.Errorf("catalog: insert doc section %q: %w", sec.ID, err)
			}
			for _, ref := range sec.Refs {
				if _, err := tx.ExecContext(ctx,
					`INSERT OR IGNORE INTO doc_refs (section_id, kind, value) VALUES (?, ?, ?)`,
					sec.ID, string(ref.Kind), ref.Value,
				); err != nil {
					return fmt.Errorf("catalog: insert doc ref %q: %w", sec.ID, err)
				}
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO docs_fts (section_id, service, title, heading, body) VALUES (?, ?, ?, ?, ?)`,
				sec.ID, serviceID, d.Title, sec.Heading, sec.Body,
			); err != nil {
				return fmt.Errorf("catalog: insert docs_fts %q: %w", sec.ID, err)
			}
		}
	}
	return nil
}

func replaceContractFilesTx(ctx context.Context, tx *sql.Tx, serviceID string, files map[string]string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM contract_files WHERE service_id = ?`, serviceID); err != nil {
		return fmt.Errorf("catalog: delete contract_files for %q: %w", serviceID, err)
	}
	lastParsed := now.Format(time.RFC3339)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO contract_files (service_id, path, hash, last_parsed) VALUES (?, ?, ?, ?)`,
			serviceID, p, files[p], lastParsed,
		); err != nil {
			return fmt.Errorf("catalog: insert contract_file %q: %w", p, err)
		}
	}
	return nil
}

// MarkServiceError records a failed ingest for svc: the last good catalog
// (operations, fields, schemas, docs, flows) is left untouched; only the
// service row's status and error_report change. Creates the service row
// (with an otherwise-empty catalog) if it doesn't exist yet.
func (c *Catalog) MarkServiceError(ctx context.Context, svc domain.Service, msg string) error {
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		var existingJSON string
		err := tx.QueryRowContext(ctx, `SELECT doc_json FROM services WHERE id = ?`, svc.ID).Scan(&existingJSON)
		switch {
		case err == nil:
			var existing domain.Service
			if uerr := store.UnmarshalJSON(existingJSON, &existing); uerr == nil && existing.ID != "" {
				svc = existing
			}
		case errors.Is(err, sql.ErrNoRows):
			// First-ever attempt for this service: fall through and insert svc as given.
		default:
			return fmt.Errorf("catalog: query service %q: %w", svc.ID, err)
		}

		svc.Status = domain.SyncError
		svc.Error = msg

		return upsertServiceTx(ctx, tx, svc)
	})
}

// RemoveService deletes svc and every catalog row that belongs to it,
// including rows SQLite's ON DELETE CASCADE does not reach: the FTS shadow
// tables and the service's owned flows (flows.owner_id is not a real foreign
// key, since a flow's owner may be "workspace" or "service").
func (c *Catalog) RemoveService(ctx context.Context, id string) error {
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		opRows, err := tx.QueryContext(ctx, `SELECT id FROM operations WHERE service_id = ?`, id)
		if err != nil {
			return fmt.Errorf("catalog: query operations for %q: %w", id, err)
		}
		var opIDs []string
		for opRows.Next() {
			var opID string
			if err := opRows.Scan(&opID); err != nil {
				opRows.Close()
				return fmt.Errorf("catalog: scan operation id: %w", err)
			}
			opIDs = append(opIDs, opID)
		}
		if err := opRows.Err(); err != nil {
			opRows.Close()
			return err
		}
		opRows.Close()

		secRows, err := tx.QueryContext(ctx, `
			SELECT ds.id FROM doc_sections ds JOIN docs d ON d.id = ds.doc_id WHERE d.service_id = ?
		`, id)
		if err != nil {
			return fmt.Errorf("catalog: query doc sections for %q: %w", id, err)
		}
		var sectionIDs []string
		for secRows.Next() {
			var secID string
			if err := secRows.Scan(&secID); err != nil {
				secRows.Close()
				return fmt.Errorf("catalog: scan doc section id: %w", err)
			}
			sectionIDs = append(sectionIDs, secID)
		}
		if err := secRows.Err(); err != nil {
			secRows.Close()
			return err
		}
		secRows.Close()

		for _, opID := range opIDs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM operations_fts WHERE id = ?`, opID); err != nil {
				return fmt.Errorf("catalog: delete operations_fts %q: %w", opID, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM operations_trigram WHERE id = ?`, opID); err != nil {
				return fmt.Errorf("catalog: delete operations_trigram %q: %w", opID, err)
			}
		}
		for _, secID := range sectionIDs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM docs_fts WHERE section_id = ?`, secID); err != nil {
				return fmt.Errorf("catalog: delete docs_fts %q: %w", secID, err)
			}
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM flows WHERE owner_kind = 'service' AND owner_id = ?`, id); err != nil {
			return fmt.Errorf("catalog: delete flows for %q: %w", id, err)
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM services WHERE id = ?`, id); err != nil {
			return fmt.Errorf("catalog: delete service %q: %w", id, err)
		}
		return nil
	})
}
