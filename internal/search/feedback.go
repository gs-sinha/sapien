package search

import (
	"context"
	"math"

	"github.com/gs-sinha/sapien/internal/textutil"
)

// feedbackWeight scales a matching operation's total log1p(count) usage
// signal (summed across every one of the query's tokens that has a
// search_feedback row for it) into an additive score boost. Kept well
// below the per-token additive boosts in match.go (which top out around
// 0.1-0.3 each) so a handful of past uses nudges ranking rather than
// swamping fresh lexical signal.
const feedbackWeight = 0.35

// feedbackBoostCap limits how far the feedback boost alone can move any
// one operation's score, expressed as a fraction of the query's own best
// (pre-feedback) lexical score: feedback can add at most
// feedbackBoostCap * topScore. A query with no real lexical signal at all
// has topScore == 0, so the cap is 0 too — feedback can win a close race,
// never manufacture relevance for a query an operation has nothing to do
// with.
const feedbackBoostCap = 0.3

// feedbackNewCandidateMinCount is the minimum total search_feedback.count
// (summed across the query's tokens) an operation needs, when it is not
// already a lexical/trigram/semantic candidate, to be added to the result
// set purely on the strength of past usage.
const feedbackNewCandidateMinCount = 3

// FeedbackTokens returns query's topic tokens the way
// internal/engine/local's usage-feedback recorder (search_feedback.go)
// stores them and the way applyFeedbackBoost below looks them back up:
// tokenized, stop words dropped, generic verbs dropped (they carry little
// discriminating power — see genericVerbs in match.go), deduplicated, in
// first-occurrence order, capped at max (max <= 0 means unlimited). Kept
// in this package (rather than duplicated in internal/engine/local) so the
// two sides of the feedback loop can never drift apart on what counts as
// a "topic" token.
func FeedbackTokens(query string, max int) []string {
	out := nonGenericTokens(filterStopWords(textutil.Tokens(query)))
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// nonGenericTokens filters genericVerbs out of tokens, deduplicating and
// preserving order.
func nonGenericTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t == "" || genericVerbs[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// feedbackRow is one search_feedback row.
type feedbackRow struct {
	term  string
	opID  string
	count int
}

// loadFeedback loads every search_feedback row whose term is one of terms.
func (s *Searcher) loadFeedback(ctx context.Context, terms []string) ([]feedbackRow, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	placeholders, args := placeholdersFor(terms)
	q := `SELECT term, op_id, count FROM search_feedback WHERE term IN (` + placeholders + `)`
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "load search feedback")
	}
	defer rows.Close()

	var out []feedbackRow
	for rows.Next() {
		var r feedbackRow
		if err := rows.Scan(&r.term, &r.opID, &r.count); err != nil {
			return nil, wrapf(err, "scan search feedback")
		}
		out = append(out, r)
	}
	return out, wrapf(rows.Err(), "search feedback rows")
}

// feedbackAgg is one operation's aggregated feedback signal for a query's
// tokens: log1pSum feeds the boost magnitude (feedbackWeight * log1pSum,
// capped); countTotal (the raw, un-logged sum) gates whether an operation
// with no lexical match at all still gets added as a candidate.
type feedbackAgg struct {
	log1pSum   float64
	countTotal int
}

// aggregateFeedback groups rows by operation.
func aggregateFeedback(rows []feedbackRow) map[string]feedbackAgg {
	out := map[string]feedbackAgg{}
	for _, r := range rows {
		a := out[r.opID]
		a.log1pSum += math.Log1p(float64(r.count))
		a.countTotal += r.count
		out[r.opID] = a
	}
	return out
}
