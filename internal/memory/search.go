package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/store"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// Scoring weights (PLAN.md §13): score = structural + w_l*bm25_norm +
// w_p*provenance + w_r*recency. There is no vector/semantic term: sqlite-vec
// hybrid search is optional and not implemented by this package (PLAN §16
// says it's off by default), so w_v*cosine is always 0.
const (
	weightLexical    = 0.6
	weightProvenance = 0.2
	weightRecency    = 0.05

	recencyHalfLifeDays = 90.0
)

// Structural tiers (PLAN.md §13).
const (
	tierOperationField = 1.0
	tierOperation      = 0.8
	tierSchema         = 0.7
	tierError          = 0.6
	tierFlow           = 0.5
	tierConcept        = 0.5
	tierService        = 0.4
	tierErrorFallback  = 0.4
	tierTagOverlap     = 0.3
	tierMisc           = 0.3
)

// candidate accumulates a memory's structural score and reasons across
// however many subjects/expansions matched it, plus (for Search) its
// normalized lexical score.
type candidate struct {
	structural float64
	reasons    []string
	reasonSet  map[string]bool
	bm25       float64
}

func (c *candidate) bump(tier float64, reason string) {
	if tier > c.structural {
		c.structural = tier
	}
	if c.reasonSet == nil {
		c.reasonSet = map[string]bool{}
	}
	if !c.reasonSet[reason] {
		c.reasonSet[reason] = true
		c.reasons = append(c.reasons, reason)
	}
}

func getOrCreateCandidate(cands map[string]*candidate, id string) *candidate {
	c, ok := cands[id]
	if !ok {
		c = &candidate{}
		cands[id] = c
	}
	return c
}

// Relevant is structural-first retrieval (PLAN §13, §14): no lexical
// component, just the union of structural candidates each subject expands to
// (its own kind, plus - for an operation subject - its service, the schemas
// it uses, and the flows that call it, via the Resolver). Excludes inactive
// memories. Ordered by score descending, limited to limit (default 15).
func (s *Store) Relevant(ctx context.Context, subjects []domain.Subject, limit int) ([]domain.ScoredMemory, error) {
	if limit <= 0 {
		limit = 15
	}
	cands := map[string]*candidate{}
	for _, subj := range subjects {
		if err := s.expandSubject(ctx, subj, cands); err != nil {
			return nil, err
		}
	}
	return s.finalizeScored(ctx, cands, limit, nil)
}

// Search is hybrid retrieval (PLAN §13): the union of lexical FTS candidates
// (from q.Text, tokenized via textutil.Tokens and matched as prefixes) and
// structural candidates (from q.Subjects, expanded exactly as Relevant
// does), additionally filtered by q.Scope/Type/Service/Operation/Flow when
// set. Excludes inactive memories. Ordered by score descending, limited to
// q.Limit (default 50).
func (s *Store) Search(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	cands := map[string]*candidate{}
	for _, subj := range q.Subjects {
		if err := s.expandSubject(ctx, subj, cands); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(q.Text) != "" {
		if err := s.lexicalCandidates(ctx, q.Text, cands); err != nil {
			return nil, err
		}
	}

	return s.finalizeScored(ctx, cands, limit, func(m domain.Memory) bool { return matchesQueryFilters(m, q) })
}

// matchesQueryFilters applies MemoryQuery's structural filters (beyond
// Subjects/Text, which already shaped the candidate set) to one memory.
func matchesQueryFilters(m domain.Memory, q domain.MemoryQuery) bool {
	if q.Scope != "" && m.Scope != q.Scope {
		return false
	}
	if q.Type != "" && m.Type != q.Type {
		return false
	}
	if q.Service != "" && m.Subject.Service != q.Service && !strings.HasPrefix(m.Subject.Operation, q.Service+".") {
		return false
	}
	if q.Operation != "" && m.Subject.Operation != q.Operation {
		return false
	}
	if q.Flow != "" && m.Subject.Flow != q.Flow {
		return false
	}
	return true
}

// expandSubject expands one subject into every memory_subjects match it
// implies, bumping each matched memory's candidate tier/reasons. See
// PLAN.md §13's structural tier table and §14's expansion rule ("operation
// -> its service, its schemas, flows using it").
func (s *Store) expandSubject(ctx context.Context, subj domain.Subject, cands map[string]*candidate) error {
	lookup := func(kind, value string, tier float64, reason string) error {
		if value == "" {
			return nil
		}
		ids, err := s.memoriesForSubject(ctx, kind, value)
		if err != nil {
			return err
		}
		for _, id := range ids {
			getOrCreateCandidate(cands, id).bump(tier, reason)
		}
		return nil
	}

	if subj.Operation != "" {
		if subj.Field != "" {
			if err := lookup("field", subj.Operation+"#"+subj.Field, tierOperationField, "operation+field match"); err != nil {
				return err
			}
		}
		if err := lookup("operation", subj.Operation, tierOperation, "operation match"); err != nil {
			return err
		}

		if s.res != nil {
			if info, ok := s.res.Operation(ctx, subj.Operation); ok {
				if err := lookup("service", info.Service, tierService, "service "+info.Service); err != nil {
					return err
				}
				for _, sch := range info.Schemas {
					if err := lookup("schema", sch, tierSchema, "schema "+sch); err != nil {
						return err
					}
				}
				for _, flowID := range s.res.FlowsUsing(ctx, subj.Operation) {
					if err := lookup("flow", flowID, tierFlow, "flow "+flowID); err != nil {
						return err
					}
				}
				if len(info.Tags) > 0 {
					if err := s.tagOverlap(ctx, info.Tags, cands); err != nil {
						return err
					}
				}
			}
		}
	} else if subj.Field != "" && subj.Schema != "" {
		if err := lookup("field", subj.Schema+"#"+subj.Field, tierSchema, "schema field match"); err != nil {
			return err
		}
	}

	if subj.Schema != "" {
		if err := lookup("schema", subj.Schema, tierSchema, "schema match"); err != nil {
			return err
		}
	}
	if subj.Flow != "" {
		if err := lookup("flow", subj.Flow, tierFlow, "flow match"); err != nil {
			return err
		}
	}
	if subj.Service != "" {
		if err := lookup("service", subj.Service, tierService, "service match"); err != nil {
			return err
		}
	}
	if subj.Concept != "" {
		if err := lookup("concept", subj.Concept, tierConcept, "concept match"); err != nil {
			return err
		}
	}
	if subj.Environment != "" {
		if err := lookup("environment", subj.Environment, tierMisc, "environment match"); err != nil {
			return err
		}
	}
	if subj.Run != "" {
		if err := lookup("run", subj.Run, tierMisc, "run reference"); err != nil {
			return err
		}
	}
	if subj.Error != nil {
		key := fmt.Sprintf("%s#%d#%s", subj.Error.Operation, subj.Error.Status, subj.Error.Code)
		if err := lookup("error", key, tierError, "error match"); err != nil {
			return err
		}
		if subj.Error.Operation != "" {
			if err := lookup("operation", subj.Error.Operation, tierErrorFallback, "error operation fallback"); err != nil {
				return err
			}
		}
	}
	return nil
}

// memoriesForSubject returns the IDs of memories with a memory_subjects row
// (kind, value).
func (s *Store) memoriesForSubject(ctx context.Context, kind, value string) ([]string, error) {
	var ids []string
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `SELECT memory_id FROM memory_subjects WHERE kind = ? AND value = ?`, kind, value)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "look up memory_subjects %s=%s", kind, value)
	}
	return ids, nil
}

// tagOverlap bumps every memory whose tags share at least one (case-folded)
// tag with opTags: "concept/tag overlap with operation tags" (PLAN §13).
func (s *Store) tagOverlap(ctx context.Context, opTags []string, cands map[string]*candidate) error {
	if len(opTags) == 0 {
		return nil
	}
	tagSet := make(map[string]bool, len(opTags))
	for _, t := range opTags {
		tagSet[strings.ToLower(t)] = true
	}

	return s.db.Read(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `SELECT id, tags_json FROM memories WHERE tags_json IS NOT NULL AND tags_json != '' AND tags_json != '[]'`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, tagsJSON string
			if err := rows.Scan(&id, &tagsJSON); err != nil {
				return err
			}
			var tags []string
			_ = store.UnmarshalJSON(tagsJSON, &tags)
			for _, t := range tags {
				if tagSet[strings.ToLower(t)] {
					getOrCreateCandidate(cands, id).bump(tierTagOverlap, "tag "+t)
					break
				}
			}
		}
		return rows.Err()
	})
}

// ftsMatchQuery builds an FTS5 MATCH expression that hits any of tokens as a
// prefix (textutil.Tokens already yields alnum/underscore-only tokens, so no
// escaping is needed beyond the quoting below, which is defensive).
func ftsMatchQuery(tokens []string) string {
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		parts[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"*`
	}
	return strings.Join(parts, " OR ")
}

// lexicalCandidates runs an FTS5 query over memories_fts and folds the
// (sign-flipped, batch-normalized to 0..1) bm25 rank into each hit's
// candidate. bm25() is a cost (lower is "better"/more negative), so raw
// scores are negated before normalizing against the best (max) raw score in
// this result set.
func (s *Store) lexicalCandidates(ctx context.Context, text string, cands map[string]*candidate) error {
	tokens := textutil.Tokens(text)
	if len(tokens) == 0 {
		return nil
	}
	match := ftsMatchQuery(tokens)

	return s.db.Read(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx,
			`SELECT id, bm25(memories_fts) FROM memories_fts WHERE memories_fts MATCH ? ORDER BY bm25(memories_fts) LIMIT 200`, match)
		if err != nil {
			return err
		}
		defer rows.Close()

		var ids []string
		var raws []float64
		maxRaw := 0.0
		for rows.Next() {
			var id string
			var rank float64
			if err := rows.Scan(&id, &rank); err != nil {
				return err
			}
			raw := -rank
			ids = append(ids, id)
			raws = append(raws, raw)
			if raw > maxRaw {
				maxRaw = raw
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i, id := range ids {
			norm := 0.0
			if maxRaw > 0 {
				norm = raws[i] / maxRaw
				if norm < 0 {
					norm = 0
				}
			}
			c := getOrCreateCandidate(cands, id)
			if norm > c.bm25 {
				c.bm25 = norm
			}
		}
		return nil
	})
}

// provenanceWeight maps a memory's source kind to the provenance weight
// PLAN.md §13 defines: user/documentation 1.0, run 0.9, agent 0.7. Anything
// else (e.g. import) gets a conservative middle value.
func provenanceWeight(kind string) float64 {
	switch kind {
	case "user", "documentation":
		return 1.0
	case "run":
		return 0.9
	case "agent":
		return 0.7
	default:
		return 0.5
	}
}

// recencyScore is a linear decay from 1 (just updated) to 0 (>=90 days old),
// PLAN §13's "recency is a tiebreak only".
func recencyScore(updated time.Time) float64 {
	days := time.Since(updated).Hours() / 24
	if days <= 0 {
		return 1
	}
	score := 1 - days/recencyHalfLifeDays
	if score < 0 {
		return 0
	}
	return score
}

// finalizeScored resolves each candidate to its stored memory, drops inactive
// memories and anything filter rejects, scores the rest per PLAN §13, sorts
// by score descending (ties broken by more-recently-updated first), and
// truncates to limit.
func (s *Store) finalizeScored(ctx context.Context, cands map[string]*candidate, limit int, filter func(domain.Memory) bool) ([]domain.ScoredMemory, error) {
	out := make([]domain.ScoredMemory, 0, len(cands))
	for id, c := range cands {
		m, err := s.Get(ctx, id)
		if err != nil {
			if errs.Is(err, errs.MemoryNotFound) {
				continue // index/file race: candidate vanished since it was matched
			}
			return nil, err
		}
		if m.Status != domain.MemoryActive {
			continue
		}
		if filter != nil && !filter(*m) {
			continue
		}

		score := c.structural +
			c.bm25*weightLexical +
			provenanceWeight(string(m.Source.Kind))*weightProvenance +
			recencyScore(m.Updated)*weightRecency

		reasons := c.reasons
		if c.bm25 > 0 {
			reasons = append(reasons, "lexical")
		}
		out = append(out, domain.ScoredMemory{Memory: *m, Score: score, Reasons: reasons})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Memory.Updated.After(out[j].Memory.Updated)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
