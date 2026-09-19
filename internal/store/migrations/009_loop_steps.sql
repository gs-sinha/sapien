-- 009_loop_steps.sql — run_steps iteration/parent/kind/count (PLAN.md §34f.8).
--
-- A loop block's own StepResult (kind: "foreach"|"repeat", count: iterations
-- actually run) and each of its nested executions (iteration: 0-based,
-- parent: the enclosing block's step id) now share the run_steps table
-- with every other step. iteration defaults to -1, meaning "not inside a
-- loop" -- the same as every step before this migration, and as any
-- top-level/setup/teardown step going forward. The primary key grows a
-- third column so a nested step can appear once per iteration instead of
-- clobbering itself (and every other iteration) on every pass.
--
-- SQLite has no ALTER TABLE ... DROP/CHANGE PRIMARY KEY, so this rebuilds
-- the table: create the new shape, copy every existing row with
-- iteration = -1 (every row that already existed predates loop blocks, so
-- none of them are nested executions), drop the old table, rename the new
-- one into place. Tested against a database created by the pre-009 schema
-- with rows already in it (see internal/store/migrate_test.go).
CREATE TABLE run_steps_new (
    run_id          TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_id         TEXT NOT NULL,
    idx             INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT '',
    skip_reason     TEXT NOT NULL DEFAULT '',
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
    iteration       INTEGER NOT NULL DEFAULT -1,
    parent          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT '',
    count           INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (run_id, step_id, iteration)
);

INSERT INTO run_steps_new (
    run_id, step_id, idx, status, skip_reason, operation, attempts,
    request_json, response_json, timings_json, assertions_json, out_json,
    error_json, started, finished, iteration, parent, kind, count
)
SELECT
    run_id, step_id, idx, status, skip_reason, operation, attempts,
    request_json, response_json, timings_json, assertions_json, out_json,
    error_json, started, finished, -1, '', '', 0
FROM run_steps;

DROP TABLE run_steps;
ALTER TABLE run_steps_new RENAME TO run_steps;

CREATE INDEX idx_run_steps_run_id_idx ON run_steps(run_id, idx);
