package search

import "context"

// Semantic is the subset of internal/semantic.Index's Query method Searcher
// needs to fuse a vector-similarity ranking into lexical search (PLAN.md
// §16). It is defined here, independently of internal/semantic, so search
// never imports semantic (avoiding an import cycle risk and keeping this
// package's only dependencies domain/store/textutil): any type satisfying
// this method set — in practice *semantic.Index — can be passed to
// WithSemantic.
type Semantic interface {
	// Query returns up to limit semantic search hits for kind ("operation"
	// or "doc"), best match first.
	Query(ctx context.Context, kind string, text string, limit int) ([]SemanticHit, error)
}

// SemanticHit is one hit from a Semantic implementation.
type SemanticHit struct {
	ID    string
	Score float32
}

// WithSemantic sets (or, with sem == nil, clears) the semantic search
// backend fused into Operations() and Docs(). It returns s for chaining. A
// nil sem (the zero value, and New's default) disables semantic fusion
// entirely: Operations()/Docs() then behave exactly as lexical-only search,
// unchanged from before this hook existed.
func (s *Searcher) WithSemantic(sem Semantic) *Searcher {
	s.sem = sem
	return s
}

// rrfK mirrors internal/semantic.RRF's reciprocal-rank-fusion constant.
const rrfK = 60.0

// rrf fuses ranked id lists (best match first) via reciprocal-rank fusion:
// an id at (0-based) rank r in a list contributes 1/(rrfK+r+1) to its fused
// score, summed across every list it appears in (only its first occurrence
// within a given list counts). The result is sorted by fused score
// descending, id ascending as a tiebreak.
//
// This is a deliberate, small (~20 line) duplicate of
// internal/semantic.RRF's exact algorithm: search must not import semantic
// (semantic is an optional, off-by-default layer; search is core and must
// keep working with zero knowledge of it), and semantic has no reason to
// import search either, so keeping one shared implementation would mean
// inventing a third package just to hold ~20 lines.
func rrf(lists ...[]string) []scoredID {
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

	out := make([]scoredID, 0, len(order))
	for _, id := range order {
		out = append(out, scoredID{id: id, score: scores[id]})
	}
	return sortScored(out, len(out))
}
