package search

import (
	"math"
	"sort"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/textutil"
)

// stopWords are dropped from a lexical query unless doing so would leave no
// tokens at all (task spec / PLAN §16).
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "for": true, "to": true,
	"in": true, "on": true, "with": true, "and": true, "or": true, "by": true,
	"me": true, "find": true, "get": true, "list": true, "all": true, "show": true,
}

// genericVerbs are query tokens carrying little discriminating power for
// ranking (they name an action almost every operation could plausibly
// support) rather than a domain concept. They are never dropped from the
// query — filterStopWords already drops "find"/"get"/"list"/"show" in the
// common case, but a query that is nothing but a generic verb still needs to
// search for it — but every additive boost below scales a generic verb
// token's contribution by genericVerbWeight instead of the full 1.0, so a
// domain noun ("allocation", "qcom") outweighs an incidental verb
// ("create", "test") when both appear in the same query (search ranking
// tuning task).
var genericVerbs = map[string]bool{
	"create": true, "get": true, "list": true, "find": true, "fetch": true,
	"show": true, "search": true, "test": true, "verify": true, "check": true,
	"make": true, "new": true, "run": true,
}

// genericVerbWeight is the contribution multiplier applied to a generic verb
// token in every additive lexical boost.
const genericVerbWeight = 0.3

// tokenWeight returns genericVerbWeight for a generic verb token, else 1.0
// (a domain noun/verb keeps full weight).
func tokenWeight(t string) float64 {
	if genericVerbs[t] {
		return genericVerbWeight
	}
	return 1.0
}

// stemMinRunes is the minimum shared-prefix length (in runes) for stemMatch
// to consider two tokens the same root. This is deliberately not a real
// stemmer (no LLM, no stemming library, per the task) — just enough to
// bridge a query noun to a differently-inflected identifier/text form of the
// same root (e.g. "allocation" ↔ "allocate", "orders" ↔ "order") without
// over-matching short, unrelated words.
const stemMinRunes = 5

// stemMatch reports whether a and b are the same token, or share a common
// prefix of at least stemMinRunes runes (in either order, since neither
// string need be a literal prefix of the other: "allocation" and "allocate"
// share "allocat" but neither contains the other).
func stemMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	n := 0
	for n < len(ra) && n < len(rb) && ra[n] == rb[n] {
		n++
	}
	return n >= stemMinRunes
}

// stemMatchAny reports whether token stem-matches any element of parts.
func stemMatchAny(token string, parts []string) bool {
	for _, p := range parts {
		if stemMatch(token, p) {
			return true
		}
	}
	return false
}

// filterStopWords drops stop words from tokens, keeping the original slice
// unchanged when every token is a stop word (so a query like "get" still
// searches for "get" rather than nothing).
func filterStopWords(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !stopWords[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return tokens
	}
	return out
}

// BuildMatch builds a safe FTS5 MATCH string from tokens. Every token is
// double-quoted so FTS5 syntax characters inside it (parens, colons, hyphens,
// etc.) are treated literally rather than as query syntax, and tokens of 3 or
// more runes get a trailing prefix "*" outside the quotes so "allocat"
// matches "allocate". mode selects the boolean operator joining tokens: "or"
// joins with OR, anything else (including "and") joins with AND. An empty (or
// all-empty) tokens slice returns "".
func BuildMatch(tokens []string, mode string) string {
	op := " AND "
	if strings.EqualFold(mode, "or") {
		op = " OR "
	}

	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t == "" {
			continue
		}
		parts = append(parts, quoteFTSTerm(t))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, op)
}

// quoteFTSTerm double-quotes a single token for literal FTS5 matching
// (doubling any embedded quote), appending a prefix "*" when the token has 3
// or more runes.
func quoteFTSTerm(t string) string {
	q := `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	if len([]rune(t)) >= 3 {
		q += "*"
	}
	return q
}

// quoteFTSLiteral double-quotes t for an exact (non-prefix) FTS5 match, e.g.
// for querying the trigram index where a prefix "*" has no useful meaning.
func quoteFTSLiteral(t string) string {
	return `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
}

// normalizeBM25 rescales raw bm25 scores (which SQLite returns as negative
// numbers, lower/more-negative meaning a better match) onto 0..1 within this
// result set, with the best match mapped to 1.0. An empty input returns an
// empty, non-nil map. A result set where every score is equal (including a
// single result) maps every id to 1.0.
func normalizeBM25(raw map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(raw))
	if len(raw) == 0 {
		return out
	}

	best, worst := math.Inf(1), math.Inf(-1)
	for _, v := range raw {
		if v < best {
			best = v
		}
		if v > worst {
			worst = v
		}
	}
	if worst == best {
		for id := range raw {
			out[id] = 1.0
		}
		return out
	}
	for id, v := range raw {
		out[id] = (worst - v) / (worst - best)
	}
	return out
}

// tagsConceptsTokens tokenizes op's tags and concepts exactly the way the
// catalog indexer builds the operations_fts "tags" column (op.Tags ++
// op.Concepts, space-joined, then textutil.Tokens'd) so a boost computed
// here agrees with what actually got indexed.
func tagsConceptsTokens(op domain.Operation) []string {
	combined := make([]string, 0, len(op.Tags)+len(op.Concepts))
	combined = append(combined, op.Tags...)
	combined = append(combined, op.Concepts...)
	if len(combined) == 0 {
		return nil
	}
	return textutil.Tokens(strings.Join(combined, " "))
}

// tagConceptBoost is task-spec boost (b): a token matching one of op's
// tags/concepts (as a substring of the tokenized tags+concepts text, or one
// of its identifier parts — e.g. "allocation" matching the concept "rider
// allocation") adds 0.25, scaled by the token's genericVerbWeight.
func tagConceptBoost(tokens []string, op domain.Operation) float64 {
	parts := tagsConceptsTokens(op)
	if len(parts) == 0 {
		return 0
	}
	var total float64
	for _, t := range tokens {
		if t != "" && stemMatchAny(t, parts) {
			total += 0.25 * tokenWeight(t)
		}
	}
	return total
}

// descriptionServiceStemBoost is task-spec boost (c): a token that (stem-)
// appears in op's summary/description AND whose stem is also one of the
// service name's identifier parts (e.g. "allocation" appears in "Allocate a
// rider..." and stem-matches "allocation-service") adds 0.15, scaled by the
// token's genericVerbWeight.
func descriptionServiceStemBoost(tokens []string, op domain.Operation, serviceName string) float64 {
	if serviceName == "" {
		return 0
	}
	serviceParts := textutil.Tokens(serviceName)
	textParts := textutil.Tokens(op.Summary + " " + op.Description)
	if len(serviceParts) == 0 || len(textParts) == 0 {
		return 0
	}
	var total float64
	for _, t := range tokens {
		if t == "" {
			continue
		}
		if stemMatchAny(t, textParts) && stemMatchAny(t, serviceParts) {
			total += 0.15 * tokenWeight(t)
		}
	}
	return total
}

// fieldLeafBoost is task-spec boost (d): a token matching a field leaf name
// or parameter name (reusing tokenMatchesName, exactly what MatchedOn's
// "field:" labels detect) adds 0.1 per matching token, scaled by the
// token's genericVerbWeight.
func fieldLeafBoost(tokens []string, params []domain.Param, fieldLeaves []string) float64 {
	var total float64
	for _, t := range tokens {
		if t == "" {
			continue
		}
		matched := false
		for _, p := range params {
			if tokenMatchesName([]string{t}, p.Name) {
				matched = true
				break
			}
		}
		if !matched {
			for _, leaf := range fieldLeaves {
				if tokenMatchesName([]string{t}, leaf) {
					matched = true
					break
				}
			}
		}
		if matched {
			total += 0.1 * tokenWeight(t)
		}
	}
	return total
}

// rawOpIDStemBoost is the existing "query token names this operation's raw
// operationId" boost, upgraded to stem-matching (so "allocation" credits
// rawOpID "allocate") and scaled per-token by genericVerbWeight.
func rawOpIDStemBoost(tokens []string, rawOpID string) float64 {
	if rawOpID == "" {
		return 0
	}
	parts := textutil.Tokens(rawOpID)
	var total float64
	for _, t := range tokens {
		if t != "" && stemMatchAny(t, parts) {
			total += 0.3 * tokenWeight(t)
		}
	}
	return total
}

// serviceNameStemBoost is the existing "query token names this operation's
// service" boost, upgraded to stem-matching and scaled per-token by
// genericVerbWeight.
func serviceNameStemBoost(tokens []string, serviceName string) float64 {
	if serviceName == "" {
		return 0
	}
	parts := textutil.Tokens(serviceName)
	var total float64
	for _, t := range tokens {
		if t != "" && stemMatchAny(t, parts) {
			total += 0.2 * tokenWeight(t)
		}
	}
	return total
}

// exactRawOpIDToken reports whether any of tokens is an exact (case-fold)
// match for rawOpID as a whole — the first key of the deterministic
// tie-break (search ranking tuning task).
func exactRawOpIDToken(tokens []string, rawOpID string) bool {
	if rawOpID == "" {
		return false
	}
	lc := strings.ToLower(rawOpID)
	for _, t := range tokens {
		if t == lc {
			return true
		}
	}
	return false
}

// pathLen returns the length (in runes) of op's HTTP path, or a sentinel
// larger than any real path for a non-HTTP operation, so it always sorts
// last on the "shorter path" tie-break key.
func pathLen(op domain.Operation) int {
	if op.HTTP == nil {
		return math.MaxInt32
	}
	return len([]rune(op.HTTP.Path))
}

// normalizeScores scales list's scores in place, proportionally, so the
// best raw score maps to 1.0 — never clamping an individual score — leaving
// relative order and margins intact. A non-positive best (e.g. every score
// is exactly 0, or list is empty) leaves scores unchanged.
func normalizeScores(list []scoredID) {
	if len(list) == 0 {
		return
	}
	best := list[0].score
	for _, sc := range list[1:] {
		if sc.score > best {
			best = sc.score
		}
	}
	if best <= 0 {
		return
	}
	for i := range list {
		list[i].score /= best
	}
}

// finalizeLexicalScores turns a set of raw, unbounded lexical scores into
// the final ranked, limited result list (search ranking tuning task, PLAN
// §16): scores are normalized proportionally so the best raw score maps to
// 1.0 (never clamped per-id), then sorted score descending with a
// deterministic tie-break — exact raw_op_id token match first, then shorter
// HTTP path, then id ascending — before truncating to limit.
func finalizeLexicalScores(list []scoredID, limit int, tokens []string, metas map[string]opMeta, ops map[string]domain.Operation) []scoredID {
	if len(list) == 0 {
		return list
	}

	normalizeScores(list)

	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		iExact := exactRawOpIDToken(tokens, metas[list[i].id].rawOpID)
		jExact := exactRawOpIDToken(tokens, metas[list[j].id].rawOpID)
		if iExact != jExact {
			return iExact
		}
		iPath, jPath := pathLen(ops[list[i].id]), pathLen(ops[list[j].id])
		if iPath != jPath {
			return iPath < jPath
		}
		return list[i].id < list[j].id
	})

	if len(list) > limit {
		list = list[:limit]
	}
	return list
}
