-- 004_vectors.sql — additive table for internal/semantic (PLAN.md §16).
--
-- 001-003 are left untouched; this migration only adds a new table and its index, so
-- existing rows and existing readers of the prior schema are unaffected.
--
-- vectors holds one row per (kind, id) — "operation" | "doc" | "memory" and the catalog/doc/
-- memory row's own id — with the embedding computed by internal/semantic.Index. There is no
-- sqlite-vec (or any other cgo) dependency: embedding is a plain BLOB of little-endian
-- float32s, decoded and compared with brute-force cosine similarity in Go. That keeps the
-- engine binary cgo-free at the cost of a full table scan per query, which internal/semantic
-- documents as fine up to about 10k rows per kind and not intended to scale much beyond that.
--
-- model and dim are stored per row (not just once globally) so that switching embedding
-- models/endpoints never mixes incompatible vectors: internal/semantic.Index.Query filters to
-- the current embedder's (model, dim) before computing cosine similarity, and re-embeds rows
-- written under a different model on next Index* call. content_hash is the hash of the text
-- that was embedded (PLAN §16 "Semantic"); a row is skipped on the next Index* call when its
-- content_hash and model both still match, so re-indexing unchanged content is a no-op.
CREATE TABLE vectors (
    kind         TEXT NOT NULL,
    id           TEXT NOT NULL,
    model        TEXT NOT NULL,
    dim          INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    embedding    BLOB NOT NULL,
    updated      TEXT,
    PRIMARY KEY (kind, id)
);

CREATE INDEX idx_vectors_kind ON vectors(kind);
