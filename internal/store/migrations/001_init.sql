-- 001_init.sql — initial persistence schema (PLAN.md §15).
--
-- Conventions:
--   * All timestamps are TEXT in RFC3339 (UTC), e.g. "2026-09-05T12:00:00Z".
--   * All *_json columns are TEXT holding serialized JSON (see store.MarshalJSON /
--     store.UnmarshalJSON). SQLite has no native JSON type; keeping it as TEXT keeps the
--     column portable and lets callers use the json1 functions on it if ever needed.
--   * Booleans are INTEGER 0/1 (SQLite has no BOOLEAN type).
--   * Primary keys that are "id" columns are TEXT holding store.NewID()-style
--     "<prefix>_<ULID>" values, except schema_migrations.version and environments.name
--     which are natural keys, and join/edge tables which use composite PKs.
--   * A few tables carry columns beyond the abbreviated PLAN §15 sketch (e.g. runs.pinned,
--     runs.trigger, runs.error_json, runs.operation_hashes_json, run_steps.operation,
--     run_steps.attempts, run_steps.started/finished) so the schema can round-trip the full
--     internal/domain.Run / domain.StepResult structs. Every column PLAN §15 names is present
--     under the same name; nothing there was renamed or dropped.
--
-- FTS5 maintenance strategy:
--   operations_fts, docs_fts, and memories_fts are PLAIN fts5 tables — NOT "external content"
--   tables. They are not kept in sync automatically by SQLite triggers; the catalog/docs/memory
--   indexers in other packages own their lifecycle explicitly:
--     * On insert of the source row: INSERT INTO <x>_fts (id/section_id, ...) VALUES (...).
--     * On update: DELETE FROM <x>_fts WHERE id = ? (or section_id = ?), then re-INSERT.
--     * On delete: DELETE FROM <x>_fts WHERE id = ? (or section_id = ?).
--   The id/section_id column is declared UNINDEXED, so it is stored but not tokenized; deleting
--   by it performs a linear scan of the shadow tables rather than an index seek (fts5 virtual
--   tables cannot carry a secondary index on an UNINDEXED column). This is acceptable because
--   these deletes happen only during batch reindex operations, not on the hot query path.
--   operations_trigram is a separate fts5 table using the 'trigram' tokenizer, maintained the
--   same way, used only for substring/fuzzy hits on op_id/path (bm25 ranking is meaningless for
--   trigram matches, so callers treat it purely as a filter/candidate source).

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL
);

CREATE TABLE services (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    source_json  TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT '',
    last_indexed TEXT,
    error_report TEXT
);

CREATE TABLE contract_files (
    service_id  TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    path        TEXT NOT NULL,
    hash        TEXT NOT NULL DEFAULT '',
    last_parsed TEXT,
    PRIMARY KEY (service_id, path)
);

CREATE TABLE operations (
    id             TEXT PRIMARY KEY,
    service_id     TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    protocol       TEXT NOT NULL DEFAULT 'http',
    method         TEXT NOT NULL DEFAULT '',
    path           TEXT NOT NULL DEFAULT '',
    raw_op_id      TEXT,
    summary        TEXT,
    description    TEXT,
    tags_json      TEXT NOT NULL DEFAULT '[]',
    deprecated     INTEGER NOT NULL DEFAULT 0,
    source_file    TEXT,
    source_pointer TEXT,
    source_line    INTEGER,
    hash           TEXT NOT NULL DEFAULT '',
    doc_json       TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_operations_service_id ON operations(service_id);
CREATE INDEX idx_operations_method_path ON operations(method, path);

CREATE TABLE operation_aliases (
    method       TEXT NOT NULL,
    path         TEXT NOT NULL,
    operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    PRIMARY KEY (method, path, operation_id)
);

CREATE INDEX idx_operation_aliases_method_path ON operation_aliases(method, path);

CREATE TABLE schemas (
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    hash       TEXT NOT NULL DEFAULT '',
    doc_json   TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (service_id, name)
);

CREATE TABLE fields (
    operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    field_path   TEXT NOT NULL,
    type         TEXT NOT NULL DEFAULT '',
    description  TEXT,
    PRIMARY KEY (operation_id, field_path)
);

CREATE INDEX idx_fields_operation_id ON fields(operation_id);

-- bm25 column weights are applied by callers at query time via the `operations_fts(operations_fts)`
-- auxiliary rank function or an explicit bm25(operations_fts, w0, w1, ...) call; weights per
-- PLAN §16: op_id 8, path 6, summary 5, tags 3, param/field names 3, description 1.
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
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE VIRTUAL TABLE operations_trigram USING fts5(
    id UNINDEXED,
    op_id,
    path,
    tokenize = 'trigram'
);

CREATE TABLE docs (
    id         TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    path       TEXT NOT NULL DEFAULT '',
    title      TEXT,
    source     TEXT NOT NULL DEFAULT 'file',
    hash       TEXT NOT NULL DEFAULT '',
    updated    TEXT
);

CREATE TABLE doc_sections (
    id      TEXT PRIMARY KEY,
    doc_id  TEXT NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    ord     INTEGER NOT NULL DEFAULT 0,
    heading TEXT,
    level   INTEGER NOT NULL DEFAULT 0,
    body    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_doc_sections_doc_id ON doc_sections(doc_id);

CREATE TABLE doc_refs (
    section_id TEXT NOT NULL REFERENCES doc_sections(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    value      TEXT NOT NULL,
    PRIMARY KEY (section_id, kind, value)
);

CREATE INDEX idx_doc_refs_kind_value ON doc_refs(kind, value);

CREATE VIRTUAL TABLE docs_fts USING fts5(
    section_id UNINDEXED,
    service,
    title,
    heading,
    body,
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TABLE flows (
    id         TEXT PRIMARY KEY,
    owner_kind TEXT NOT NULL DEFAULT '',
    owner_id   TEXT,
    path       TEXT,
    name       TEXT,
    tags_json  TEXT NOT NULL DEFAULT '[]',
    ops_json   TEXT NOT NULL DEFAULT '[]',
    hash       TEXT NOT NULL DEFAULT '',
    updated    TEXT
);

CREATE TABLE memories (
    id           TEXT PRIMARY KEY,
    scope        TEXT NOT NULL,
    type         TEXT NOT NULL DEFAULT '',
    file_path    TEXT,
    subject_json TEXT NOT NULL DEFAULT '{}',
    tags_json    TEXT NOT NULL DEFAULT '[]',
    source_json  TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'active',
    body         TEXT NOT NULL DEFAULT '',
    created      TEXT NOT NULL,
    updated      TEXT NOT NULL,
    hash         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE memory_subjects (
    memory_id     TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    value         TEXT NOT NULL,
    resolved_json TEXT,
    PRIMARY KEY (memory_id, kind, value)
);

CREATE INDEX idx_memory_subjects_kind_value ON memory_subjects(kind, value);

CREATE VIRTUAL TABLE memories_fts USING fts5(
    id UNINDEXED,
    body,
    tags,
    subject_text,
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TABLE runs (
    id                    TEXT PRIMARY KEY,
    flow_id               TEXT,
    flow_snapshot_json    TEXT,
    env                   TEXT,
    inputs_json           TEXT NOT NULL DEFAULT '{}',
    status                TEXT NOT NULL DEFAULT 'queued',
    started               TEXT,
    finished              TEXT,
    duration_ms           INTEGER,
    summary_json          TEXT NOT NULL DEFAULT '{}',
    pinned                INTEGER NOT NULL DEFAULT 0,
    trigger               TEXT,
    error_json            TEXT,
    operation_hashes_json TEXT
);

CREATE INDEX idx_runs_flow_id_started ON runs(flow_id, started);

CREATE TABLE run_steps (
    run_id          TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_id         TEXT NOT NULL,
    idx             INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT '',
    operation       TEXT,
    attempts        INTEGER NOT NULL DEFAULT 0,
    request_json    TEXT,
    response_json   TEXT,
    timings_json    TEXT,
    assertions_json TEXT,
    out_json        TEXT,
    error_json      TEXT,
    started         TEXT,
    finished        TEXT,
    PRIMARY KEY (run_id, step_id)
);

CREATE INDEX idx_run_steps_run_id_idx ON run_steps(run_id, idx);

CREATE TABLE environments (
    name       TEXT PRIMARY KEY,
    production INTEGER NOT NULL DEFAULT 0,
    doc_json   TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT
);
