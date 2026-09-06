-- 005_examples.sql — table for internal/example (PLAN.md §34b).
--
-- 001-004 are left untouched; this migration only adds a new table and its indexes, so
-- existing rows and existing readers of the prior schema are unaffected.
--
-- Examples are files first: one committed "<id>.example.yaml" per saved request, living under
-- <workspace>/examples/ (workspace scope) or a service's own api/examples/ (service scope,
-- travels with the code). This table indexes just enough of each file's metadata --
-- operation, service, scope, description, tags, whether it carries a `verified` block, its
-- verified environment, and where its file lives -- for internal/example.Store's
-- List/Get/ForOperations lookups; the full body/input/headers/expect content is read from the
-- file itself (Get parses the file at `path`), the same files-are-truth, SQLite-is-an-index
-- split PLAN.md §10-§13 describes for memories.
CREATE TABLE examples (
    id          TEXT PRIMARY KEY,
    operation   TEXT NOT NULL,
    service     TEXT NOT NULL,
    scope       TEXT NOT NULL,
    description TEXT,
    tags        TEXT, -- JSON array
    verified    INTEGER NOT NULL DEFAULT 0,
    env         TEXT,
    path        TEXT NOT NULL,
    created     TEXT,
    updated     TEXT
);

CREATE INDEX idx_examples_operation ON examples(operation);
CREATE INDEX idx_examples_service ON examples(service);
CREATE INDEX idx_examples_updated ON examples(updated);
