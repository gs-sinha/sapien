// Package catalog persists the normalized API model (PLAN.md §5, §15) into the
// workspace SQLite database and serves the read queries behind the engine's
// CatalogAPI: services, operations, schemas, fields, docs, and flows. It also
// maintains the FTS5 shadow tables the search package (internal/search,
// built in parallel) queries: operations_fts, operations_trigram, and
// docs_fts.
//
// # FTS column contract (PLAN.md §16)
//
// Catalog.Apply is the only writer of these three tables; every column below
// is produced with internal/textutil so the search package's tokenization of
// a user query matches the tokenization of the indexed text exactly.
//
//   - operations_fts (one row per operation):
//     id           = Operation.ID (UNINDEXED)
//     service      = the owning Service.Name
//     op_id        = textutil.Join(append(textutil.SplitIdent(op.ID), op.ID, op.RawOpID))
//     path_tokens  = textutil.Join(append(textutil.PathTokens(path), path)) where path is
//     op.HTTP.Path ("" if op.HTTP is nil)
//     summary      = op.Summary, verbatim
//     description  = op.Description, verbatim
//     tags         = textutil.Join(textutil.Tokens(strings.Join(append(op.Tags, op.Concepts...), " ")))
//     param_names  = textutil.Join(textutil.Tokens(strings.Join(param names, " ")))
//     field_names  = textutil.Join(textutil.Tokens(strings.Join(leaf field names, " "))),
//     where a leaf field name is a Field.Path's last "."-segment with any
//     trailing "[]" stripped (e.g. "response.200.body.items[].riderId" -> "riderId")
//     doc_text     = every doc section referencing this operation (doc_refs, kind
//     "operation"), each rendered as its heading plus the first 300 characters of its
//     body, joined and capped at 2 KB (knowledge.go, written by Apply right after it
//     replaces a service's docs)
//     memory_text  = the first 300 characters of every active memory whose subject
//     names this operation, or whose subject field belongs to it, joined and capped at
//     2 KB (knowledge.go, written by RefreshOperationKnowledge — see engine/local's
//     memory-write hooks)
//
//   - operations_trigram (one row per operation): id = op.ID (UNINDEXED), op_id =
//     strings.ToLower(op.ID), path = strings.ToLower(op.HTTP.Path) ("" if nil).
//
//   - docs_fts (one row per doc section): section_id = DocSection.ID (UNINDEXED), service =
//     the owning Service.Name, title = the parent Doc.Title, heading = DocSection.Heading,
//     body = DocSection.Body.
//
// Rows are deleted by id/section_id before being re-inserted on any change, and deleted
// outright when the underlying operation/doc/service disappears — FTS5 tables are not kept in
// sync by SQLite itself (see 001_init.sql's header comment).
package catalog

import (
	"context"
	"log/slog"

	"github.com/gs-sinha/sapien/internal/store"
)

// Catalog reads and writes the normalized catalog tables described above and
// in PLAN.md §15 (schemas, fields, docs, flows) via db.
type Catalog struct {
	db *store.DB
}

// knowledgeSchemaVersion identifies the shape of operations_fts' doc_text/
// memory_text columns. Bump it whenever that shape changes (a new column,
// a different truncation rule, ...) so New's one-time Reindex (below) runs
// again on every existing workspace after the upgrade ships.
const knowledgeSchemaVersion = "1"

// knowledgeVersionSettingKey is the `settings` row New checks/updates.
const knowledgeVersionSettingKey = "catalog_knowledge_schema_version"

// New returns a Catalog backed by db. db must already be open and migrated
// (store.Open does this by default).
//
// New also guards migration 006's operations_fts drop-and-recreate (FTS5
// cannot ALTER TABLE ADD COLUMN a virtual table): it compares
// knowledgeVersionSettingKey against knowledgeSchemaVersion and, the first
// time they don't match (a brand new workspace, or an existing one just
// upgraded past migration 006), runs a full Reindex — rebuilding
// operations_fts/operations_trigram straight from the operations/fields/
// doc_refs/doc_sections/memories tables already on disk, no source or
// registry access needed — before recording the new version. This is the
// only hook point available to this package for "existing workspaces
// reindex once": internal/engine/local's Open/staleCheck (which decide
// whether to resync a service from its actual contract) belong to a
// different task's file ownership. A failed Reindex here is logged and
// left unrecorded, so the next Open retries it rather than silently
// leaving operations_fts empty.
func New(db *store.DB) *Catalog {
	c := &Catalog{db: db}
	c.ensureKnowledgeSchema(context.Background())
	return c
}

func (c *Catalog) ensureKnowledgeSchema(ctx context.Context) {
	var current string
	err := c.db.SQL().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, knowledgeVersionSettingKey).Scan(&current)
	if err == nil && current == knowledgeSchemaVersion {
		return
	}
	if err := c.Reindex(ctx); err != nil {
		slog.Default().Warn("catalog: full reindex after knowledge schema upgrade failed; will retry on next open", "error", err)
		return
	}
	if _, err := c.db.SQL().ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, knowledgeVersionSettingKey, knowledgeSchemaVersion); err != nil {
		slog.Default().Warn("catalog: recording knowledge schema version failed", "error", err)
	}
}

// Stats summarizes the size of the catalog.
type Stats struct {
	Services   int `json:"services"`
	Operations int `json:"operations"`
	Tasks      int `json:"tasks"`
	Fields     int `json:"fields"`
	Docs       int `json:"docs"`
	Sections   int `json:"sections"`
	Flows      int `json:"flows"`
}
