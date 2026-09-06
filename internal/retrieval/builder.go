package retrieval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/memory"
	"github.com/gs-sinha/sapien/internal/search"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// examplesPerOperation caps how many saved examples buildExamples fetches
// per selected operation (PLAN §14/§34b: "the examples of every selected
// operation").
const examplesPerOperation = 2

// defaultBudgetTokens is used when ContextRequest.BudgetTokens is unset.
const defaultBudgetTokens = 8000

// maxMemoryExpandedOperations caps how many operations Build's memory-aware
// expansion (see doc.go and expandFromMemories below) may add beyond the
// initial selection, so one popular concept can't flood the bundle with
// operations the caller never asked about.
const maxMemoryExpandedOperations = 3

// memoryExpansionScoreFactor and minMemoryExpansionScore compute a memory-
// driven operation's Score: 0.5x the memory's own score, floored at 0.2 so
// it never renders as a zero/negligible-relevance contract entry.
const (
	memoryExpansionScoreFactor = 0.5
	minMemoryExpansionScore    = 0.2
)

// RunSource is the run-history lookup Builder needs. *runs.Store satisfies
// it; Builder also accepts nil, meaning the runs tier is always empty.
type RunSource interface {
	List(ctx context.Context, f domain.RunFilter) ([]domain.Run, error)
}

// ExampleSource is the saved-example lookup Builder needs for the examples
// tier (PLAN §14/§34b). engine.ExampleAPI satisfies it. A Builder with no
// example source configured (the New default) always renders an empty
// examples tier.
type ExampleSource interface {
	ForOperations(ctx context.Context, operationIDs []string, limit int) ([]domain.SavedExample, error)
}

// Builder implements the agent context builder (PLAN.md §14): it composes
// the catalog, search, memory, and run stores of one workspace into a
// vendor-neutral domain.ContextBundle.
type Builder struct {
	cat    *catalog.Catalog
	srch   *search.Searcher
	mem    *memory.Store
	runsrc RunSource

	// exMu guards exsrc: SetExamples may be called concurrently with Build
	// (internal/engine/local/context.go calls it on every Context().Build,
	// since Local's constructor already fixed New's call site's arity
	// before the examples tier existed and a plain field write here would
	// race under -race otherwise).
	exMu  sync.RWMutex
	exsrc ExampleSource
}

// New returns a Builder over cat and srch (both required) plus mem and
// runsrc, either of which may be nil: a nil mem means the memories tier is
// always empty; a nil runsrc means the runs tier is always empty. The
// example source starts nil (examples tier always empty); set it with
// SetExamples.
func New(cat *catalog.Catalog, srch *search.Searcher, mem *memory.Store, runsrc RunSource) *Builder {
	return &Builder{cat: cat, srch: srch, mem: mem, runsrc: runsrc}
}

// SetExamples sets (or, with nil, clears) the example source Build's
// examples tier reads from. Safe for concurrent use, including concurrently
// with Build.
func (b *Builder) SetExamples(src ExampleSource) {
	b.exMu.Lock()
	b.exsrc = src
	b.exMu.Unlock()
}

// examples returns the current example source, or nil if none is set.
func (b *Builder) examples() ExampleSource {
	b.exMu.RLock()
	defer b.exMu.RUnlock()
	return b.exsrc
}

// selectedOp pairs a resolved operation with the search score that selected
// it (0 for an operation named explicitly in the request, or pulled in via
// req.Flow).
type selectedOp struct {
	op    domain.Operation
	score float64
}

// Build assembles a ContextBundle for req (PLAN.md §14's eight-step
// algorithm, extended by the memory-aware operation discovery described in
// doc.go): select operations, retrieve memories for them, let a retrieved
// memory's own subject operation pull in an operation search missed (intent-
// driven requests only), render every selected operation compactly, gather
// referencing docs/flows/runs for the *expanded* set, run memories once more
// against it, then enforce req.BudgetTokens in tiers, recording what was cut.
func (b *Builder) Build(ctx context.Context, req domain.ContextRequest) (*domain.ContextBundle, error) {
	budget := req.BudgetTokens
	if budget <= 0 {
		budget = defaultBudgetTokens
	}

	selected, err := b.selectOperations(ctx, req)
	if err != nil {
		return nil, err
	}
	ops := opsOf(selected)

	mems, err := b.buildMemories(ctx, ops, req.Intent)
	if err != nil {
		return nil, err
	}

	// Memory-aware operation discovery (PLAN.md §14/§25; Sapien.md §23, §31,
	// §48): only when the operation list was intent-driven (req.Operations
	// empty - an explicit list is respected as given) does a memory retrieved
	// above get to pull in the operation it's about, when search missed it.
	if len(req.Operations) == 0 {
		expansions := b.expandFromMemories(ctx, mems, selected)
		if len(expansions) > 0 {
			selected = append(selected, expansions...)
			ops = opsOf(selected)

			// One bounded extra round: memories attached only to a newly
			// expanded operation (and missed entirely by the first round,
			// which never had that operation's subjects) are picked up now.
			more, err := b.buildMemories(ctx, ops, req.Intent)
			if err != nil {
				return nil, err
			}
			mems = mergeMemories(mems, more)
		}
	}

	opCtxs := make([]domain.OperationContext, len(selected))
	for i, s := range selected {
		fields, err := b.cat.Fields(ctx, s.op.ID)
		if err != nil {
			return nil, err
		}
		oc := RenderOperation(&s.op, fields, true)
		oc.Score = s.score
		opCtxs[i] = oc
	}

	docs, err := b.buildDocs(ctx, ops, req.Intent)
	if err != nil {
		return nil, err
	}
	examples := b.buildExamples(ctx, ops)
	flows, err := b.buildFlows(ctx, ops)
	if err != nil {
		return nil, err
	}
	runResults, err := b.buildRuns(ctx, ops)
	if err != nil {
		return nil, err
	}

	bundle := &domain.ContextBundle{
		Intent:     req.Intent,
		Operations: opCtxs,
		Docs:       docs,
		Examples:   examples,
		Memories:   mems,
		Flows:      flows,
		Runs:       runResults,
	}

	enforceBudget(bundle, budget)
	bundle.EstimatedTokens = EstimateTokens(bundle)
	ensureBundleSlices(bundle)
	return bundle, nil
}

// opsOf projects selected down to its operations, in the same order.
func opsOf(selected []selectedOp) []domain.Operation {
	ops := make([]domain.Operation, len(selected))
	for i, s := range selected {
		ops[i] = s.op
	}
	return ops
}

// expandFromMemories implements the memory-aware operation discovery
// documented in doc.go: walking mems in the score order buildMemories already
// produced, it resolves the operation each memory's subject names (via
// subjectOperationID) and, when that operation isn't already in selected,
// appends it - Tier contract (via the caller's RenderOperation call), Score
// 0.5x the memory's score floored at minMemoryExpansionScore - up to
// maxMemoryExpandedOperations additions. A subject naming an operation the
// catalog no longer has is skipped, not an error (mirrors selectOperations'
// req.Flow handling of a stale reference).
func (b *Builder) expandFromMemories(ctx context.Context, mems []domain.MemoryContext, selected []selectedOp) []selectedOp {
	seen := make(map[string]bool, len(selected))
	for _, s := range selected {
		seen[s.op.ID] = true
	}

	var out []selectedOp
	for _, m := range mems {
		if len(out) >= maxMemoryExpandedOperations {
			break
		}
		opID := subjectOperationID(m.Subject)
		if opID == "" || seen[opID] {
			continue
		}
		op, err := b.cat.GetOperation(ctx, opID)
		if err != nil {
			continue
		}
		seen[opID] = true

		score := m.Score * memoryExpansionScoreFactor
		if score < minMemoryExpansionScore {
			score = minMemoryExpansionScore
		}
		out = append(out, selectedOp{op: *op, score: score})
	}
	return out
}

// subjectOperationID returns the operation id a memory subject names:
// Subject.Operation directly - which also covers a field-scoped subject,
// since domain.Subject documents Field as needing Operation or Schema, so an
// operation-scoped field IS that operation - or, absent that, an error
// subject's Subject.Error.Operation. A schema-only subject implies no single
// operation (many operations can share a schema) and yields "".
func subjectOperationID(s domain.Subject) string {
	if s.Operation != "" {
		return s.Operation
	}
	if s.Error != nil {
		return s.Error.Operation
	}
	return ""
}

// mergeMemories merges two buildMemories results, keeping the higher score
// for a duplicate id, then re-sorts and re-caps exactly as buildMemories
// itself does for one round (score descending, id ascending tiebreak, top
// 15) so a second round can never leave the bundle in a state buildMemories
// alone wouldn't have produced.
func mergeMemories(a, b []domain.MemoryContext) []domain.MemoryContext {
	byID := make(map[string]domain.MemoryContext, len(a)+len(b))
	for _, m := range a {
		byID[m.ID] = m
	}
	for _, m := range b {
		if cur, ok := byID[m.ID]; !ok || m.Score > cur.Score {
			byID[m.ID] = m
		}
	}

	out := make([]domain.MemoryContext, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > 15 {
		out = out[:15]
	}
	return out
}

// selectOperations implements PLAN.md §14 step 1's operation selection: an
// explicit req.Operations list (resolved via cat.GetOperation, erroring with
// suggestions on an unknown ID) or, when empty, the top search hits above a
// relevance floor; req.Flow's operations are appended either way.
func (b *Builder) selectOperations(ctx context.Context, req domain.ContextRequest) ([]selectedOp, error) {
	var selected []selectedOp
	seen := map[string]bool{}

	if len(req.Operations) == 0 {
		results, err := b.srch.Operations(ctx, req.Intent, domain.SearchOptions{Limit: 8})
		if err != nil {
			return nil, err
		}
		top := 0.0
		for _, r := range results {
			if r.Score > top {
				top = r.Score
			}
		}
		floor := 0.15
		relFloor := top * 0.25
		for _, r := range results {
			if r.Score < floor || r.Score < relFloor {
				continue
			}
			if seen[r.Operation.ID] {
				continue
			}
			seen[r.Operation.ID] = true
			selected = append(selected, selectedOp{op: r.Operation, score: r.Score})
		}
	} else {
		for _, id := range req.Operations {
			op, err := b.cat.GetOperation(ctx, id)
			if err != nil {
				return nil, err
			}
			if seen[op.ID] {
				continue
			}
			seen[op.ID] = true
			selected = append(selected, selectedOp{op: *op})
		}
	}

	if req.Flow != "" {
		flow, err := b.cat.GetFlowSummary(ctx, req.Flow)
		if err != nil {
			return nil, err
		}
		if flow != nil {
			for _, opID := range flow.Operations {
				if seen[opID] {
					continue
				}
				op, err := b.cat.GetOperation(ctx, opID)
				if err != nil {
					// The flow references an operation the catalog no
					// longer has (e.g. a stale flow after a contract
					// change); skip it rather than failing the whole
					// request.
					continue
				}
				seen[opID] = true
				selected = append(selected, selectedOp{op: *op})
			}
		}
	}

	return selected, nil
}

// docCandidate accumulates one doc section's structural tier (the max
// across every structural match it earned) and lexical contribution.
type docCandidate struct {
	structural float64
	lexical    float64
}

// buildDocs implements PLAN.md §14 step 2: sections referencing a selected
// operation, its schemas, or its operations' tags, plus a lexical search on
// the intent; scored structural + lexical*0.6, deduped by section id, top 6.
func (b *Builder) buildDocs(ctx context.Context, ops []domain.Operation, intent string) ([]domain.DocContext, error) {
	cands := map[string]*docCandidate{}
	bump := func(id string, tier float64) {
		c, ok := cands[id]
		if !ok {
			c = &docCandidate{}
			cands[id] = c
		}
		if tier > c.structural {
			c.structural = tier
		}
	}

	for _, op := range ops {
		refs, err := b.cat.DocsReferencing(ctx, domain.RefOperation, op.ID, 0)
		if err != nil {
			return nil, err
		}
		for _, r := range refs {
			bump(r.Section.ID, 1.0)
		}
	}

	seenSchema := map[string]bool{}
	for _, op := range ops {
		schemas, err := schemasForOperation(ctx, b.cat, op)
		if err != nil {
			return nil, err
		}
		for _, s := range schemas {
			if seenSchema[s.Name] {
				continue
			}
			seenSchema[s.Name] = true
			refs, err := b.cat.DocsReferencing(ctx, domain.RefSchema, s.Name, 0)
			if err != nil {
				return nil, err
			}
			for _, r := range refs {
				bump(r.Section.ID, 0.7)
			}
		}
	}

	seenTag := map[string]bool{}
	for _, op := range ops {
		// PLAN.md §14 step 2 matches docs on "its service's concept tags":
		// op.Concepts (service.yaml concepts, e.g. "qcom") is what docs.Parse
		// actually records as RefConcept values (PLAN §7); op.Tags (raw
		// OpenAPI tags, e.g. "allocations") is unioned in too since a doc can
		// equally reference an OpenAPI tag name verbatim.
		for _, tag := range unionStrings(op.Tags, op.Concepts) {
			if seenTag[tag] {
				continue
			}
			seenTag[tag] = true
			refs, err := b.cat.DocsReferencing(ctx, domain.RefConcept, tag, 0)
			if err != nil {
				return nil, err
			}
			for _, r := range refs {
				bump(r.Section.ID, 0.5)
			}
		}
	}

	if strings.TrimSpace(intent) != "" {
		hits, err := b.srch.Docs(ctx, intent, domain.SearchOptions{Limit: 6})
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			c, ok := cands[h.SectionID]
			if !ok {
				c = &docCandidate{}
				cands[h.SectionID] = c
			}
			if h.Score > c.lexical {
				c.lexical = h.Score
			}
		}
	}

	type scored struct {
		id    string
		score float64
	}
	list := make([]scored, 0, len(cands))
	for id, c := range cands {
		list = append(list, scored{id: id, score: c.structural + c.lexical*0.6})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].id < list[j].id
	})
	if len(list) > 6 {
		list = list[:6]
	}

	out := make([]domain.DocContext, 0, len(list))
	for _, s := range list {
		sec, doc, err := b.cat.GetDocSection(ctx, s.id)
		if err != nil {
			return nil, err
		}
		if sec == nil || doc == nil {
			continue
		}
		slug := s.id
		if i := strings.Index(s.id, "#"); i >= 0 {
			slug = s.id[i+1:]
		}
		out = append(out, domain.DocContext{
			Tier:    domain.TierDocumentation,
			Service: doc.ServiceID,
			Path:    doc.Path,
			Heading: sec.Heading,
			Body:    sec.Body,
			URI:     fmt.Sprintf("sapien://services/%s/docs/%s#%s", doc.ServiceID, doc.Path, slug),
		})
	}
	return out, nil
}

// buildMemories implements PLAN.md §14 step 3: subjects covering each
// selected operation, its service, its schemas, its tags, and the most
// salient intent tokens, merged with a lexical search on the intent (max
// score per memory id), top 15.
func (b *Builder) buildMemories(ctx context.Context, ops []domain.Operation, intent string) ([]domain.MemoryContext, error) {
	if b.mem == nil {
		return nil, nil
	}

	var subjects []domain.Subject
	seenService := map[string]bool{}
	seenSchema := map[string]bool{}
	seenTag := map[string]bool{}

	for _, op := range ops {
		subjects = append(subjects, domain.Subject{Operation: op.ID})

		if !seenService[op.ServiceID] {
			seenService[op.ServiceID] = true
			subjects = append(subjects, domain.Subject{Service: op.ServiceID})
		}

		schemas, err := schemasForOperation(ctx, b.cat, op)
		if err != nil {
			return nil, err
		}
		for _, s := range schemas {
			key := s.ServiceID + "." + s.Name
			if seenSchema[key] {
				continue
			}
			seenSchema[key] = true
			subjects = append(subjects, domain.Subject{Schema: key})
		}

		// Mirrors buildDocs: op.Concepts (service.yaml concepts) is unioned
		// with op.Tags (raw OpenAPI tags) so a direct {Concept: ...} subject
		// covers both (memory.Store's own tag-overlap expansion, driven by
		// CatalogResolver.Operation, already unions the two when res != nil;
		// this keeps the same behavior when mem is wired without one).
		for _, tag := range unionStrings(op.Tags, op.Concepts) {
			if seenTag[tag] {
				continue
			}
			seenTag[tag] = true
			subjects = append(subjects, domain.Subject{Concept: tag})
		}
	}

	for _, tok := range b.salientTokens(ctx, ops, intent, 3) {
		subjects = append(subjects, domain.Subject{Concept: tok})
	}

	relevant, err := b.mem.Relevant(ctx, subjects, 15)
	if err != nil {
		return nil, err
	}
	searched, err := b.mem.Search(ctx, domain.MemoryQuery{Text: intent, Limit: 10})
	if err != nil {
		return nil, err
	}

	merged := map[string]domain.ScoredMemory{}
	for _, m := range relevant {
		merged[m.Memory.ID] = m
	}
	for _, m := range searched {
		if cur, ok := merged[m.Memory.ID]; !ok || m.Score > cur.Score {
			merged[m.Memory.ID] = m
		}
	}

	list := make([]domain.ScoredMemory, 0, len(merged))
	for _, m := range merged {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Score != list[j].Score {
			return list[i].Score > list[j].Score
		}
		return list[i].Memory.ID < list[j].Memory.ID
	})
	if len(list) > 15 {
		list = list[:15]
	}

	out := make([]domain.MemoryContext, 0, len(list))
	for _, m := range list {
		out = append(out, domain.MemoryContext{
			Tier:    domain.TierMemory,
			ID:      m.Memory.ID,
			Type:    m.Memory.Type,
			Scope:   m.Memory.Scope,
			Source:  string(m.Memory.Source.Kind),
			Subject: m.Memory.Subject,
			Text:    m.Memory.Text,
			Score:   m.Score,
		})
	}
	return out, nil
}

// stopWords are dropped when picking salient intent tokens for concept
// subjects (mirrors internal/search's lexical stop-word list, duplicated
// here since that list is unexported and specific to FTS query building).
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "for": true, "to": true,
	"in": true, "on": true, "with": true, "and": true, "or": true, "by": true,
	"me": true, "find": true, "get": true, "list": true, "all": true, "show": true,
}

// salientTokens picks up to n tokens from intent (textutil.Tokens, minus
// stopWords), preferring tokens that name a known field of one of ops'
// services (via cat.FieldsByName), for use as {Concept: token} memory
// subjects (PLAN.md §14 step 3).
func (b *Builder) salientTokens(ctx context.Context, ops []domain.Operation, intent string, n int) []string {
	tokens := textutil.Tokens(intent)
	filtered := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !stopWords[t] {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) == 0 {
		filtered = tokens
	}
	if len(filtered) == 0 {
		return nil
	}

	services := map[string]bool{}
	for _, op := range ops {
		services[op.ServiceID] = true
	}

	type candidate struct {
		token   string
		matches bool
	}
	scored := make([]candidate, len(filtered))
	for i, t := range filtered {
		matches := false
		for svc := range services {
			fields, err := b.cat.FieldsByName(ctx, svc, t)
			if err == nil && len(fields) > 0 {
				matches = true
				break
			}
		}
		scored[i] = candidate{token: t, matches: matches}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].matches && !scored[j].matches
	})

	if n > len(scored) {
		n = len(scored)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = scored[i].token
	}
	return out
}

// buildExamples implements the examples tier (PLAN §14/§34b): for every
// selected operation, up to examplesPerOperation saved examples (verified
// first -- ExampleSource.ForOperations' own contract), in ops order. A nil
// example source (the default until SetExamples is called) yields no tier
// at all, matching buildRuns' nil-runsrc behaviour. Any error from the
// source (notably E_NOT_IMPLEMENTED before a workspace's example store is
// wired up) is likewise tolerated as "no examples" rather than failing the
// whole bundle: unlike docs/memories/flows/runs, examples are optional,
// unvalidated infrastructure this tier degrades gracefully without.
func (b *Builder) buildExamples(ctx context.Context, ops []domain.Operation) []domain.ExampleContext {
	src := b.examples()
	if src == nil {
		return nil
	}
	var out []domain.ExampleContext
	for _, op := range ops {
		exs, err := src.ForOperations(ctx, []string{op.ID}, examplesPerOperation)
		if err != nil {
			continue
		}
		for _, ex := range exs {
			out = append(out, renderExample(ex))
		}
	}
	return out
}

// buildFlows implements PLAN.md §14 step 4: every flow using at least one
// selected operation, deduped in encounter order, top 5. FlowSummary carries
// no step ids, so each step is rendered as "call: <operation id>".
func (b *Builder) buildFlows(ctx context.Context, ops []domain.Operation) ([]domain.FlowContext, error) {
	seen := map[string]bool{}
	var flows []domain.FlowSummary
	for _, op := range ops {
		fs, err := b.cat.FlowsUsingOperation(ctx, op.ID)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			if seen[f.ID] {
				continue
			}
			seen[f.ID] = true
			flows = append(flows, f)
		}
	}
	if len(flows) > 5 {
		flows = flows[:5]
	}
	return renderFlows(flows), nil
}

func renderFlows(flows []domain.FlowSummary) []domain.FlowContext {
	out := make([]domain.FlowContext, 0, len(flows))
	for _, f := range flows {
		steps := make([]string, 0, len(f.Operations))
		for _, opID := range f.Operations {
			steps = append(steps, "call: "+opID)
		}
		out = append(out, domain.FlowContext{ID: f.ID, Name: f.Name, Steps: steps})
	}
	return out
}

// buildRuns implements PLAN.md §14 step 5: the 3 most recent runs touching
// any selected operation, one-line-summarized.
func (b *Builder) buildRuns(ctx context.Context, ops []domain.Operation) ([]domain.RunContext, error) {
	if b.runsrc == nil {
		return nil, nil
	}

	seen := map[string]domain.Run{}
	for _, op := range ops {
		runList, err := b.runsrc.List(ctx, domain.RunFilter{Operation: op.ID, Limit: 3})
		if err != nil {
			return nil, err
		}
		for _, r := range runList {
			if _, ok := seen[r.ID]; !ok {
				seen[r.ID] = r
			}
		}
	}

	all := make([]domain.Run, 0, len(seen))
	for _, r := range seen {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].Started.Equal(all[j].Started) {
			return all[i].Started.After(all[j].Started)
		}
		return all[i].ID > all[j].ID
	})
	if len(all) > 3 {
		all = all[:3]
	}

	out := make([]domain.RunContext, 0, len(all))
	for _, r := range all {
		out = append(out, domain.RunContext{
			Tier:   domain.TierRun,
			ID:     r.ID,
			FlowID: r.FlowID,
			Status: string(r.Status),
			Summary: fmt.Sprintf("%s · %d/%d steps · %d assertion failures · %dms",
				r.Status, r.Summary.StepsPassed, r.Summary.StepsTotal, r.Summary.AssertionsFailed, r.DurationMs),
		})
	}
	return out, nil
}

// ensureBundleSlices replaces nil tiers with empty slices so the JSON a
// browser or MCP host receives says [] rather than null; several UI paths
// read .length on every tier.
func ensureBundleSlices(b *domain.ContextBundle) {
	if b.Operations == nil {
		b.Operations = []domain.OperationContext{}
	}
	if b.Docs == nil {
		b.Docs = []domain.DocContext{}
	}
	if b.Examples == nil {
		b.Examples = []domain.ExampleContext{}
	}
	if b.Memories == nil {
		b.Memories = []domain.MemoryContext{}
	}
	if b.Flows == nil {
		b.Flows = []domain.FlowContext{}
	}
	if b.Runs == nil {
		b.Runs = []domain.RunContext{}
	}
}
