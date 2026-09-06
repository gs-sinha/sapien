package search

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
)

// wrapf wraps err (if non-nil) with a "search: <what>" prefix.
func wrapf(err error, what string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("search: %s: %w", what, err)
}

// placeholdersFor returns a "?,?,...,?" placeholder list and the matching
// args slice for ids, for use in an "IN (...)" clause.
func placeholdersFor(ids []string) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

// queryIDs runs q (which must SELECT exactly one text column) and returns
// the matched values in row order.
func queryIDs(ctx context.Context, db *store.DB, q string, args ...any) ([]string, error) {
	rows, err := db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "query ids")
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapf(err, "scan id")
		}
		ids = append(ids, id)
	}
	return ids, wrapf(rows.Err(), "query ids rows")
}

// loadOperations loads the full domain.Operation for each id from
// operations.doc_json.
func loadOperations(ctx context.Context, db *store.DB, ids []string) (map[string]domain.Operation, error) {
	out := map[string]domain.Operation{}
	if len(ids) == 0 {
		return out, nil
	}

	placeholders, args := placeholdersFor(ids)
	q := `SELECT id, doc_json FROM operations WHERE id IN (` + placeholders + `)`
	rows, err := db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "load operations")
	}
	defer rows.Close()

	for rows.Next() {
		var id, docJSON string
		if err := rows.Scan(&id, &docJSON); err != nil {
			return nil, wrapf(err, "scan operation")
		}
		var op domain.Operation
		if err := store.UnmarshalJSON(docJSON, &op); err != nil {
			return nil, wrapf(err, "unmarshal operation "+id)
		}
		out[id] = op
	}
	return out, wrapf(rows.Err(), "load operations rows")
}

// opMeta is the lightweight per-operation metadata search needs for scoring,
// filtering, and boosts, without paying for a full doc_json decode.
type opMeta struct {
	rawOpID     string
	method      string
	deprecated  bool
	serviceName string
}

// loadOpMeta loads opMeta for each id.
func (s *Searcher) loadOpMeta(ctx context.Context, ids []string) (map[string]opMeta, error) {
	out := map[string]opMeta{}
	if len(ids) == 0 {
		return out, nil
	}

	placeholders, args := placeholdersFor(ids)
	q := `SELECT o.id, COALESCE(o.raw_op_id, ''), o.method, o.deprecated, sv.name
	      FROM operations o
	      JOIN services sv ON sv.id = o.service_id
	      WHERE o.id IN (` + placeholders + `)`
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "load op meta")
	}
	defer rows.Close()

	for rows.Next() {
		var id, rawOpID, method, serviceName string
		var deprecated int
		if err := rows.Scan(&id, &rawOpID, &method, &deprecated, &serviceName); err != nil {
			return nil, wrapf(err, "scan op meta")
		}
		out[id] = opMeta{rawOpID: rawOpID, method: method, deprecated: deprecated != 0, serviceName: serviceName}
	}
	return out, wrapf(rows.Err(), "load op meta rows")
}

// loadFieldLeaves loads the leaf name of every fields.field_path row for
// each operation id (e.g. "response.200.body.rider.qcomSkill" -> "qcomSkill"),
// for MatchedOn's "field:<leaf>" reporting.
func (s *Searcher) loadFieldLeaves(ctx context.Context, ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(ids) == 0 {
		return out, nil
	}

	placeholders, args := placeholdersFor(ids)
	q := `SELECT operation_id, field_path FROM fields WHERE operation_id IN (` + placeholders + `)`
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "load field leaves")
	}
	defer rows.Close()

	for rows.Next() {
		var opID, path string
		if err := rows.Scan(&opID, &path); err != nil {
			return nil, wrapf(err, "scan field leaf")
		}
		leaf := path
		if i := strings.LastIndexByte(leaf, '.'); i >= 0 {
			leaf = leaf[i+1:]
		}
		leaf = strings.TrimSuffix(leaf, "[]")
		out[opID] = append(out[opID], leaf)
	}
	return out, wrapf(rows.Err(), "load field leaves rows")
}

// ftsOperationsRaw runs match against operations_fts and returns the raw
// (negative, lower-is-better) bm25 score per operation id. An empty match
// returns an empty map without touching the database, since FTS5 rejects an
// empty MATCH string.
func (s *Searcher) ftsOperationsRaw(ctx context.Context, match string) (map[string]float64, error) {
	out := map[string]float64{}
	if match == "" {
		return out, nil
	}

	// Column order: id, service, op_id, path_tokens, summary, description,
	// tags, param_names, field_names, doc_text, memory_text (task spec's
	// exact bm25 call; doc_text/memory_text added by the search ranking
	// tuning task's knowledge-folding step, weight 2 each — see
	// internal/catalog.go's FTS column contract).
	q := `SELECT id, bm25(operations_fts, 0, 1, 8, 6, 5, 1, 3, 3, 3, ` + knowledgeWeights() + `) AS score
	      FROM operations_fts WHERE operations_fts MATCH ?`
	rows, err := s.db.SQL().QueryContext(ctx, q, match)
	if err != nil {
		return nil, wrapf(err, "operations fts query")
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, wrapf(err, "scan operations fts row")
		}
		out[id] = score
	}
	return out, wrapf(rows.Err(), "operations fts rows")
}

// trigramOperationScores runs one MATCH query per token of 3+ runes against
// operations_trigram (op_id/path substring hits) and returns, per matched
// id, the sum of 0.4*tokenWeight(t) over every token t that hit it — so a
// trigram hit driven only by a down-weighted generic-verb token (e.g.
// "create" substring-matching "createOrder") contributes less than one
// driven by a domain token (e.g. "allocation" substring-matching
// "allocation-service.allocate"), same as every other additive boost
// (search ranking tuning task).
// columnMatchIDs runs a cheap per-column MATCH probe restricted to ids and
// to the single FTS5 column col (via fts5's "colname:term" column-filter
// syntax), returning the subset of ids whose col actually contains a hit
// for one of tokens. Used to tell whether a result's overall bm25 hit came
// (at least partly) from doc_text or memory_text, for matched_on's "docs"/
// "memories" labels — cheap because ids is already limited to the query's
// result set (at most a handful), not the whole table.
func (s *Searcher) columnMatchIDs(ctx context.Context, ids []string, col string, tokens []string) (map[string]bool, error) {
	out := map[string]bool{}
	match := columnMatchOR(col, tokens)
	if match == "" || len(ids) == 0 {
		return out, nil
	}

	idPH, idArgs := placeholdersFor(ids)
	q := `SELECT DISTINCT id FROM operations_fts WHERE id IN (` + idPH + `) AND operations_fts MATCH ?`
	args := append(idArgs, match)

	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "column match "+col)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapf(err, "scan column match "+col)
		}
		out[id] = true
	}
	return out, wrapf(rows.Err(), "column match rows "+col)
}

// columnMatchOR builds an fts5 MATCH expression restricting every token to
// column col, ORed together, e.g. `doc_text:"pickup"* OR doc_text:"radius"*`.
// Returns "" for no (non-empty) tokens.
func columnMatchOR(col string, tokens []string) string {
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t == "" {
			continue
		}
		parts = append(parts, col+":"+quoteFTSTerm(t))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " OR ")
}

func (s *Searcher) trigramOperationScores(ctx context.Context, tokens []string) (map[string]float64, error) {
	scores := map[string]float64{}
	for _, t := range tokens {
		if len([]rune(t)) < 3 {
			continue
		}
		rows, err := s.db.SQL().QueryContext(ctx,
			`SELECT DISTINCT id FROM operations_trigram WHERE operations_trigram MATCH ?`,
			quoteFTSLiteral(t))
		if err != nil {
			return nil, wrapf(err, "trigram query")
		}
		w := 0.4 * tokenWeight(t)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, wrapf(err, "scan trigram row")
			}
			scores[id] += w
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, wrapf(err, "trigram rows")
		}
	}
	return scores, nil
}

// knowledgeWeights returns the bm25 weights for the doc_text and memory_text
// columns as "d, m". They default to docTextWeight and memoryTextWeight;
// SAPIEN_SEARCH_KNOWLEDGE_WEIGHTS="d,m" overrides them for ranking
// experiments (experiments/search-eval) without a rebuild. Read once.
func knowledgeWeights() string {
	knowledgeWeightsOnce.Do(func() {
		knowledgeWeightsStr = fmt.Sprintf("%g, %g", docTextWeight, memoryTextWeight)
		if v := os.Getenv("SAPIEN_SEARCH_KNOWLEDGE_WEIGHTS"); v != "" {
			var d, m float64
			if n, err := fmt.Sscanf(v, "%g,%g", &d, &m); err == nil && n == 2 && d >= 0 && m >= 0 {
				knowledgeWeightsStr = fmt.Sprintf("%g, %g", d, m)
			}
		}
	})
	return knowledgeWeightsStr
}

// docTextWeight and memoryTextWeight are the bm25 column weights for the
// knowledge columns (PLAN §16): below summary (5) and above description (1),
// so a doc or memory mention supports a contract match rather than
// overriding it. Tuned on experiments/search-eval.
const (
	docTextWeight    = 1.0
	memoryTextWeight = 4.0
)

var (
	knowledgeWeightsOnce sync.Once
	knowledgeWeightsStr  string
)
