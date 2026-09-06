package semantic

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// DocSectionInput is one doc section to embed (IndexDocs), carrying just
// enough to build its embedding text and to identify the row: id must match
// the doc_sections.id search already returns as DocSearchResult.SectionID,
// so Index.Query("doc", ...) hits can be joined straight back to it.
type DocSectionInput struct {
	ID      string
	Title   string
	Heading string
	Body    string
}

// Index computes and stores embeddings for operations, doc sections, and
// memories in one workspace database's "vectors" table
// (internal/store/migrations/004_vectors.sql), and answers nearest-neighbor
// queries against them.
type Index struct {
	db  *store.DB
	emb Embedder
}

// NewIndex returns an Index backed by db (which must already carry the
// 004_vectors.sql migration) and emb.
func NewIndex(db *store.DB, emb Embedder) *Index {
	return &Index{db: db, emb: emb}
}

// IndexOperations upserts an embedding per operation, skipping any operation
// whose built text hashes the same as what's already stored under the
// current embedding model. fields is keyed by operation id (domain.Field.
// OperationID); it need not contain an entry for every op. It returns how
// many rows were (re)written.
func (x *Index) IndexOperations(ctx context.Context, ops []domain.Operation, fields map[string][]domain.Field) (int, error) {
	return x.upsert(ctx, KindOperation, len(ops), func(i int) (id, text string) {
		op := ops[i]
		return op.ID, operationText(op, fields[op.ID])
	})
}

// IndexDocs upserts an embedding per doc section, skipping unchanged
// content. It returns how many rows were (re)written.
func (x *Index) IndexDocs(ctx context.Context, sections []DocSectionInput) (int, error) {
	return x.upsert(ctx, KindDoc, len(sections), func(i int) (id, text string) {
		s := sections[i]
		return s.ID, docSectionText(s)
	})
}

// IndexMemories upserts an embedding per memory, skipping unchanged content.
// It returns how many rows were (re)written.
func (x *Index) IndexMemories(ctx context.Context, mems []domain.Memory) (int, error) {
	return x.upsert(ctx, KindMemory, len(mems), func(i int) (id, text string) {
		m := mems[i]
		return m.ID, memoryText(m)
	})
}

// operationText builds the text embedded for one operation (PLAN §16):
// summary, description, the tokenized path, its leaf field names, and its
// tags.
func operationText(op domain.Operation, fields []domain.Field) string {
	var path string
	if op.HTTP != nil {
		path = op.HTTP.Path
	}

	leaves := make([]string, 0, len(fields))
	for _, f := range fields {
		leaves = append(leaves, fieldLeaf(f.Path))
	}

	return strings.Join([]string{
		op.Summary,
		op.Description,
		textutil.Join(textutil.Tokens(path)),
		strings.Join(leaves, " "),
		strings.Join(op.Tags, " "),
	}, " ")
}

// fieldLeaf returns the last path segment of a field path (with a trailing
// "[]" stripped), e.g. "response.200.body.items[].riderId" -> "riderId".
func fieldLeaf(path string) string {
	leaf := path
	if i := strings.LastIndexByte(leaf, '.'); i >= 0 {
		leaf = leaf[i+1:]
	}
	return strings.TrimSuffix(leaf, "[]")
}

// docSectionMaxBodyRunes caps how much of a doc section's body contributes
// to its embedding text (PLAN §16 / task spec).
const docSectionMaxBodyRunes = 1000

// docSectionText builds the text embedded for one doc section: title,
// heading, and the body's first 1,000 characters.
func docSectionText(s DocSectionInput) string {
	body := s.Body
	if r := []rune(body); len(r) > docSectionMaxBodyRunes {
		body = string(r[:docSectionMaxBodyRunes])
	}
	return strings.Join([]string{s.Title, s.Heading, body}, " ")
}

// memoryText builds the text embedded for one memory: its body followed by
// its tags.
func memoryText(m domain.Memory) string {
	return strings.Join(append([]string{m.Text}, m.Tags...), " ")
}

// existingRow is what upsert needs to know about an already-indexed row to
// decide whether it can be skipped.
type existingRow struct {
	hash  string
	model string
}

// itemText returns the (id, text) pair for item i; upsert calls it once per
// item before deciding what needs (re)embedding.
type itemText func(i int) (id, text string)

// upsert is the shared body of IndexOperations/IndexDocs/IndexMemories: it
// builds every item's text, skips ones whose content hash and model already
// match the stored row, embeds the rest in one Embed call (letting the
// Embedder itself batch against its configured BatchSize), and writes the
// result in a single transaction. It returns how many rows were written.
func (x *Index) upsert(ctx context.Context, kind Kind, n int, get itemText) (int, error) {
	if n == 0 {
		return 0, nil
	}

	ids := make([]string, n)
	texts := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i], texts[i] = get(i)
	}

	existing, err := x.loadExisting(ctx, kind, ids)
	if err != nil {
		return 0, err
	}

	model := x.emb.Model()

	type pending struct {
		idx  int
		hash string
	}
	var todo []pending
	for i, id := range ids {
		h := contentHash(texts[i])
		if ex, ok := existing[id]; ok && ex.hash == h && ex.model == model {
			continue // unchanged content under the same model: skip
		}
		todo = append(todo, pending{idx: i, hash: h})
	}
	if len(todo) == 0 {
		return 0, nil
	}

	toEmbed := make([]string, len(todo))
	for i, p := range todo {
		toEmbed[i] = texts[p.idx]
	}
	vecs, err := x.emb.Embed(ctx, toEmbed)
	if err != nil {
		return 0, fmt.Errorf("semantic: embed %s: %w", kind, err)
	}
	if len(vecs) != len(todo) {
		return 0, fmt.Errorf("semantic: embedder returned %d vectors for %d inputs", len(vecs), len(todo))
	}

	dim := x.emb.Dim()
	now := time.Now().UTC().Format(time.RFC3339)

	err = x.db.Write(ctx, func(tx *sql.Tx) error {
		for i, p := range todo {
			blob := encodeVector(vecs[i])
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO vectors (kind, id, model, dim, content_hash, embedding, updated)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(kind, id) DO UPDATE SET
					model        = excluded.model,
					dim          = excluded.dim,
					content_hash = excluded.content_hash,
					embedding    = excluded.embedding,
					updated      = excluded.updated`,
				string(kind), ids[p.idx], model, dim, p.hash, blob, now,
			); err != nil {
				return fmt.Errorf("semantic: upsert vector %s/%s: %w", kind, ids[p.idx], err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(todo), nil
}

// loadExisting loads the current (content_hash, model) for every id of kind
// that already has a row.
func (x *Index) loadExisting(ctx context.Context, kind Kind, ids []string) (map[string]existingRow, error) {
	out := map[string]existingRow{}
	if len(ids) == 0 {
		return out, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, string(kind))
	for _, id := range ids {
		args = append(args, id)
	}

	q := `SELECT id, content_hash, model FROM vectors WHERE kind = ? AND id IN (` + placeholders + `)`
	rows, err := x.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("semantic: load existing vectors: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, hash, model string
		if err := rows.Scan(&id, &hash, &model); err != nil {
			return nil, fmt.Errorf("semantic: scan existing vector: %w", err)
		}
		out[id] = existingRow{hash: hash, model: model}
	}
	return out, rows.Err()
}

// Query embeds text and returns the limit best cosine matches among the
// stored vectors of kind, restricted to rows written under the Embedder's
// current (model, dim) — rows from a since-changed embedder configuration
// are never mixed into a comparison. Ties break on id ascending. An empty
// (or whitespace-only) text or a non-positive limit returns (nil, nil)
// without embedding anything.
func (x *Index) Query(ctx context.Context, kind Kind, text string, limit int) ([]Hit, error) {
	if strings.TrimSpace(text) == "" || limit <= 0 {
		return nil, nil
	}

	qvecs, err := x.emb.Embed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("semantic: embed query: %w", err)
	}
	if len(qvecs) == 0 {
		return nil, fmt.Errorf("semantic: embedder returned no vector for query")
	}
	q := qvecs[0]

	rows, err := x.db.SQL().QueryContext(ctx,
		`SELECT id, embedding FROM vectors WHERE kind = ? AND model = ? AND dim = ?`,
		string(kind), x.emb.Model(), x.emb.Dim())
	if err != nil {
		return nil, fmt.Errorf("semantic: query vectors: %w", err)
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("semantic: scan vector: %w", err)
		}
		hits = append(hits, Hit{ID: id, Score: Cosine(q, decodeVector(blob))})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// Stats summarizes the vectors table: total rows, a per-kind breakdown, and
// a per-(model, dim) breakdown (useful for spotting a stale model's rows
// after a config change, since Query silently ignores them).
func (x *Index) Stats(ctx context.Context) (Stats, error) {
	st := Stats{ByKind: map[Kind]int{}}

	kindRows, err := x.db.SQL().QueryContext(ctx, `SELECT kind, COUNT(*) FROM vectors GROUP BY kind`)
	if err != nil {
		return Stats{}, fmt.Errorf("semantic: stats by kind: %w", err)
	}
	for kindRows.Next() {
		var k string
		var c int
		if err := kindRows.Scan(&k, &c); err != nil {
			kindRows.Close()
			return Stats{}, fmt.Errorf("semantic: scan stats by kind: %w", err)
		}
		st.ByKind[Kind(k)] = c
		st.Total += c
	}
	if err := kindRows.Err(); err != nil {
		kindRows.Close()
		return Stats{}, err
	}
	kindRows.Close()

	modelRows, err := x.db.SQL().QueryContext(ctx,
		`SELECT model, dim, COUNT(*) FROM vectors GROUP BY model, dim ORDER BY model, dim`)
	if err != nil {
		return Stats{}, fmt.Errorf("semantic: stats by model: %w", err)
	}
	defer modelRows.Close()
	for modelRows.Next() {
		var m ModelStat
		if err := modelRows.Scan(&m.Model, &m.Dim, &m.Count); err != nil {
			return Stats{}, fmt.Errorf("semantic: scan stats by model: %w", err)
		}
		st.Models = append(st.Models, m)
	}
	return st, modelRows.Err()
}

// Clear deletes every vector row of kind.
func (x *Index) Clear(ctx context.Context, kind Kind) error {
	return x.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM vectors WHERE kind = ?`, string(kind))
		return err
	})
}

// contentHash hashes text for the skip-unchanged check.
func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// encodeVector serializes v as little-endian float32s (the vectors.embedding
// blob format).
func encodeVector(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:i*4+4], math.Float32bits(f))
	}
	return buf
}

// decodeVector is encodeVector's inverse. A blob whose length is not a
// multiple of 4 has its trailing partial float dropped.
func decodeVector(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4 : i*4+4]))
	}
	return out
}
