-- 003_memory.sql — additive columns/indexes for internal/memory (PLAN.md §10-§13, §15).
--
-- 001_init.sql is left untouched; every change here is an ALTER TABLE ADD COLUMN or a new
-- index, so existing rows and existing readers of the 001 schema are unaffected.

-- memories.resolved_json holds the whole-memory domain.Memory.Resolved snapshot (method,
-- path, operation_hash, resolved_at, unresolved) taken the last time the memory's subject was
-- resolved against the catalog (PLAN §11). This is distinct from (and mirrored into)
-- memory_subjects.resolved_json, which 001 already carries per subject row; the memories-level
-- column exists so Store.Get can round-trip domain.Memory.Resolved without a join.
ALTER TABLE memories ADD COLUMN resolved_json TEXT;

-- Indexes supporting Store.List's filters (scope, type) and its "ordered by updated desc"
-- default, and Store.Reindex's "does this file still exist" sweep over non-personal rows.
CREATE INDEX idx_memories_scope ON memories(scope);
CREATE INDEX idx_memories_status ON memories(status);
CREATE INDEX idx_memories_updated ON memories(updated);
