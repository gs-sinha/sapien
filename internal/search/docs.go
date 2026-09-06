package search

import (
	"context"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// docHit accumulates a doc section's running score and its FTS snippet.
type docHit struct {
	score   float64
	snippet string
}

// docMeta is a doc section's display metadata.
type docMeta struct {
	serviceName string
	docID       string
	docPath     string
	title       string
	heading     string
}

// Docs searches the documentation catalog at section granularity (PLAN.md
// §16): FTS5 over docs_fts, boosted when a section references (doc_refs) an
// operation that also appears in this same query's top-5 Operations()
// results. An empty (or whitespace-only) query returns (nil, nil).
func (s *Searcher) Docs(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, nil
	}

	limit := effectiveLimit(opts.Limit)

	allTokens := textutil.Tokens(trimmed)
	if len(allTokens) == 0 {
		return nil, nil
	}
	tokens := filterStopWords(allTokens)

	combined := map[string]*docHit{}

	andRaw, andSnip, err := s.ftsDocsRaw(ctx, BuildMatch(tokens, "and"))
	if err != nil {
		return nil, err
	}
	for id, v := range normalizeBM25(andRaw) {
		combined[id] = &docHit{score: v, snippet: andSnip[id]}
	}

	if len(andRaw) < limit {
		orRaw, orSnip, err := s.ftsDocsRaw(ctx, BuildMatch(tokens, "or"))
		if err != nil {
			return nil, err
		}
		for id, v := range normalizeBM25(orRaw) {
			if h, ok := combined[id]; ok {
				h.score += v * 0.6
			} else {
				combined[id] = &docHit{score: v * 0.6, snippet: orSnip[id]}
			}
		}
	}

	// lexicalIDs snapshots combined's keys before any semantic ids are
	// mixed into the wider id set below (see the analogous comment in
	// lexicalLookup).
	lexicalIDs := make([]string, 0, len(combined))
	for id := range combined {
		lexicalIDs = append(lexicalIDs, id)
	}

	// Optional semantic fusion (PLAN §16): fetched before the "nothing
	// matched" check, so a query lexical search misses entirely can still
	// surface semantic-only doc hits.
	var semHits []SemanticHit
	if s.sem != nil {
		var serr error
		semHits, serr = s.sem.Query(ctx, "doc", trimmed, limit*2)
		if serr != nil {
			return nil, serr
		}
	}

	if len(combined) == 0 && len(semHits) == 0 {
		return nil, nil
	}

	idSet := make(map[string]bool, len(lexicalIDs)+len(semHits))
	ids := make([]string, 0, len(lexicalIDs)+len(semHits))
	for _, id := range lexicalIDs {
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

	refsBySection, err := s.loadDocRefs(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Ref boost: a section referencing an operation this same query's top-5
	// Operations() results contains gets +0.5.
	opHits, err := s.Operations(ctx, trimmed, domain.SearchOptions{Service: opts.Service, Limit: 5, IncludeDeprecated: true})
	if err != nil {
		return nil, err
	}
	opIDs := make(map[string]bool, len(opHits))
	for _, h := range opHits {
		opIDs[h.Operation.ID] = true
	}
	for id, refs := range refsBySection {
		h, ok := combined[id]
		if !ok {
			continue
		}
		for _, r := range refs {
			if r.Kind == domain.RefOperation && opIDs[r.Value] {
				h.score += 0.5
				break
			}
		}
	}

	metas, err := s.loadDocSectionMeta(ctx, ids)
	if err != nil {
		return nil, err
	}

	if s.sem == nil {
		list := make([]scoredID, 0, len(combined))
		for id, h := range combined {
			meta, ok := metas[id]
			if !ok {
				continue
			}
			if opts.Service != "" && !strings.EqualFold(meta.serviceName, opts.Service) {
				continue
			}
			list = append(list, scoredID{id: id, score: h.score})
		}
		// Normalize proportionally (best raw score -> 1.0) rather than
		// clamping each id at 1.0, so ties introduced by clamping don't
		// flatten the ranking margin (same fix as Operations(), PLAN §16).
		normalizeScores(list)
		list = sortScored(list, limit)

		return s.buildDocResults(list, metas, combined, refsBySection), nil
	}

	// Fuse the lexical ranking with the (filtered, same as lexical above)
	// semantic ranking via reciprocal-rank fusion, normalized so the top hit
	// is 1.0. domain.DocSearchResult has no MatchedOn-style field to mark
	// which hits came from semantic search (unlike domain.SearchResult),
	// so unlike Operations() there is nothing to append here.
	semIDs := make([]string, 0, len(semHits))
	seenSem := map[string]bool{}
	for _, h := range semHits {
		if seenSem[h.ID] {
			continue
		}
		meta, ok := metas[h.ID]
		if !ok {
			continue
		}
		if opts.Service != "" && !strings.EqualFold(meta.serviceName, opts.Service) {
			continue
		}
		seenSem[h.ID] = true
		semIDs = append(semIDs, h.ID)
	}

	// Ranked by raw (unclamped) score: clamping two hits to the same 1.0
	// ceiling here would tie their rank and distort the reciprocal-rank
	// fusion below, which cares about relative order, not magnitude.
	lexRanked := make([]scoredID, 0, len(combined))
	for id, h := range combined {
		lexRanked = append(lexRanked, scoredID{id: id, score: h.score})
	}
	lexRanked = sortScored(lexRanked, len(lexRanked))
	lexIDs := make([]string, len(lexRanked))
	for i, sc := range lexRanked {
		lexIDs[i] = sc.id
	}

	fused := rrf(lexIDs, semIDs)
	if len(fused) > 0 && fused[0].score > 0 {
		top := fused[0].score
		for i := range fused {
			fused[i].score /= top
		}
	}
	fused = sortScored(fused, limit)

	return s.buildDocResults(fused, metas, combined, refsBySection), nil
}

// buildDocResults renders one domain.DocSearchResult per scored id, using
// meta for display fields, combined for the FTS snippet when a lexical hit
// produced one (a semantic-only hit has none), and refsBySection for Refs.
func (s *Searcher) buildDocResults(list []scoredID, metas map[string]docMeta, combined map[string]*docHit, refsBySection map[string][]domain.DocRef) []domain.DocSearchResult {
	results := make([]domain.DocSearchResult, 0, len(list))
	for _, sc := range list {
		meta, ok := metas[sc.id]
		if !ok {
			continue
		}
		var snippet string
		if h, ok := combined[sc.id]; ok {
			snippet = h.snippet
		}
		results = append(results, domain.DocSearchResult{
			Service:   meta.serviceName,
			DocID:     meta.docID,
			Path:      meta.docPath,
			Title:     meta.title,
			SectionID: sc.id,
			Heading:   meta.heading,
			Snippet:   snippet,
			Refs:      refsBySection[sc.id],
			Score:     sc.score,
		})
	}
	return results
}

// ftsDocsRaw runs match against docs_fts and returns the raw bm25 score and
// an FTS5 snippet (highlighting body hits) per section id.
func (s *Searcher) ftsDocsRaw(ctx context.Context, match string) (map[string]float64, map[string]string, error) {
	scores := map[string]float64{}
	snippets := map[string]string{}
	if match == "" {
		return scores, snippets, nil
	}

	// Column order: section_id, service, title, heading, body (task spec's
	// exact bm25/snippet calls; body is column index 4).
	q := `SELECT section_id, bm25(docs_fts, 0, 1, 3, 4, 1) AS score,
	             snippet(docs_fts, 4, '[', ']', '…', 12)
	      FROM docs_fts WHERE docs_fts MATCH ?`
	rows, err := s.db.SQL().QueryContext(ctx, q, match)
	if err != nil {
		return nil, nil, wrapf(err, "docs fts query")
	}
	defer rows.Close()

	for rows.Next() {
		var id, snip string
		var score float64
		if err := rows.Scan(&id, &score, &snip); err != nil {
			return nil, nil, wrapf(err, "scan docs fts row")
		}
		scores[id] = score
		snippets[id] = snip
	}
	return scores, snippets, wrapf(rows.Err(), "docs fts rows")
}

// loadDocRefs loads every doc_refs row for the given section ids.
func (s *Searcher) loadDocRefs(ctx context.Context, ids []string) (map[string][]domain.DocRef, error) {
	out := map[string][]domain.DocRef{}
	if len(ids) == 0 {
		return out, nil
	}

	placeholders, args := placeholdersFor(ids)
	q := `SELECT section_id, kind, value FROM doc_refs WHERE section_id IN (` + placeholders + `)`
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "load doc refs")
	}
	defer rows.Close()

	for rows.Next() {
		var sectionID, kind, value string
		if err := rows.Scan(&sectionID, &kind, &value); err != nil {
			return nil, wrapf(err, "scan doc ref")
		}
		out[sectionID] = append(out[sectionID], domain.DocRef{Kind: domain.RefKind(kind), Value: value})
	}
	return out, wrapf(rows.Err(), "load doc refs rows")
}

// loadDocSectionMeta loads display metadata for each doc section id.
func (s *Searcher) loadDocSectionMeta(ctx context.Context, ids []string) (map[string]docMeta, error) {
	out := map[string]docMeta{}
	if len(ids) == 0 {
		return out, nil
	}

	placeholders, args := placeholdersFor(ids)
	q := `SELECT ds.id, COALESCE(ds.heading, ''), d.id, COALESCE(d.path, ''), COALESCE(d.title, ''), sv.name
	      FROM doc_sections ds
	      JOIN docs d ON d.id = ds.doc_id
	      JOIN services sv ON sv.id = d.service_id
	      WHERE ds.id IN (` + placeholders + `)`
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "load doc section meta")
	}
	defer rows.Close()

	for rows.Next() {
		var sectionID, heading, docID, docPath, title, serviceName string
		if err := rows.Scan(&sectionID, &heading, &docID, &docPath, &title, &serviceName); err != nil {
			return nil, wrapf(err, "scan doc section meta")
		}
		out[sectionID] = docMeta{serviceName: serviceName, docID: docID, docPath: docPath, title: title, heading: heading}
	}
	return out, wrapf(rows.Err(), "load doc section meta rows")
}
