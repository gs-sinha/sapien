// Package semantic is Sapien's optional semantic-search layer (PLAN.md §16):
// embedding-backed vector search over operations, doc sections, and
// memories, fused with lexical (FTS5) search via reciprocal-rank fusion. It
// is off by default and requires an external, OpenAI-compatible or Ollama
// embedding endpoint (see NewHTTPEmbedder and README.md).
//
// There is no sqlite-vec (or any other cgo) dependency: vectors are stored as
// plain BLOBs of little-endian float32s in the "vectors" table
// (internal/store/migrations/004_vectors.sql) and similarity is brute-force
// cosine, computed in Go over every row of the queried kind. That keeps the
// engine binary cgo-free at the cost of an O(n) scan per query — fine up to
// roughly 10,000 rows per kind (PLAN §16's "≤10k operations" target), and not
// intended to scale much beyond that without adding a real vector index.
package semantic

import (
	"math"
	"sort"
)

// Kind identifies which catalog a vector row belongs to.
type Kind string

const (
	KindOperation Kind = "operation"
	KindDoc       Kind = "doc"
	KindMemory    Kind = "memory"
)

// Hit is one semantic search result.
type Hit struct {
	ID    string
	Score float32 // cosine similarity, [-1, 1]
}

// ModelStat is the row count for one (model, dim) pair, as reported by Stats.
type ModelStat struct {
	Model string
	Dim   int
	Count int
}

// Stats summarizes the vectors table.
type Stats struct {
	Total  int
	ByKind map[Kind]int
	Models []ModelStat
}

// Cosine returns the cosine similarity of a and b. Vectors of unequal length
// are compared over their shared prefix (this should not happen in practice:
// Index.Query only ever compares vectors written by the same (model, dim)).
// A zero-length or all-zero vector yields 0 rather than dividing by zero.
func Cosine(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		af, bf := float64(a[i]), float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

// Scored is one id from RRF, with its fused reciprocal-rank score.
type Scored struct {
	ID    string
	Score float64
}

// rrfK is the reciprocal-rank-fusion constant (PLAN §16). It damps the
// influence of rank differences deep in a list: a fixed k means the 1st vs.
// 2nd place gap contributes much more than the 60th vs. 61st gap.
const rrfK = 60.0

// RRF fuses any number of ranked id lists (best match first) into one
// reciprocal-rank-fusion ranking: every id in list l at (0-based) rank r
// contributes 1/(rrfK+r+1) to its fused score, summed across every list it
// appears in. The result is sorted by fused score descending, then id
// ascending as a deterministic tiebreak. A list may contain duplicate ids;
// only the first occurrence's rank counts toward that list's contribution
// (later duplicates are ignored, matching how a ranked search result list is
// never expected to repeat an id).
//
// internal/search implements the same algorithm as a small unexported
// helper rather than importing this package, to keep semantic decoupled from
// search (search must not import semantic; semantic has no need to import
// search either, so there is no cycle — the duplication is a deliberate,
// small (~20 line) tradeoff documented in both places).
func RRF(lists ...[]string) []Scored {
	scores := map[string]float64{}
	rankedAlready := map[string]bool{}
	var order []string

	for _, list := range lists {
		seenInList := map[string]bool{}
		for rank, id := range list {
			if seenInList[id] {
				continue
			}
			seenInList[id] = true
			scores[id] += 1.0 / (rrfK + float64(rank) + 1.0)
			if !rankedAlready[id] {
				rankedAlready[id] = true
				order = append(order, id)
			}
		}
	}

	out := make([]Scored, 0, len(order))
	for _, id := range order {
		out = append(out, Scored{ID: id, Score: scores[id]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}
