-- 002_catalog.sql — additive columns/indexes for internal/catalog (PLAN.md §5, §15, §16).
--
-- 001_init.sql is left untouched; every change here is an ALTER TABLE ADD COLUMN or a new
-- index, so existing rows and existing readers of the 001 schema are unaffected.
--
-- doc_json columns follow the convention already established in 001 (operations.doc_json,
-- schemas.doc_json): the column holds the full JSON encoding of the corresponding
-- internal/domain struct and is the source of truth for reads; narrower columns alongside it
-- exist for filtering/indexing only and are kept in sync by internal/catalog on every write.

-- services: doc_json is the full domain.Service (Status/Error/Warnings/LastIndexed/
-- OperationCount all synced from the narrower columns on every catalog.Apply /
-- catalog.MarkServiceError call). operation_count mirrors len(Snapshot.Operations).
ALTER TABLE services ADD COLUMN doc_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE services ADD COLUMN operation_count INTEGER NOT NULL DEFAULT 0;

-- operations: raw_op_id_lc is the lower-cased "bare" operationId used for case-insensitive
-- bare-operationId resolution (falls back to the ID suffix after "<service>." when the
-- contract had no operationId, i.e. a synthesized ID).
ALTER TABLE operations ADD COLUMN raw_op_id_lc TEXT;
CREATE INDEX idx_operations_raw_op_id_lc ON operations(raw_op_id_lc);

-- fields: 001 has no doc_json column and no room for a searchable "leaf name" (the last
-- path segment with a trailing "[]" stripped, e.g. "items[].riderId" -> "riderId"). Both are
-- needed to round-trip the full domain.Field (Required/Enum/Format) and to serve
-- Catalog.FieldsByName / the operations_fts field_names column.
ALTER TABLE fields ADD COLUMN doc_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE fields ADD COLUMN leaf_name TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_fields_leaf_name ON fields(leaf_name);

-- flows: 001 has no column for domain.FlowSummary.StepCount.
ALTER TABLE flows ADD COLUMN step_count INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_flows_owner ON flows(owner_kind, owner_id);

-- docs: no index existed on the FK used by ListDocs/GetDoc/RemoveService.
CREATE INDEX idx_docs_service_id ON docs(service_id);
