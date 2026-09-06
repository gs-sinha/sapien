-- 006_search_knowledge.sql — fold docs and memories into the operations
-- search index, plus persisted usage-feedback learning (search ranking
-- tuning task, part 2 of PLAN.md §16).
--
-- FTS5 cannot ALTER TABLE ADD COLUMN a virtual table, so operations_fts is
-- dropped and recreated with two extra columns, doc_text and memory_text,
-- appended after field_names. bm25 column order (and the new weight
-- vector, task spec): id(0, UNINDEXED, weight ignored), service(1),
-- op_id(8), path_tokens(6), summary(5), description(1), tags(3),
-- param_names(3), field_names(3), doc_text(2), memory_text(2) — the exact
-- call is bm25(operations_fts, 0, 1, 8, 6, 5, 1, 3, 3, 3, 2, 2)
-- (internal/search/query.go).
--
-- doc_text is, per operation, the heading + first 300 characters of body
-- for every doc section whose doc_refs reference that operation (kind =
-- 'operation'), joined and capped at 2 KB; memory_text is the first 300
-- characters of every active memory whose subject names that operation (or
-- whose subject field belongs to it), capped at 2 KB. Both are written by
-- internal/catalog.RefreshOperationKnowledge, called from Apply (right
-- after it replaces a service's docs) and from internal/engine/local's
-- memory-write hooks.
--
-- Dropping operations_fts here empties it for every existing workspace
-- until it is repopulated. internal/catalog.Catalog.New guards against a
-- silently-empty index after this upgrade: it checks a `settings` row
-- ("catalog_knowledge_schema_version") on every Open and, the first time it
-- doesn't match, runs a full Catalog.Reindex — a from-stored-data rebuild
-- of operations_fts/operations_trigram (operations, fields, doc_refs,
-- doc_sections, and memories/memory_subjects are all still intact; only
-- the FTS5 shadow table was dropped), no source/registry access required.
DROP TABLE IF EXISTS operations_fts;

CREATE VIRTUAL TABLE operations_fts USING fts5(
    id UNINDEXED,
    service,
    op_id,
    path_tokens,
    summary,
    description,
    tags,
    param_names,
    field_names,
    doc_text,
    memory_text,
    tokenize = 'unicode61 remove_diacritics 2'
);

-- search_feedback records, per (query token, operation), how many times a
-- recent search whose tokens included that term was followed (within
-- internal/engine/local's 10-minute lookback window) by that operation
-- actually being used: Catalog().GetOperation/ResolveOperation, an
-- operation referenced by a saved/updated flow, or Runner().Call.
-- internal/search adds a small, capped boost from this table on top of
-- lexical scoring (internal/search/feedback.go) and labels the result
-- "feedback" in matched_on. Rows persist across processes (unlike the
-- in-memory search ring that feeds them), so the boost a search earns in
-- one process is visible to every other process/Local opened against the
-- same workspace database.
CREATE TABLE search_feedback (
    term    TEXT NOT NULL,
    op_id   TEXT NOT NULL,
    count   INTEGER NOT NULL,
    updated TEXT NOT NULL,
    PRIMARY KEY (term, op_id)
);

CREATE INDEX idx_search_feedback_op_id ON search_feedback(op_id);
