// Package diagnose closes the loop between "call failed" and "here's why".
//
// A failed step usually carries an error response whose real explanation is
// off the wire: an API returns "400 Automation Rule Exclusion" and nothing
// else. Sapien already knows the service graph -- its catalog documents
// what a status code means for that operation, its docs describe error
// codes in prose, and its memories capture what an agent (or a person)
// learned about this exact failure before. diagnose.Run pulls candidate
// tokens out of the failed response, asks the catalog whether the
// operation's contract documents that status, and searches docs and
// memories for those tokens, so a run's own output can say "might explain
// it" instead of leaving the next step of the investigation to the caller.
package diagnose

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// Kind classifies where a Hint's explanation came from.
type Kind string

const (
	// KindContract is a hint drawn from the operation's own documented
	// responses -- the highest-confidence source, since it is the contract
	// the API itself publishes.
	KindContract Kind = "contract"
	// KindDoc is a hint drawn from a documentation section search hit.
	KindDoc Kind = "doc"
	// KindMemory is a hint drawn from a memory search hit.
	KindMemory Kind = "memory"
)

// Ref is how a Hint's source can be reopened by a CLI or MCP client: a doc
// section (Service, Path, and Section) or a memory (MemoryID). Exactly one
// of these groups is populated, matching Kind.
type Ref struct {
	Service  string `json:"service,omitempty"`
	Path     string `json:"path,omitempty"`
	Section  string `json:"section,omitempty"`
	MemoryID string `json:"memory_id,omitempty"`
}

// Hint is one candidate explanation for a failed or errored step's
// response.
type Hint struct {
	Kind Kind `json:"kind"`
	// StepID names the run step this hint explains, so a multi-step flow
	// run's hints can be attributed back to the step that produced them.
	StepID string `json:"step_id,omitempty"`
	// Title is the human-readable line: for a contract hint, "contract
	// says <status>: <description>"; for a doc hint,
	// "<service>/<path> # <heading>"; for a memory hint, "<id>: <first
	// line of its text, capped at 80 chars>".
	Title string `json:"title"`
	// Score ranks hints within a step: contract hints always sort first
	// (they carry the highest possible score), then doc/memory hits by
	// their search score.
	Score float64 `json:"score"`
	// MatchedOn is the token that produced this hint (empty for a contract
	// hint, which isn't token-driven).
	MatchedOn string `json:"matched_on,omitempty"`
	Ref       Ref    `json:"ref"`
}

// Tuning constants for how much work Run and RunStep are willing to do.
// searchBudget bounds the total number of Docs/Memories calls made across
// an entire run (shared by every failing step, so one run never fires more
// than ~searchBudget searches no matter how many steps failed); the rest
// bound one step's own share of that work.
const (
	maxTokensPerStep = 6
	maxHintsPerStep  = 5
	searchBudget     = 12
	docsLimit        = 3
	memoriesLimit    = 3
	contractScore    = 100 // always ranks above search-derived hints
)

// Run inspects every failed or errored step of run that carries an HTTP
// response with status >= 400, and returns candidate hints about what
// caused it, in step order. It never fails the caller: a Catalog lookup or
// Search/Memories error for one step is logged and skipped rather than
// returned, so one bad lookup never hides hints another step found. Across
// the whole call, Run makes at most about searchBudget Docs/Memories calls
// combined, regardless of how many steps failed.
func Run(ctx context.Context, eng engine.Engine, run *domain.Run) []Hint {
	if run == nil || eng == nil {
		return nil
	}
	budget := searchBudget
	var hints []Hint
	for i := range run.Steps {
		st := run.Steps[i]
		if !isFailed(st) {
			continue
		}
		stepHints, spent := stepHints(ctx, eng, st, budget)
		budget -= spent
		hints = append(hints, stepHints...)
	}
	return hints
}

// RunStep is Run's per-step variant: it returns hints for just the one step
// of run identified by stepID (or nil if run has no such step, or that step
// isn't a diagnosis candidate). Unlike Run, the full searchBudget is
// available to this single step, since nothing else is competing for it.
func RunStep(ctx context.Context, eng engine.Engine, run *domain.Run, stepID string) []Hint {
	if run == nil || eng == nil {
		return nil
	}
	for i := range run.Steps {
		if run.Steps[i].StepID != stepID {
			continue
		}
		hints, _ := stepHints(ctx, eng, run.Steps[i], searchBudget)
		return hints
	}
	return nil
}

// isFailed reports whether st is a diagnosis candidate: its status is
// failed or errored, and it carries an HTTP response with status >= 400.
// Everything else -- passed steps, skipped steps, and errored steps that
// never got a response (a pure transport error) -- has nothing for Run to
// look at.
func isFailed(st domain.StepResult) bool {
	if st.Status != domain.StepFailed && st.Status != domain.StepErrored {
		return false
	}
	return st.Response != nil && st.Response.Status >= 400
}

// stepHints builds hints for one failing step, spending at most budget
// Docs/Memories calls, and reports how much of that budget it actually
// spent (so Run can share one budget across every step it processes).
func stepHints(ctx context.Context, eng engine.Engine, st domain.StepResult, budget int) (hints []Hint, spent int) {
	if !isFailed(st) {
		return nil, 0
	}

	logger := slog.Default()

	var service string
	op, err := eng.Catalog().GetOperation(ctx, st.Operation)
	if err != nil {
		logger.Warn("diagnose: resolving operation for a failed step", "step", st.StepID, "operation", st.Operation, "err", err)
	} else {
		service = op.ServiceID
		if h := contractHint(st, *op); h != nil {
			hints = append(hints, *h)
		}
	}

	tokens := extractTokens(st.Response.Body)
	tokens = append(tokens, strconv.Itoa(st.Response.Status))
	tokens = dedupeStrings(tokens)
	if len(tokens) > maxTokensPerStep {
		tokens = tokens[:maxTokensPerStep]
	}

	var candidates []Hint
	for _, tok := range tokens {
		if spent >= budget {
			break
		}
		docs, err := eng.Search().Docs(ctx, tok, domain.SearchOptions{Service: service, Limit: docsLimit})
		spent++
		if err != nil {
			logger.Warn("diagnose: doc search failed", "step", st.StepID, "token", tok, "err", err)
		} else {
			for _, d := range docs {
				candidates = append(candidates, docHint(st.StepID, d, tok))
			}
		}

		if spent >= budget {
			break
		}
		mems, err := eng.Memories().Search(ctx, domain.MemoryQuery{Text: tok, Service: service, Limit: memoriesLimit})
		spent++
		if err != nil {
			logger.Warn("diagnose: memory search failed", "step", st.StepID, "token", tok, "err", err)
		} else {
			for _, m := range mems {
				candidates = append(candidates, memoryHint(st.StepID, m, tok))
			}
		}
	}

	candidates = dedupeHints(candidates)
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	if len(candidates) > maxHintsPerStep {
		candidates = candidates[:maxHintsPerStep]
	}
	hints = append(hints, candidates...)
	return hints, spent
}

// contractHint reports whether op documents the exact status code st's
// response returned, returning a KindContract hint built from that
// response's description when it does.
func contractHint(st domain.StepResult, op domain.Operation) *Hint {
	statusText := strconv.Itoa(st.Response.Status)
	for _, r := range op.Responses {
		if r.Status != statusText {
			continue
		}
		title := "contract says " + statusText
		if r.Description != "" {
			title += ": " + r.Description
		}
		return &Hint{Kind: KindContract, StepID: st.StepID, Title: title, Score: contractScore}
	}
	return nil
}

// docHint renders one doc-section search hit as a Hint.
func docHint(stepID string, d domain.DocSearchResult, token string) Hint {
	return Hint{
		Kind:      KindDoc,
		StepID:    stepID,
		Title:     fmt.Sprintf("%s/%s # %s", d.Service, d.Path, d.Heading),
		Score:     d.Score,
		MatchedOn: token,
		Ref:       Ref{Service: d.Service, Path: d.Path, Section: d.Heading},
	}
}

// memoryHint renders one memory search hit as a Hint: "<id>: <first line of
// its text, capped at 80 chars>".
func memoryHint(stepID string, m domain.ScoredMemory, token string) Hint {
	line := m.Memory.Text
	if idx := strings.IndexAny(line, "\r\n"); idx >= 0 {
		line = line[:idx]
	}
	if len(line) > 80 {
		line = line[:80]
	}
	return Hint{
		Kind:      KindMemory,
		StepID:    stepID,
		Title:     fmt.Sprintf("%s: %s", m.Memory.ID, line),
		Score:     m.Score,
		MatchedOn: token,
		Ref:       Ref{MemoryID: m.Memory.ID},
	}
}

// hintTarget identifies what a hint points at, for dedupeHints: a doc
// section (by service/path/section) or a memory (by id).
func hintTarget(h Hint) string {
	switch h.Kind {
	case KindDoc:
		return "doc:" + h.Ref.Service + "/" + h.Ref.Path + "#" + h.Ref.Section
	case KindMemory:
		return "memory:" + h.Ref.MemoryID
	default:
		return string(h.Kind) + ":" + h.Title
	}
}

// dedupeHints collapses hints that point at the same target (the same doc
// section or memory turned up by more than one token), keeping the
// highest-scoring occurrence and the order the first occurrence appeared in.
func dedupeHints(hints []Hint) []Hint {
	if len(hints) == 0 {
		return hints
	}
	index := make(map[string]int, len(hints))
	out := make([]Hint, 0, len(hints))
	for _, h := range hints {
		key := hintTarget(h)
		if i, ok := index[key]; ok {
			if h.Score > out[i].Score {
				out[i] = h
			}
			continue
		}
		index[key] = len(out)
		out = append(out, h)
	}
	return out
}
