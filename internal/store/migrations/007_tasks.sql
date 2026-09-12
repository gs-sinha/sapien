-- 007_tasks.sql -- caller-vocabulary task index and operation mappings.
-- Tests remain in doc_json and are deliberately excluded from tasks_fts so
-- retrieval assertions cannot pass by indexing their own query text.
CREATE TABLE tasks (
    id         TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    raw_id     TEXT NOT NULL,
    doc_json   TEXT NOT NULL DEFAULT '{}',
    UNIQUE(service_id, raw_id)
);

CREATE INDEX idx_tasks_service_id ON tasks(service_id);

CREATE TABLE task_targets (
    task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    when_text    TEXT NOT NULL DEFAULT '',
    ord          INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(task_id, operation_id)
);

CREATE INDEX idx_task_targets_operation_id ON task_targets(operation_id);

CREATE VIRTUAL TABLE tasks_fts USING fts5(
    task_id UNINDEXED,
    service,
    phrases,
    tokenize = 'unicode61 remove_diacritics 2'
);
