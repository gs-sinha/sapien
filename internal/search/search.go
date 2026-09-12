// Package search implements Sapien's structured, lexical, and (future)
// semantic search over the operation and documentation catalogs (PLAN.md
// §16, PRD §13). It is a pure reader: it never writes to the store, and it
// tokenizes queries with exactly the helpers in internal/textutil so that it
// agrees with whatever wrote the FTS5 index (the catalog indexer).
package search

import (
	"context"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// Searcher runs operation and doc search against one workspace database.
type Searcher struct {
	db  *store.DB
	sem Semantic // optional; nil disables semantic fusion (see WithSemantic)
}

// New returns a Searcher backed by db. db must already be migrated; Searcher
// only issues reads.
func New(db *store.DB) *Searcher {
	return &Searcher{db: db}
}

// defaultLimit is used when opts.Limit is zero or negative.
const defaultLimit = 10

func effectiveLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	return n
}

// Operations searches the operation catalog (PLAN.md §16). A path-shaped
// query ("POST /v1/orders", "/v1/orders") is resolved structurally against
// operation_aliases; anything else runs lexical FTS5 + trigram search over
// operations_fts / operations_trigram. An empty (or whitespace-only) query
// returns (nil, nil).
func (s *Searcher) Operations(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.SearchResult, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, nil
	}

	if method, path, ok := textutil.ParseURLQuery(trimmed); ok {
		return s.urlLookup(ctx, method, path, opts)
	}
	return s.lexicalLookup(ctx, trimmed, opts)
}

// scoredID pairs an id with a final score for sorting.
type scoredID struct {
	id    string
	score float64
}

// sortScored sorts by score descending, then id ascending, and truncates to
// limit.
func sortScored(list []scoredID, limit int) []scoredID {
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].id < list[j].id
	})
	if len(list) > limit {
		list = list[:limit]
	}
	return list
}

// ---- structured (path-shaped) lookup ----

// structuredLookup implements PLAN §16's path-shaped query resolution: exact
// (method,path) alias match (score 1.0), else a templated match honoring
// {param} segments (score 0.9), else a path-prefix match (score 0.7).
func (s *Searcher) structuredLookup(ctx context.Context, method, path string, opts domain.SearchOptions) ([]domain.SearchResult, error) {
	effMethod := method
	if effMethod == "" {
		effMethod = opts.Method
	}

	ids, err := s.aliasExact(ctx, effMethod, path, opts.Service)
	if err != nil {
		return nil, err
	}
	baseScore := 1.0

	if len(ids) == 0 {
		ids, err = s.aliasTemplated(ctx, effMethod, path, opts.Service)
		if err != nil {
			return nil, err
		}
		baseScore = 0.9
	}

	if len(ids) == 0 {
		ids, err = s.aliasPrefix(ctx, effMethod, path, opts.Service)
		if err != nil {
			return nil, err
		}
		baseScore = 0.7
	}

	if len(ids) == 0 {
		return nil, nil
	}

	limit := effectiveLimit(opts.Limit)

	metas, err := s.loadOpMeta(ctx, ids)
	if err != nil {
		return nil, err
	}

	list := make([]scoredID, 0, len(ids))
	for _, id := range ids {
		meta, ok := metas[id]
		if !ok {
			continue
		}
		score := baseScore
		if meta.deprecated && !opts.IncludeDeprecated {
			score *= 0.5
		}
		list = append(list, scoredID{id: id, score: score})
	}
	list = sortScored(list, limit)

	return s.buildResults(ctx, list, textutil.PathTokens(path))
}

func (s *Searcher) aliasExact(ctx context.Context, method, path, service string) ([]string, error) {
	q := `SELECT DISTINCT a.operation_id FROM operation_aliases a
	      JOIN operations o ON o.id = a.operation_id
	      JOIN services sv ON sv.id = o.service_id
	      WHERE a.path = ?`
	args := []any{path}
	if method != "" {
		q += ` AND a.method = ?`
		args = append(args, method)
	}
	if service != "" {
		q += ` AND sv.name = ?`
		args = append(args, service)
	}
	return queryIDs(ctx, s.db, q, args...)
}

func (s *Searcher) aliasTemplated(ctx context.Context, method, path, service string) ([]string, error) {
	q := `SELECT a.operation_id, a.path FROM operation_aliases a
	      JOIN operations o ON o.id = a.operation_id
	      JOIN services sv ON sv.id = o.service_id
	      WHERE 1 = 1`
	var args []any
	if method != "" {
		q += ` AND a.method = ?`
		args = append(args, method)
	}
	if service != "" {
		q += ` AND sv.name = ?`
		args = append(args, service)
	}

	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "templated alias query")
	}
	defer rows.Close()

	seen := map[string]bool{}
	var ids []string
	for rows.Next() {
		var opID, aliasPath string
		if err := rows.Scan(&opID, &aliasPath); err != nil {
			return nil, wrapf(err, "templated alias scan")
		}
		if seen[opID] {
			continue
		}
		if textutil.PathMatches(aliasPath, path) {
			seen[opID] = true
			ids = append(ids, opID)
		}
	}
	return ids, wrapf(rows.Err(), "templated alias rows")
}

func (s *Searcher) aliasPrefix(ctx context.Context, method, path, service string) ([]string, error) {
	q := `SELECT DISTINCT a.operation_id FROM operation_aliases a
	      JOIN operations o ON o.id = a.operation_id
	      JOIN services sv ON sv.id = o.service_id
	      WHERE a.path LIKE ? || '%'`
	args := []any{path}
	if method != "" {
		q += ` AND a.method = ?`
		args = append(args, method)
	}
	if service != "" {
		q += ` AND sv.name = ?`
		args = append(args, service)
	}
	return queryIDs(ctx, s.db, q, args...)
}

// ---- lexical (free-text) lookup ----

// lexicalLookup implements PLAN §16's FTS5 + trigram scoring pipeline: an AND
// query over operations_fts, an OR fallback when the AND query is thin, a
// trigram substring pass, then boosts, filters, the deprecated penalty, and
// finally sort + limit.
func (s *Searcher) lexicalLookup(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.SearchResult, error) {
	limit := effectiveLimit(opts.Limit)

	allTokens := textutil.Tokens(query)
	if len(allTokens) == 0 {
		return nil, nil
	}
	tokens := filterStopWords(allTokens)

	combined := map[string]float64{}
	taskScores, taskMatches, err := s.taskOperationRanking(ctx, tokens, opts.Service)
	if err != nil {
		return nil, err
	}
	for id, score := range taskScores {
		// Authored task phrases are explicit retrieval contracts. Give them a
		// stronger contribution than a single operation-text ranker hit while
		// retaining the normal filters and semantic/doc fusion below.
		combined[id] += score * 2
	}

	andRaw, err := s.ftsOperationsRaw(ctx, BuildMatch(tokens, "and"))
	if err != nil {
		return nil, err
	}
	for id, v := range normalizeBM25(andRaw) {
		combined[id] += v
	}

	if len(andRaw) < limit {
		orRaw, err := s.ftsOperationsRaw(ctx, BuildMatch(tokens, "or"))
		if err != nil {
			return nil, err
		}
		for id, v := range normalizeBM25(orRaw) {
			combined[id] += v * 0.6
		}
	}

	trigramScores, err := s.trigramOperationScores(ctx, tokens)
	if err != nil {
		return nil, err
	}
	for id, v := range trigramScores {
		combined[id] += v
	}

	// lexicalIDs snapshots combined's keys before any semantic ids are
	// mixed into the wider id set below, so the boosts loop just below only
	// ever touches ids that actually came from FTS/trigram (adding a
	// semantic-only id as a *new* combined key here — via Go's map "+="
	// zero-value semantics — would silently fold it into the lexical
	// ranking instead of the RRF fusion path further down).
	lexicalIDs := make([]string, 0, len(combined))
	for id := range combined {
		lexicalIDs = append(lexicalIDs, id)
	}
	// Usage feedback (search ranking tuning task, part 2): load
	// search_feedback rows for this query's non-generic tokens and
	// aggregate per operation. An operation with enough total feedback
	// (>= feedbackNewCandidateMinCount) that isn't already a lexical
	// candidate is seeded into combined at score 0 here, so it flows
	// through the same metadata-loading/filtering pipeline as every other
	// candidate below; the actual (capped) boost is only added once "the
	// top score" it's capped relative to is known, right before scoring
	// (see the feedbackApplied block after the filters loop).
	feedbackByOp := map[string]feedbackAgg{}
	if feedbackTerms := nonGenericTokens(tokens); !opts.Deterministic && len(feedbackTerms) > 0 {
		feedbackRows, ferr := s.loadFeedback(ctx, feedbackTerms)
		if ferr != nil {
			return nil, ferr
		}
		feedbackByOp = aggregateFeedback(feedbackRows)
		for id, agg := range feedbackByOp {
			if _, ok := combined[id]; !ok && agg.countTotal >= feedbackNewCandidateMinCount {
				combined[id] = 0
			}
		}
	}

	// Optional semantic fusion (PLAN §16): fetched here, before the
	// "nothing matched" check, so a query lexical search misses entirely
	// can still surface semantic-only hits.
	var semHits []SemanticHit
	semanticActive := s.sem != nil && !opts.Deterministic
	if semanticActive {
		var serr error
		semHits, serr = s.sem.Query(ctx, "operation", query, limit*2)
		if serr != nil {
			return nil, serr
		}
	}

	// Optional docs-mediated second ranker (search ranking tuning task,
	// docs-fusion experiment; see docfusion.go): off unless
	// SAPIEN_SEARCH_DOC_FUSION says otherwise, in which case docRanking may
	// still come back empty (too few non-generic tokens, no docs hit, or no
	// hit section references an operation) and the rest of this function
	// behaves exactly as if fusion were off. Computed here, before the
	// "nothing matched" check just below (same reasoning as the semantic
	// fusion above it and the feedback seeding above that): a query lexical
	// search misses entirely can still surface an operation the docs ranker
	// alone found.
	fusionCfg := effectiveDocFusionConfig()
	if opts.Deterministic {
		fusionCfg.mode = docFusionOff
	}
	var docRanking docOpRanking
	if fusionCfg.mode != docFusionOff {
		docRanking, err = s.docFusionRanking(ctx, tokens, fusionCfg.scope)
		if err != nil {
			return nil, err
		}
	}

	// Filter doc-ranker candidates by the same service/method/deprecated
	// rules as every other candidate (mirrors the semantic-hit filtering
	// above), preserving docRanking's rank order.
	var docSet map[string]bool
	var docIDsFiltered []string
	filteredDocScore := map[string]float64{}
	if len(docRanking.opIDs) > 0 {
		docMetas, derr := s.loadOpMeta(ctx, docRanking.opIDs)
		if derr != nil {
			return nil, derr
		}
		docSet = make(map[string]bool, len(docRanking.opIDs))
		for _, id := range docRanking.opIDs {
			meta, ok := docMetas[id]
			if !ok {
				continue
			}
			if opts.Service != "" && !strings.EqualFold(meta.serviceName, opts.Service) {
				continue
			}
			if opts.Method != "" && !strings.EqualFold(meta.method, opts.Method) {
				continue
			}
			if meta.deprecated && !opts.IncludeDeprecated {
				continue
			}
			docSet[id] = true
			docIDsFiltered = append(docIDsFiltered, id)
			filteredDocScore[id] = docRanking.rawScore[id]
		}
	}
	docFusionActive := fusionCfg.mode != docFusionOff && len(docIDsFiltered) > 0

	// preFusionRank/fusedRank (populated only when docFusionActive) let the
	// matched_on labelling below tell whether an operation "entered or
	// moved up through the docs ranker" (ground rule), independently of
	// whichever mode (rrf/weighted) produced the final ranking.
	var preFusionRank, fusedRank map[string]int

	if len(combined) == 0 && len(semHits) == 0 && len(docIDsFiltered) == 0 {
		return nil, nil
	}

	idSet := make(map[string]bool, len(combined)+len(semHits))
	ids := make([]string, 0, len(combined)+len(semHits))
	for _, id := range lexicalIDs {
		idSet[id] = true
		ids = append(ids, id)
	}
	for id := range combined {
		// Feedback-only new candidates: present in combined (seeded at 0
		// above) but not part of the original lexicalIDs snapshot.
		if idSet[id] {
			continue
		}
		idSet[id] = true
		ids = append(ids, id)
	}
	for _, h := range semHits {
		if idSet[h.ID] {
			continue
		}
		idSet[h.ID] = true
		ids = append(ids, h.ID)
	}

	metas, err := s.loadOpMeta(ctx, ids)
	if err != nil {
		return nil, err
	}
	ops, err := loadOperations(ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	fieldsByOp, err := s.loadFieldLeaves(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Additive boosts (PLAN §16 / task spec steps 2-3), lexical hits only.
	// Every boost below is raw and unbounded — the whole result set is
	// normalized proportionally (best raw score -> 1.0) once, after filters
	// and the deprecated penalty, in finalizeLexicalScores. Nothing here
	// clamps an individual id's score.
	for _, id := range lexicalIDs {
		meta, ok := metas[id]
		if !ok {
			continue
		}
		if strings.EqualFold(query, meta.rawOpID) || strings.EqualFold(query, id) {
			combined[id] += 1.0
		}
		combined[id] += rawOpIDStemBoost(tokens, meta.rawOpID)
		combined[id] += serviceNameStemBoost(tokens, meta.serviceName)
		if op, ok := ops[id]; ok {
			combined[id] += tagConceptBoost(tokens, op)
			combined[id] += descriptionServiceStemBoost(tokens, op, meta.serviceName)
			combined[id] += fieldLeafBoost(tokens, op.Params, fieldsByOp[id])
		}
	}

	// Filters and the deprecated penalty — no per-id clamp.
	for id := range combined {
		meta, ok := metas[id]
		if !ok {
			delete(combined, id)
			continue
		}
		if opts.Service != "" && !strings.EqualFold(meta.serviceName, opts.Service) {
			delete(combined, id)
			continue
		}
		if opts.Method != "" && !strings.EqualFold(meta.method, opts.Method) {
			delete(combined, id)
			continue
		}
		if meta.deprecated && !opts.IncludeDeprecated {
			combined[id] *= 0.5
		}
	}
	taskRanked := make([]scoredID, 0, len(taskScores))
	for id, score := range taskScores {
		if _, ok := combined[id]; ok {
			taskRanked = append(taskRanked, scoredID{id: id, score: score})
		}
	}
	taskRanked = sortScored(taskRanked, len(taskRanked))
	taskIDsFiltered := make([]string, len(taskRanked))
	for idx, item := range taskRanked {
		taskIDsFiltered[idx] = item.id
	}

	// Apply the usage-feedback boost now that "the top score" (every other
	// boost and filter already applied, feedback not yet added) is known.
	// feedbackApplied records which ids actually carried a matching
	// feedback signal, for matched_on's "feedback" label below — tracked
	// independently of whether the capped boost came out to exactly 0 (a
	// query with no other candidates at all has topScore 0, so the cap is
	// 0 too, but the id still owes its presence in the result set to
	// feedback).
	feedbackApplied := map[string]bool{}
	if len(feedbackByOp) > 0 {
		topScore := 0.0
		for _, v := range combined {
			if v > topScore {
				topScore = v
			}
		}
		boostCap := feedbackBoostCap * topScore
		for id, agg := range feedbackByOp {
			if _, ok := combined[id]; !ok {
				continue // never a candidate, or filtered out just above
			}
			feedbackApplied[id] = true
			boost := feedbackWeight * agg.log1pSum
			if boost > boostCap {
				boost = boostCap
			}
			if boost > 0 {
				combined[id] += boost
			}
		}
	}

	var results []domain.SearchResult
	if !semanticActive {
		list := make([]scoredID, 0, len(combined))
		for id, score := range combined {
			list = append(list, scoredID{id: id, score: score})
		}

		if !docFusionActive {
			list = finalizeLexicalScores(list, limit, tokens, metas, ops)
			results, err = s.buildResults(ctx, list, tokens)
			if err != nil {
				return nil, err
			}
		} else {
			preFusionRank = rankIndex(finalizeLexicalScores(append([]scoredID{}, list...), len(list), tokens, metas, ops))

			var finalList []scoredID
			switch fusionCfg.mode {
			case docFusionRRF:
				lexRanked := sortScored(append([]scoredID{}, list...), len(list))
				finalList = rrf(idsOf(lexRanked), taskIDsFiltered, docIDsFiltered)
				if len(finalList) > 0 && finalList[0].score > 0 {
					top := finalList[0].score
					for i := range finalList {
						finalList[i].score /= top
					}
				}
			case docFusionWeighted:
				normBase := append([]scoredID{}, list...)
				normalizeScores(normBase)
				merged := weightedFuseScores(scoreMap(normBase), filteredDocScore, fusionCfg.weight)
				finalList = make([]scoredID, 0, len(merged))
				for id, sc := range merged {
					finalList = append(finalList, scoredID{id: id, score: sc})
				}
			}
			finalList = sortScored(finalList, limit)
			fusedRank = rankIndex(finalList)

			results, err = s.buildResults(ctx, finalList, tokens)
			if err != nil {
				return nil, err
			}
		}
	} else {
		// Fuse the lexical ranking (now including any feedback boost) with
		// the (filtered, same as lexical above) semantic ranking via
		// reciprocal-rank fusion, normalize the fused score so the top hit
		// is 1.0, and mark every fused-in semantic id.
		semSet := map[string]bool{}
		semIDs := make([]string, 0, len(semHits))
		for _, h := range semHits {
			if semSet[h.ID] {
				continue
			}
			meta, ok := metas[h.ID]
			if !ok {
				continue
			}
			if opts.Service != "" && !strings.EqualFold(meta.serviceName, opts.Service) {
				continue
			}
			if opts.Method != "" && !strings.EqualFold(meta.method, opts.Method) {
				continue
			}
			if meta.deprecated && !opts.IncludeDeprecated {
				continue
			}
			semSet[h.ID] = true
			semIDs = append(semIDs, h.ID)
		}

		lexRanked := make([]scoredID, 0, len(combined))
		for id, score := range combined {
			lexRanked = append(lexRanked, scoredID{id: id, score: score})
		}
		lexRanked = sortScored(lexRanked, len(lexRanked))
		lexIDs := make([]string, len(lexRanked))
		for i, sc := range lexRanked {
			lexIDs[i] = sc.id
		}

		var fused []scoredID
		if !docFusionActive {
			fused = rrf(lexIDs, semIDs, taskIDsFiltered)
		} else {
			preFusionRank = rankIndex(rrf(lexIDs, semIDs, taskIDsFiltered))
			switch fusionCfg.mode {
			case docFusionRRF:
				fused = rrf(lexIDs, semIDs, taskIDsFiltered, docIDsFiltered)
			case docFusionWeighted:
				base := rrf(lexIDs, semIDs, taskIDsFiltered)
				normalizeScores(base)
				merged := weightedFuseScores(scoreMap(base), filteredDocScore, fusionCfg.weight)
				fused = make([]scoredID, 0, len(merged))
				for id, sc := range merged {
					fused = append(fused, scoredID{id: id, score: sc})
				}
			}
		}
		if len(fused) > 0 && fused[0].score > 0 {
			top := fused[0].score
			for i := range fused {
				fused[i].score /= top
			}
		}
		fused = sortScored(fused, limit)
		if docFusionActive {
			fusedRank = rankIndex(fused)
		}

		results, err = s.buildResults(ctx, fused, tokens)
		if err != nil {
			return nil, err
		}
		for i := range results {
			if semSet[results[i].Operation.ID] {
				results[i].MatchedOn = append(results[i].MatchedOn, "semantic")
			}
		}
	}

	// Label doc_text/memory_text/feedback contributions (search ranking
	// tuning task): cheap per-column MATCH probes restricted to the
	// already-limited result ids, plus the feedbackApplied set computed
	// above.
	if len(results) > 0 {
		resultIDs := make([]string, len(results))
		for i, r := range results {
			resultIDs[i] = r.Operation.ID
		}
		docHits, err := s.columnMatchIDs(ctx, resultIDs, "doc_text", tokens)
		if err != nil {
			return nil, err
		}
		memHits, err := s.columnMatchIDs(ctx, resultIDs, "memory_text", tokens)
		if err != nil {
			return nil, err
		}
		for i := range results {
			id := results[i].Operation.ID
			if matches := taskMatches[id]; len(matches) > 0 {
				results[i].Tasks = matches
				for _, match := range matches {
					results[i].MatchedOn = appendMatchedOnUnique(results[i].MatchedOn, "task:"+match.ID)
				}
			}
			if docHits[id] {
				results[i].MatchedOn = append(results[i].MatchedOn, "docs")
			}
			if memHits[id] {
				results[i].MatchedOn = append(results[i].MatchedOn, "memories")
			}
			if feedbackApplied[id] {
				results[i].MatchedOn = append(results[i].MatchedOn, "feedback")
			}
			if docFusionActive && docFusionContributed(id, docSet, preFusionRank, fusedRank) {
				results[i].MatchedOn = appendMatchedOnUnique(results[i].MatchedOn, "docs")
			}
		}
	}

	return results, nil
}

// buildResults loads the full domain.Operation and field leaves for each
// scored id (preserving list's order/score) and computes MatchedOn.
func (s *Searcher) buildResults(ctx context.Context, list []scoredID, matchTokens []string) ([]domain.SearchResult, error) {
	ids := make([]string, len(list))
	for i, sc := range list {
		ids[i] = sc.id
	}

	ops, err := loadOperations(ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	fieldsByOp, err := s.loadFieldLeaves(ctx, ids)
	if err != nil {
		return nil, err
	}

	results := make([]domain.SearchResult, 0, len(list))
	for _, sc := range list {
		op, ok := ops[sc.id]
		if !ok {
			continue
		}
		results = append(results, domain.SearchResult{
			Operation: op,
			Score:     sc.score,
			MatchedOn: matchedOn(matchTokens, op, fieldsByOp[sc.id]),
		})
	}
	return results, nil
}
