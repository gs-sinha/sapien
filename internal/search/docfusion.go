package search

// Docs-mediated second ranker (search ranking tuning task, docs-fusion
// experiment): an offline study (experiments/search-eval/REPORT.md) found
// that a plain BM25 over operation text enriched with the doc sections
// referencing each operation beat the shipped search on the user's real
// workspace, but folding that doc text into operations_fts as a weighted
// column (SAPIEN_SEARCH_KNOWLEDGE_WEIGHTS, query.go's knowledgeWeights)
// regressed as many queries as it fixed (experiments/search-eval/
// sweep_weights.sh). This file implements the alternative the study
// proposed instead: search docs_fts for the query, map its top sections to
// the operations they reference (doc_refs), and fuse that ranking with the
// operations ranking via reciprocal-rank fusion or a weighted blend — never
// touching operations_fts itself.
//
// Off by default (SAPIEN_SEARCH_DOC_FUSION=off/unset): Operations() behaves
// exactly as before this file existed.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/growsimplee/sapien/internal/domain"
)

// docFusionMode selects whether/how the docs ranker is fused in.
type docFusionMode int

const (
	docFusionOff docFusionMode = iota
	docFusionRRF
	docFusionWeighted
)

// docFusionScope selects the cross-talk control: whether a doc section
// votes for every operation doc_refs says it references ("all", default) or
// only those also named in the section's own heading or first paragraph
// ("heading"). See headingScopedOps for why this is a proxy, not an exact
// re-derivation.
type docFusionScope int

const (
	docFusionScopeAll docFusionScope = iota
	docFusionScopeHeading
)

// docFusionConfig is the fully-resolved configuration for one search:
// mode/weight from SAPIEN_SEARCH_DOC_FUSION, scope from
// SAPIEN_SEARCH_DOC_FUSION_SCOPE.
type docFusionConfig struct {
	mode   docFusionMode
	weight float64 // lexical weight, docFusionWeighted only; always 0..1
	scope  docFusionScope
}

// defaultDocFusionLexicalWeight is the shipped blend: 0.7 lexical, 0.3
// docs-mediated. Measured on experiments/search-eval (104 intents on a real
// four-service workspace): Recall@1 0.721 -> 0.798, MRR 0.791 -> 0.846, 14
// queries improved and 6 slipped (one lost first place), no category worse.
// 0.5 and 0.6 gained less and regressed more; 0.75 to 0.85 gained less.
const defaultDocFusionLexicalWeight = 0.7

// defaultDocFusionConfig is what an unset, empty, or malformed
// SAPIEN_SEARCH_DOC_FUSION resolves to.
func defaultDocFusionConfig() docFusionConfig {
	return docFusionConfig{mode: docFusionWeighted, weight: defaultDocFusionLexicalWeight}
}

// parseDocFusionConfig parses SAPIEN_SEARCH_DOC_FUSION's value:
//
//	"" (unset)           -> the shipped default, weighted:0.7
//	"off"                -> docFusionOff: the operation ranking alone
//	"rrf"                -> docFusionRRF (measured worse; kept for experiments)
//	"weighted:<w>"       -> docFusionWeighted, lexical weight w, 0 <= w <= 1
//
// Anything else, an unrecognized keyword or a malformed or out-of-range
// weight, resolves to the default, so a typo'd env var never silently
// changes ranking; only "off" turns fusion off.
func parseDocFusionConfig(v string) docFusionConfig {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return defaultDocFusionConfig()
	case strings.EqualFold(v, "off"):
		return docFusionConfig{mode: docFusionOff}
	case strings.EqualFold(v, "rrf"):
		return docFusionConfig{mode: docFusionRRF}
	case len(v) > len("weighted:") && strings.EqualFold(v[:len("weighted:")], "weighted:"):
		w, err := strconv.ParseFloat(strings.TrimSpace(v[len("weighted:"):]), 64)
		if err != nil || w < 0 || w > 1 {
			return defaultDocFusionConfig()
		}
		return docFusionConfig{mode: docFusionWeighted, weight: w}
	default:
		return defaultDocFusionConfig()
	}
}

// parseDocFusionScope parses SAPIEN_SEARCH_DOC_FUSION_SCOPE's value: "heading"
// (case-insensitive) selects docFusionScopeHeading; anything else (including
// unset) selects docFusionScopeAll, the shipped default.
func parseDocFusionScope(v string) docFusionScope {
	if strings.EqualFold(strings.TrimSpace(v), "heading") {
		return docFusionScopeHeading
	}
	return docFusionScopeAll
}

var (
	docFusionEnvOnce sync.Once
	docFusionEnvCfg  docFusionConfig

	// docFusionOverride, when non-nil, replaces the env-derived
	// configuration entirely. It exists solely so this package's own tests
	// can exercise Operations()'s fusion code paths deterministically:
	// SAPIEN_SEARCH_DOC_FUSION is read once per process (docFusionEnvOnce,
	// mirroring query.go's knowledgeWeights), which means a second Go test
	// in the same test binary setting a different env value would silently
	// see the first test's cached value — exactly the reason
	// query.go/knowledgeWeights itself is only ever validated by spawning
	// separate `sapien` processes (experiments/search-eval/sweep_weights.sh)
	// rather than by Go subtests. Production code (search.New) never sets
	// this; real callers always go through the env var.
	docFusionOverride *docFusionConfig
)

// effectiveDocFusionConfig returns docFusionOverride if set, else the
// env-derived configuration (each half read once).
func effectiveDocFusionConfig() docFusionConfig {
	if docFusionOverride != nil {
		return *docFusionOverride
	}
	docFusionEnvOnce.Do(func() {
		docFusionEnvCfg = parseDocFusionConfig(os.Getenv("SAPIEN_SEARCH_DOC_FUSION"))
		docFusionEnvCfg.scope = parseDocFusionScope(os.Getenv("SAPIEN_SEARCH_DOC_FUSION_SCOPE"))
	})
	return docFusionEnvCfg
}

// docFusionMinTokens is the minimum number of non-generic query tokens
// required before the docs ranker runs at all (ground rule: bound the extra
// work — a query that is one bare noun, or nothing but generic verbs, is too
// coarse for a second, docs-shaped ranking pass to add cheap signal).
const docFusionMinTokens = 2

// docFusionSectionLimit bounds the one extra docs_fts query doc fusion
// issues per operations search to its top docFusionSectionLimit sections
// (ground rule: one docs query, limit 15).
const docFusionSectionLimit = 15

// docOpRanking is the docs ranker's view of a query: opIDs is the distinct
// operation ids it surfaces, ranked best-first (for RRF); rawScore is each
// operation's raw docs-ranker score — the reciprocal rank (1/(rank+1)) of
// the best (highest-bm25) section that named it, already in (0,1] with the
// best-named operation at 1.0 — for the weighted variant.
type docOpRanking struct {
	opIDs    []string
	rawScore map[string]float64
}

// docFusionRanking runs the bounded, single docs_fts query and maps its top
// docFusionSectionLimit sections to the operations they reference
// (doc_refs), producing a ranking a plain FTS5 bm25 pass over operations_fts
// can't see directly: a section mentioning an operation only in passing, a
// worked example, a migration note. Returns a zero-value docOpRanking (not
// an error) when there is nothing to fuse: fewer than docFusionMinTokens
// non-generic tokens, no docs hit at all, or none of the hit sections
// reference any operation.
func (s *Searcher) docFusionRanking(ctx context.Context, tokens []string, scope docFusionScope) (docOpRanking, error) {
	if len(nonGenericTokens(tokens)) < docFusionMinTokens {
		return docOpRanking{}, nil
	}

	match := BuildMatch(tokens, "or")
	if match == "" {
		return docOpRanking{}, nil
	}

	// The one docs query the ground rules allow: broadest (OR) match so a
	// query missing one token still surfaces sections about the rest,
	// ranked by the same bm25 weights docs.go's ftsDocsRaw uses, capped at
	// docFusionSectionLimit.
	q := `SELECT section_id FROM docs_fts
	      WHERE docs_fts MATCH ?
	      ORDER BY bm25(docs_fts, 0, 1, 3, 4, 1)
	      LIMIT ?`
	rows, err := s.db.SQL().QueryContext(ctx, q, match, docFusionSectionLimit)
	if err != nil {
		return docOpRanking{}, wrapf(err, "doc fusion query")
	}
	var sectionIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return docOpRanking{}, wrapf(err, "scan doc fusion section")
		}
		sectionIDs = append(sectionIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return docOpRanking{}, wrapf(err, "doc fusion rows")
	}
	rows.Close()

	if len(sectionIDs) == 0 {
		return docOpRanking{}, nil
	}

	refsBySection, err := s.loadDocRefs(ctx, sectionIDs)
	if err != nil {
		return docOpRanking{}, err
	}

	var allowed map[string]map[string]bool
	if scope == docFusionScopeHeading {
		allowed, err = s.headingScopedOps(ctx, sectionIDs, refsBySection)
		if err != nil {
			return docOpRanking{}, err
		}
	}

	return buildDocOpRanking(sectionIDs, refsBySection, allowed), nil
}

// buildDocOpRanking maps ranked doc sections (best/rank-0 first) to the
// operations they reference (kind RefOperation), assigning each
// newly-encountered operation the reciprocal rank of the best section that
// named it (a section further down the ranking never demotes an operation
// a better section already named). When allowed is non-nil (the cross-talk
// control), a section's votes are restricted to allowed[sectionID][opID];
// a nil allowed means every doc_refs operation reference counts — the
// default "all" scope. Pure and DB-free so it's directly unit-testable.
func buildDocOpRanking(sectionIDs []string, refsBySection map[string][]domain.DocRef, allowed map[string]map[string]bool) docOpRanking {
	out := docOpRanking{rawScore: map[string]float64{}}
	seen := map[string]bool{}
	for rank, secID := range sectionIDs {
		var allowedForSec map[string]bool
		if allowed != nil {
			allowedForSec = allowed[secID]
		}
		for _, r := range refsBySection[secID] {
			if r.Kind != domain.RefOperation {
				continue
			}
			if allowed != nil && !allowedForSec[r.Value] {
				continue
			}
			if seen[r.Value] {
				continue
			}
			seen[r.Value] = true
			out.opIDs = append(out.opIDs, r.Value)
			out.rawScore[r.Value] = 1.0 / (float64(rank) + 1.0)
		}
	}
	return out
}

// headingScopedOps implements the cross-talk control variant: a section
// only votes for the operations doc_refs says it references when that
// operation is also named in the section's own heading or first paragraph
// of body text, rather than anywhere in a (possibly long) section body.
//
// doc_refs, as persisted (section_id, kind, value —
// internal/store/migrations/001_init.sql), carries no position at all:
// ExtractRefs computes each match's byte offset only to order and
// deduplicate matches within one section (internal/ingest/docs/refs.go's
// unexported "found.pos"), then discards it before the caller writes
// doc_refs rows (internal/catalog/apply.go). So doc_refs alone cannot tell
// us where in a section a reference occurred — the ground rule's "if
// doc_refs carries enough information to tell" — it does not.
//
// Exactly re-deriving it would mean rebuilding docs.KnownRefs (operation
// ids, path/method aliases, schema names, and service.yaml concepts —
// internal/registry/builder.go's buildKnownRefs) and re-running
// docs.ExtractRefs restricted to each section's heading+first-paragraph
// text. That data is assembled once, at ingest time, from the source
// service packages (schemas and service.yaml concepts in particular have no
// catalog table internal/search can query), so it isn't available to
// reconstruct here.
//
// What is available at query time is doc_sections.heading/body (already
// loaded elsewhere in this package for FTS/snippet purposes) and, for each
// doc_refs candidate operation, its raw_op_id and HTTP path (opMeta /
// domain.Operation, already queried by every other path in this package).
// This function uses those as a proxy for "named here": an operation counts
// as named in a section's heading/first-paragraph when its raw operation
// id, its full operation id, or its HTTP path appears as a literal,
// case-insensitive substring there — the same two signals ExtractRefs
// itself keys off for a RefOperation match (an operation-id mention, or a
// resolvable METHOD/path mention), just checked positionally instead of
// re-run through its regex machinery.
func (s *Searcher) headingScopedOps(ctx context.Context, sectionIDs []string, refsBySection map[string][]domain.DocRef) (map[string]map[string]bool, error) {
	opSet := map[string]bool{}
	for _, refs := range refsBySection {
		for _, r := range refs {
			if r.Kind == domain.RefOperation {
				opSet[r.Value] = true
			}
		}
	}
	if len(opSet) == 0 {
		return map[string]map[string]bool{}, nil
	}
	opIDs := make([]string, 0, len(opSet))
	for id := range opSet {
		opIDs = append(opIDs, id)
	}

	metas, err := s.loadOpMeta(ctx, opIDs)
	if err != nil {
		return nil, err
	}
	ops, err := loadOperations(ctx, s.db, opIDs)
	if err != nil {
		return nil, err
	}

	headings, firstParas, err := s.loadHeadingAndFirstPara(ctx, sectionIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string]map[string]bool, len(sectionIDs))
	for _, secID := range sectionIDs {
		text := strings.ToLower(headings[secID] + "\n" + firstParas[secID])
		allowed := map[string]bool{}
		for _, r := range refsBySection[secID] {
			if r.Kind != domain.RefOperation {
				continue
			}
			if operationNamedIn(text, r.Value, metas[r.Value], ops[r.Value]) {
				allowed[r.Value] = true
			}
		}
		out[secID] = allowed
	}
	return out, nil
}

// operationNamedIn reports whether op (identified by opID/meta/op) is
// literally named in lowerText (already lower-cased): via its raw
// operation id, its full id, or its HTTP path.
func operationNamedIn(lowerText, opID string, meta opMeta, op domain.Operation) bool {
	if lowerText == "" {
		return false
	}
	if meta.rawOpID != "" && strings.Contains(lowerText, strings.ToLower(meta.rawOpID)) {
		return true
	}
	if opID != "" && strings.Contains(lowerText, strings.ToLower(opID)) {
		return true
	}
	if op.HTTP != nil && op.HTTP.Path != "" && strings.Contains(lowerText, strings.ToLower(op.HTTP.Path)) {
		return true
	}
	return false
}

// loadHeadingAndFirstPara loads each section's heading and the first
// paragraph of its body (the text up to the first blank line). Cheap: keyed
// to sectionIDs, already bounded to docFusionSectionLimit.
func (s *Searcher) loadHeadingAndFirstPara(ctx context.Context, sectionIDs []string) (map[string]string, map[string]string, error) {
	headings := map[string]string{}
	firstParas := map[string]string{}
	if len(sectionIDs) == 0 {
		return headings, firstParas, nil
	}
	placeholders, args := placeholdersFor(sectionIDs)
	q := `SELECT id, COALESCE(heading, ''), body FROM doc_sections WHERE id IN (` + placeholders + `)`
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, wrapf(err, "load doc section heading/body")
	}
	defer rows.Close()
	for rows.Next() {
		var id, heading, body string
		if err := rows.Scan(&id, &heading, &body); err != nil {
			return nil, nil, wrapf(err, "scan doc section heading/body")
		}
		headings[id] = heading
		firstParas[id] = firstParagraph(body)
	}
	return headings, firstParas, wrapf(rows.Err(), "doc section heading/body rows")
}

// firstParagraph returns the text up to the first blank line (a double
// newline), trimmed — a cheap proxy for "the section's lead sentence(s)".
func firstParagraph(body string) string {
	if i := strings.Index(body, "\n\n"); i >= 0 {
		return strings.TrimSpace(body[:i])
	}
	return strings.TrimSpace(body)
}

// ---- fusion arithmetic (pure; unit-tested directly) ----

// idsOf returns just the ids of list, in order.
func idsOf(list []scoredID) []string {
	out := make([]string, len(list))
	for i, sc := range list {
		out[i] = sc.id
	}
	return out
}

// scoreMap turns list into an id -> score map.
func scoreMap(list []scoredID) map[string]float64 {
	out := make(map[string]float64, len(list))
	for _, sc := range list {
		out[sc.id] = sc.score
	}
	return out
}

// rankIndex returns each id's 0-based position in ranked.
func rankIndex(ranked []scoredID) map[string]int {
	out := make(map[string]int, len(ranked))
	for i, sc := range ranked {
		out[sc.id] = i
	}
	return out
}

// weightedFuseScores combines base (the pre-fusion ranking's score,
// proportionally normalized so its best is 1.0) and doc (the docs ranker's
// reciprocal-rank score, already at most 1.0 by construction) per operation
// id: score = lexWeight*base + (1-lexWeight)*doc, over the union of both
// maps' keys — an id present in only one side gets 0 from the other, it is
// not treated as a missing candidate. lexWeight is clamped to [0,1] as a
// safety net (parseDocFusionConfig already guarantees this for the env-var
// path; the clamp only protects a caller that builds docFusionConfig some
// other way, e.g. a test).
func weightedFuseScores(base, doc map[string]float64, lexWeight float64) map[string]float64 {
	if lexWeight < 0 {
		lexWeight = 0
	}
	if lexWeight > 1 {
		lexWeight = 1
	}
	out := make(map[string]float64, len(base)+len(doc))
	for id, v := range base {
		out[id] = lexWeight * v
	}
	for id, v := range doc {
		out[id] += (1 - lexWeight) * v
	}
	return out
}

// docFusionContributed reports whether id's presence/position in the final,
// fused result set is attributable to the docs ranker: either id was never
// a candidate at all pre-fusion ("entered"), or its fused rank beats the
// rank it held pre-fusion ("moved up"). docOpSet is the docs ranker's own
// (filtered) candidate set for this query — an id absent from it never
// counts, even if it happens to also rank better for unrelated reasons
// (feedback, semantic fusion).
func docFusionContributed(id string, docOpSet map[string]bool, preFusionRank, fusedRank map[string]int) bool {
	if !docOpSet[id] {
		return false
	}
	fr, fok := fusedRank[id]
	if !fok {
		return false
	}
	lr, lok := preFusionRank[id]
	if !lok {
		return true // entered: no pre-fusion candidate at all
	}
	return fr < lr // moved up
}

// appendMatchedOnUnique appends label to list unless it's already present
// (MatchedOn's own "docs" label, from a doc_text column hit — query.go's
// columnMatchIDs — can otherwise collide with the docs-ranker's use of the
// same label below).
func appendMatchedOnUnique(list []string, label string) []string {
	for _, existing := range list {
		if existing == label {
			return list
		}
	}
	return append(list, label)
}
