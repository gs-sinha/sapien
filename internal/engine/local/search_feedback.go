// This file implements Local's usage-feedback loop (search ranking tuning
// task, part 2 of PLAN.md §16): every Search().Operations call is recorded
// into a small
// in-memory ring per *Local; whenever an operation is actually *used*
// (inspected via Catalog().GetOperation/ResolveOperation, referenced by a
// saved/updated flow, or called via Runner().Call), noteOperationUse looks
// back over the last 10 minutes of recorded searches and, for every one
// whose results contained that operation, upserts a search_feedback row
// per query token. internal/search reads that table back to boost lexical
// ranking (internal/search/feedback.go) and label matched_on "feedback".
//
// The ring lives in a package-level registry keyed by *Local's pointer
// identity rather than as a field on Local itself: this task owns only
// specific hooks inside search.go/catalog.go/memories.go/flows.go/
// runner.go (plus this new file), not local.go, where the Local struct
// itself is defined. The registry is never explicitly cleaned up on
// Close (also not owned here), so it pins a small, bounded (searchRingCap
// entries) object per opened *Local for the life of the process — a known,
// accepted cost given the file ownership split; see this task's report.
package local

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/search"
)

// searchRingCap bounds how many recent searches each *Local remembers.
const searchRingCap = 50

// feedbackLookback is how far back noteOperationUse looks for searches
// whose results contained the used operation.
const feedbackLookback = 10 * time.Minute

// maxFeedbackQueryTokens caps how many of a query's (non-generic) tokens
// are recorded per search.
const maxFeedbackQueryTokens = 8

// maxFeedbackResultIDs caps how many of a search's result operation ids
// are recorded per search.
const maxFeedbackResultIDs = 10

// searchRecord is one remembered search.
type searchRecord struct {
	tokens    []string
	resultIDs []string
	at        time.Time
}

// searchRing is a small, mutex-protected FIFO of up to searchRingCap
// searchRecords.
type searchRing struct {
	mu      sync.Mutex
	entries []searchRecord
}

func (r *searchRing) add(rec searchRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, rec)
	if len(r.entries) > searchRingCap {
		r.entries = r.entries[len(r.entries)-searchRingCap:]
	}
}

// snapshot returns a copy of the ring's current entries, so callers never
// hold the ring's lock while doing I/O (a DB write, in noteOperationUse's
// case).
func (r *searchRing) snapshot() []searchRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]searchRecord, len(r.entries))
	copy(out, r.entries)
	return out
}

var (
	feedbackRingsMu sync.Mutex
	feedbackRings   = map[*Local]*searchRing{}
)

// ringFor returns (creating if necessary) l's search ring.
func ringFor(l *Local) *searchRing {
	feedbackRingsMu.Lock()
	defer feedbackRingsMu.Unlock()
	r, ok := feedbackRings[l]
	if !ok {
		r = &searchRing{}
		feedbackRings[l] = r
	}
	return r
}

// recordSearch remembers one Search().Operations call in l's ring: query's
// non-generic tokens (search.FeedbackTokens, lowercased, capped) and the
// top result operation ids, capped. A query with no non-generic tokens at
// all (e.g. a bare path lookup, or "list") records nothing — there is no
// stable "topic" to later credit an operation's use to.
func recordSearch(l *Local, query string, results []domain.SearchResult) {
	tokens := search.FeedbackTokens(query, maxFeedbackQueryTokens)
	if len(tokens) == 0 {
		return
	}
	ids := make([]string, 0, maxFeedbackResultIDs)
	for _, r := range results {
		ids = append(ids, r.Operation.ID)
		if len(ids) >= maxFeedbackResultIDs {
			break
		}
	}
	ringFor(l).add(searchRecord{tokens: tokens, resultIDs: ids, at: time.Now().UTC()})
}

// noteOperationUse is called whenever opID is used directly (Catalog's
// GetOperation/ResolveOperation, a flow step's operation, or Runner.Call):
// it looks at l's recent (within feedbackLookback) searches whose results
// contained opID and upserts search_feedback (count += 1 per matching
// search) for each of that search's tokens. Best effort: a DB error is
// logged and swallowed, never returned or surfaced to the caller — usage
// feedback is a ranking nicety, not something worth failing a real
// operation over.
func noteOperationUse(ctx context.Context, l *Local, opID string) {
	if opID == "" {
		return
	}
	entries := ringFor(l).snapshot()
	if len(entries) == 0 {
		return
	}

	cutoff := time.Now().UTC().Add(-feedbackLookback)
	increments := map[string]int{}
	for _, e := range entries {
		if e.at.Before(cutoff) {
			continue
		}
		if !containsString(e.resultIDs, opID) {
			continue
		}
		seen := map[string]bool{}
		for _, tok := range e.tokens {
			if tok == "" || seen[tok] {
				continue
			}
			seen[tok] = true
			increments[tok]++
		}
	}
	if len(increments) == 0 {
		return
	}

	if err := upsertSearchFeedback(ctx, l, opID, increments); err != nil {
		l.logger.Warn("search feedback: recording operation use failed", "operation", opID, "error", err)
	}
}

// containsString reports whether s is in list.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// upsertSearchFeedback increments search_feedback.count for (term, opID)
// by increments[term], for every term, inside one write transaction.
func upsertSearchFeedback(ctx context.Context, l *Local, opID string, increments map[string]int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return l.db.Write(ctx, func(tx *sql.Tx) error {
		for term, inc := range increments {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO search_feedback (term, op_id, count, updated)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(term, op_id) DO UPDATE SET
					count = count + excluded.count,
					updated = excluded.updated
			`, term, opID, inc, now); err != nil {
				return fmt.Errorf("engine/local: upsert search_feedback %q/%q: %w", term, opID, err)
			}
		}
		return nil
	})
}

// dropRing forgets l's search ring; Close calls it so a closed engine does
// not pin its ring for the life of the process.
func dropRing(l *Local) {
	feedbackRingsMu.Lock()
	delete(feedbackRings, l)
	feedbackRingsMu.Unlock()
}
