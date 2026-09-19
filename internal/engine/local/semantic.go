// Semantic search wiring (PLAN.md §16, Phase 6; PLAN §34f item 5's hot-swap
// and status tracking). Off by default: enabled either via
// config.Config.Semantic (see internal/config) or by tests injecting
// Options.Embedder directly. When enabled, operations/docs/memories are
// embedded into internal/semantic's vector index in a background worker fed
// by a bounded queue, so a sync/apply or memory write is never blocked on an
// embedding call; a failed or unreachable embedding endpoint degrades to
// lexical-only search rather than surfacing an error (semanticAdapter.Query
// logs and returns no hits) -- but is still recorded in Local's own status
// (semState/semErr) so a Settings page can show it.
//
// The embedder can be replaced live (ApplySemantic, PLAN §34f item 5): every
// reader of l.semIdx/l.semQueue/l.semCancel/l.semDone goes through the
// accessors below (semanticIndex/semanticQueue), which take l.semMu's read
// lock, so a swap -- which takes the write lock, drains the outgoing
// worker, and installs the replacement -- is observed as either fully-old or
// fully-new, never half-done.
package local

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/search"
	"github.com/gs-sinha/sapien/internal/semantic"
)

// semanticQueueSize bounds the background indexing queue. A full queue
// drops the new job (logged at warn) rather than blocking whoever tried to
// enqueue it: the next sync/apply or an explicit SemanticReindex will catch
// anything missed.
const semanticQueueSize = 64

// semanticEventThrottle is how often SemanticReindex publishes a
// semantic.index progress event while it runs (PLAN §34f item 5: "~1/s").
const semanticEventThrottle = time.Second

// semanticJobKind distinguishes what a semanticJob asks the background
// worker to (re)index.
type semanticJobKind int

const (
	semanticJobService semanticJobKind = iota
	semanticJobMemories
)

// semanticJob is one unit of background indexing work.
type semanticJob struct {
	kind     semanticJobKind
	service  string          // semanticJobService: the service to (re)index
	memories []domain.Memory // semanticJobMemories: specific memories to index; nil means "reindex all"
}

// setupSemantic wires Local's semantic search up per Options.Embedder
// (bypasses config entirely) or cfg.Semantic, at Open time. It is a thin
// front for applyEmbedder/ApplySemantic -- the same machinery a later live
// settings change (PLAN §34f item 5) uses -- so Open and a hot-swap can
// never drift apart.
func (l *Local) setupSemantic(cfg config.Config, opts Options) error {
	if opts.Embedder != nil {
		applied := config.Semantic{Enabled: true, Kind: "options", Model: opts.Embedder.Model()}
		return l.applyEmbedder(applied, opts.Embedder)
	}
	return l.ApplySemantic(cfg.Semantic)
}

// ApplySemantic hot-swaps Local's embedder and semantic index to match cfg
// (PLAN §34f item 5): building a fresh semantic.Embedder from cfg (when
// cfg.Enabled) and installing it via applyEmbedder, or -- when cfg.Enabled
// is false -- turning semantic search off, leaving search purely lexical.
// It does not itself decide whether to trigger a reindex; callers (the
// settings API's PutSemantic/ReloadSemantic) compare before/after config
// for that.
func (l *Local) ApplySemantic(cfg config.Semantic) error {
	var embedder semantic.Embedder
	if cfg.Enabled {
		emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
			Kind:      cfg.Kind,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			APIKey:    cfg.APIKey,
			BatchSize: cfg.BatchSize,
		})
		if err != nil {
			return err
		}
		embedder = emb
	}
	return l.applyEmbedder(cfg, embedder)
}

// applyEmbedder is ApplySemantic's (and setupSemantic's) shared body once
// the target embedder is known (nil turns semantic search off): under
// semSwapMu (so two swaps never race each other), it drains the outgoing
// worker, then -- under l.semMu's write lock -- replaces l.semIdx/
// l.semQueue/l.semCancel/l.semDone and the search.Semantic adapter l.srch
// was handed, starts a fresh worker when embedder is non-nil, and records
// the new status.
func (l *Local) applyEmbedder(cfg config.Semantic, embedder semantic.Embedder) error {
	l.semSwapMu.Lock()
	defer l.semSwapMu.Unlock()

	l.drainSemantic()

	l.semMu.Lock()
	defer l.semMu.Unlock()

	l.semAppliedCfg = cfg

	if embedder == nil {
		l.semIdx = nil
		l.semQueue = nil
		l.semCancel = nil
		l.semDone = nil
		l.srch.WithSemantic(nil)
		l.setSemanticState(domain.SemanticOff, "")
		return nil
	}

	l.semIdx = semantic.NewIndex(l.db, embedder).WithPrefixes(effectivePrefixes(cfg))
	l.srch.WithSemantic(&semanticAdapter{idx: l.semIdx, logger: l.logger, cfg: cfg})

	ctx, cancel := context.WithCancel(context.Background())
	l.semCancel = cancel
	l.semQueue = make(chan semanticJob, semanticQueueSize)
	l.semDone = make(chan struct{})
	go l.semanticWorker(ctx)

	l.setSemanticState(domain.SemanticReady, "")
	return nil
}

// effectivePrefixes returns the task prefixes cfg embeds with: each side is
// the configured override when there is one ("" included), else what the
// model's own documentation asks for.
func effectivePrefixes(cfg config.Semantic) semantic.Prefixes {
	p := semantic.DefaultPrefixes(cfg.Model)
	if cfg.QueryPrefix != nil {
		p.Query = *cfg.QueryPrefix
	}
	if cfg.DocumentPrefix != nil {
		p.Document = *cfg.DocumentPrefix
	}
	return p
}

// drainSemantic stops the current worker (if any) and waits for it to
// exit. It reads semCancel/semDone under a brief semMu *read* lock rather
// than holding semMu for the whole wait: an in-flight job takes semMu's
// read lock too, via semanticIndex()/SemanticStatus, to publish a
// semantic.index event partway through or at the end of its own work
// (beginSemanticIndexing/recordSemanticOutcome) -- holding the write lock
// across the wait would block that read and deadlock against the very job
// this is waiting to finish. Callers must hold semSwapMu (applyEmbedder and
// stopSemantic do) so two drains can never race each other.
func (l *Local) drainSemantic() {
	l.semMu.RLock()
	cancel, done := l.semCancel, l.semDone
	l.semMu.RUnlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// stopSemantic stops the background indexing worker, if one was started,
// and waits for it to actually exit before returning -- so Close never
// closes l.db out from under a job the worker is still (or about to be)
// running against it. Safe to call more than once, and when semantic
// search was never enabled.
func (l *Local) stopSemantic() {
	l.semSwapMu.Lock()
	defer l.semSwapMu.Unlock()

	l.drainSemantic()

	l.semMu.Lock()
	l.semCancel = nil
	l.semDone = nil
	l.semMu.Unlock()
}

// semanticIndex returns the currently-installed *semantic.Index, or nil,
// under semMu's read lock -- the read side of applyEmbedder's write lock.
// Every reader of l.semIdx outside applyEmbedder/stopSemantic goes through
// this rather than the field directly.
func (l *Local) semanticIndex() *semantic.Index {
	l.semMu.RLock()
	defer l.semMu.RUnlock()
	return l.semIdx
}

// semanticQueue returns the currently-installed job queue, or nil, under
// semMu's read lock. A job sent to a queue captured just before a
// concurrent swap replaces it lands in the outgoing (abandoned) queue and
// is silently dropped -- the same "best effort, a later sync or explicit
// reindex catches it" contract enqueueSemanticIndex/
// enqueueSemanticCatchUp queues every registered service and the memories
// for indexing. upsert skips a row whose prefixed text hashes the same under
// the same model, so on an index that is already current this embeds nothing.
func (l *Local) enqueueSemanticCatchUp() {
	if l.semanticQueue() == nil {
		return
	}
	for _, ref := range l.ws.Services {
		l.enqueueSemanticIndex(ref.Name)
	}
	l.enqueueSemanticMemoryIndex(nil)
}

// enqueueSemanticMemoryIndex already documented before hot-swap existed.
func (l *Local) semanticQueue() chan semanticJob {
	l.semMu.RLock()
	defer l.semMu.RUnlock()
	return l.semQueue
}

// appliedSemanticConfig returns the config.Semantic most recently applied
// via applyEmbedder, under semMu's read lock.
func (l *Local) appliedSemanticConfig() config.Semantic {
	l.semMu.RLock()
	defer l.semMu.RUnlock()
	return l.semAppliedCfg
}

// semanticWorker is the single background goroutine that drains l.semQueue,
// running until ctx is done (a swap via applyEmbedder, or Close via
// stopSemantic), then signals semDone.
func (l *Local) semanticWorker(ctx context.Context) {
	defer close(l.semDone)
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-l.semQueue:
			l.runSemanticJob(ctx, job)
		}
	}
}

// enqueueSemanticIndex schedules a background reindex of service's
// operations and doc sections. A no-op when semantic search is disabled.
func (l *Local) enqueueSemanticIndex(service string) {
	q := l.semanticQueue()
	if q == nil {
		return
	}
	select {
	case q <- semanticJob{kind: semanticJobService, service: service}:
	default:
		l.logger.Warn("semantic index queue full; dropping job", "service", service)
	}
}

// enqueueSemanticMemoryIndex schedules a background (re)index of mems, or
// -- when mems is nil -- of every memory in the store. A no-op when
// semantic search is disabled.
func (l *Local) enqueueSemanticMemoryIndex(mems []domain.Memory) {
	q := l.semanticQueue()
	if q == nil {
		return
	}
	select {
	case q <- semanticJob{kind: semanticJobMemories, memories: mems}:
	default:
		l.logger.Warn("semantic index queue full; dropping memory reindex job")
	}
}

func (l *Local) runSemanticJob(ctx context.Context, job semanticJob) {
	l.beginSemanticIndexing(ctx)
	var err error
	switch job.kind {
	case semanticJobService:
		err = l.indexServiceSemantics(ctx, job.service)
	case semanticJobMemories:
		err = l.indexMemoriesSemantics(ctx, job.memories)
	}
	l.recordSemanticOutcome(ctx, err)
}

// indexServiceSemantics embeds service's operations (with their fields) and
// doc sections. Every failure is logged at warn and, in aggregate, returned
// (the first one encountered) so the caller can update Local's status
// (PLAN §34f item 5); indexing itself is still best-effort past that point
// -- one failed step never stops the rest from running -- and a status
// error must never surface as a sync/apply failure.
func (l *Local) indexServiceSemantics(ctx context.Context, service string) error {
	idx := l.semanticIndex()
	if idx == nil {
		return nil
	}

	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	cfg := l.appliedSemanticConfig()
	if !cfg.Embeds(config.SemanticKindOperations) {
		return l.indexServiceDocs(ctx, idx, cfg, service)
	}

	ops, err := l.cat.ListOperations(ctx, service)
	if err != nil {
		l.logger.Warn("semantic: list operations failed", "service", service, "error", err)
		return err
	}
	fields := make(map[string][]domain.Field, len(ops))
	for _, op := range ops {
		fs, ferr := l.cat.Fields(ctx, op.ID)
		if ferr != nil {
			l.logger.Warn("semantic: list fields failed", "operation", op.ID, "error", ferr)
			continue
		}
		fields[op.ID] = fs
	}
	taskPhrases := map[string][]string{}
	tasks, err := l.cat.ListTasks(ctx, service)
	if err != nil {
		l.logger.Warn("semantic: list tasks failed", "service", service, "error", err)
	} else {
		for _, task := range tasks {
			for _, target := range task.Targets {
				taskPhrases[target.Operation] = append(taskPhrases[target.Operation], task.Phrases...)
			}
		}
	}
	// A saved example's description says, in the words of whoever made it
	// work, what calling the operation is for ("rider accepts a push
	// offer"); with the examples kind on, that rides in the operation's own
	// embedded text, beside the task phrases, so an intent search that
	// matches the example ranks the operation it calls.
	if cfg.Embeds(config.SemanticKindExamples) {
		exs, eerr := l.exStore.List(ctx, domain.ExampleQuery{Service: service, Limit: 100000})
		if eerr != nil {
			l.logger.Warn("semantic: list examples failed", "service", service, "error", eerr)
		}
		for _, ex := range exs {
			if d := strings.TrimSpace(ex.Description); d != "" {
				taskPhrases[ex.Operation] = append(taskPhrases[ex.Operation], d)
			}
		}
	}
	if _, err := idx.IndexOperationsWithTasks(ctx, ops, fields, taskPhrases); err != nil {
		l.logger.Warn("semantic: index operations failed", "service", service, "error", err)
		note(err)
	}
	note(l.indexServiceDocs(ctx, idx, cfg, service))
	return firstErr
}

// indexServiceDocs embeds service's doc sections, when the docs kind is on.
func (l *Local) indexServiceDocs(ctx context.Context, idx *semantic.Index, cfg config.Semantic, service string) error {
	if !cfg.Embeds(config.SemanticKindDocs) {
		return nil
	}
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	docs, err := l.cat.ListDocs(ctx, service)
	if err != nil {
		l.logger.Warn("semantic: list docs failed", "service", service, "error", err)
		note(err)
		return firstErr
	}
	var sections []semantic.DocSectionInput
	for _, d := range docs {
		full, gerr := l.cat.GetDoc(ctx, service, d.Path)
		if gerr != nil {
			l.logger.Warn("semantic: get doc failed", "service", service, "path", d.Path, "error", gerr)
			continue
		}
		for _, sec := range full.Sections {
			sections = append(sections, semantic.DocSectionInput{
				ID:      sec.ID,
				Title:   full.Title,
				Heading: sec.Heading,
				Body:    sec.Body,
			})
		}
	}
	if _, err := idx.IndexDocs(ctx, sections); err != nil {
		l.logger.Warn("semantic: index docs failed", "service", service, "error", err)
		note(err)
	}
	return firstErr
}

// indexMemoriesSemantics embeds mems, or every memory in the store when
// mems is nil.
func (l *Local) indexMemoriesSemantics(ctx context.Context, mems []domain.Memory) error {
	idx := l.semanticIndex()
	if idx == nil || !l.appliedSemanticConfig().Embeds(config.SemanticKindMemories) {
		return nil
	}
	if mems == nil {
		all, err := l.memStore.List(ctx, domain.MemoryQuery{Limit: 100000})
		if err != nil {
			l.logger.Warn("semantic: list memories failed", "error", err)
			return err
		}
		mems = all
	}
	if len(mems) == 0 {
		return nil
	}
	if _, err := idx.IndexMemories(ctx, mems); err != nil {
		l.logger.Warn("semantic: index memories failed", "error", err)
		return err
	}
	return nil
}

// SemanticStats returns the semantic vector index's summary (PLAN §16): row
// counts per kind and per (model, dim). Returns the zero Stats, no error,
// when semantic search is disabled.
func (l *Local) SemanticStats(ctx context.Context) (semantic.Stats, error) {
	idx := l.semanticIndex()
	if idx == nil {
		return semantic.Stats{}, nil
	}
	return idx.Stats(ctx)
}

// SemanticStatus reports Local's current semantic-search status (PLAN §34f
// item 5): off when semantic search is disabled, otherwise the tracked
// state/error plus a live embedded/total count.
func (l *Local) SemanticStatus(ctx context.Context) (domain.SemanticStatus, error) {
	idx := l.semanticIndex()
	if idx == nil {
		return domain.SemanticStatus{State: domain.SemanticOff}, nil
	}

	state, errMsg := l.currentSemanticState()
	emb := idx.Embedder()
	model, dim := emb.Model(), emb.Dim()

	embedded, dim, err := semanticEmbeddedCount(ctx, idx, model, dim)
	if err != nil {
		return domain.SemanticStatus{}, err
	}
	totals, err := l.semanticTotals(ctx)
	if err != nil {
		return domain.SemanticStatus{}, err
	}
	stats, err := idx.Stats(ctx)
	if err != nil {
		return domain.SemanticStatus{}, err
	}
	stored := map[string]int{
		config.SemanticKindOperations: stats.ByKind[semantic.KindOperation],
		config.SemanticKindDocs:       stats.ByKind[semantic.KindDoc],
		config.SemanticKindMemories:   stats.ByKind[semantic.KindMemory],
	}
	total := 0
	byKind := make(map[string]domain.SemanticKindCount, len(totals))
	for kind, n := range totals {
		total += n
		byKind[kind] = domain.SemanticKindCount{Embedded: stored[kind], Total: n}
	}
	return domain.SemanticStatus{
		State: state, Error: errMsg, Model: model, Dim: dim,
		Embedded: embedded, Total: total, ByKind: byKind,
	}, nil
}

// semanticEmbeddedCount returns how many vectors-table rows are currently
// stored under (model, dim) -- across every kind, since upsert always
// stamps the writing embedder's own Model()/Dim(), so this is exactly "what
// the current embedder has written" -- along with the dim it counted under.
// An embedder learns its dim from its first response, so right after a
// daemon start dim is still 0 while the rows it wrote last time sit in the
// table: then the model's largest stored group stands in, rather than the
// status reading "0 embedded" until something happens to embed.
func semanticEmbeddedCount(ctx context.Context, idx *semantic.Index, model string, dim int) (int, int, error) {
	st, err := idx.Stats(ctx)
	if err != nil {
		return 0, dim, err
	}
	count, countDim := 0, dim
	for _, m := range st.Models {
		if m.Model != model {
			continue
		}
		if dim != 0 {
			if m.Dim == dim {
				return m.Count, dim, nil
			}
			continue
		}
		if m.Count > count {
			count, countDim = m.Count, m.Dim
		}
	}
	return count, countDim, nil
}

// semanticTotal counts operations + doc sections + memories -- exactly what
// SemanticReindex embeds -- regardless of whether semantic search is
// currently on.
func (l *Local) semanticTotal(ctx context.Context) (int, error) {
	byKind, err := l.semanticTotals(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, n := range byKind {
		total += n
	}
	return total, nil
}

// semanticTotals is semanticTotal split by kind, for the kinds the applied
// configuration embeds (config.Semantic.Kinds); "examples" has no rows of
// its own (see indexServiceSemantics) and so no entry.
func (l *Local) semanticTotals(ctx context.Context) (map[string]int, error) {
	cfg := l.appliedSemanticConfig()
	st, err := l.cat.Stats(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	if cfg.Embeds(config.SemanticKindOperations) {
		out[config.SemanticKindOperations] = st.Operations
	}
	if cfg.Embeds(config.SemanticKindDocs) {
		out[config.SemanticKindDocs] = st.Sections
	}
	if cfg.Embeds(config.SemanticKindMemories) {
		mems, err := l.memStore.List(ctx, domain.MemoryQuery{Limit: 100000})
		if err != nil {
			return nil, err
		}
		out[config.SemanticKindMemories] = len(mems)
	}
	return out, nil
}

// setSemanticState and currentSemanticState guard Local's semantic status
// snapshot under semStatusMu.
func (l *Local) setSemanticState(state domain.SemanticState, errMsg string) {
	l.semStatusMu.Lock()
	l.semState = state
	l.semErr = errMsg
	l.semStatusMu.Unlock()
}

func (l *Local) currentSemanticState() (domain.SemanticState, string) {
	l.semStatusMu.Lock()
	defer l.semStatusMu.Unlock()
	return l.semState, l.semErr
}

// beginSemanticIndexing marks the start of one round of indexing work (a
// single background job, or a full SemanticReindex): state becomes
// "indexing" and a semantic.index event is published immediately, so a
// Settings page watching the event stream sees indexing start without
// polling.
func (l *Local) beginSemanticIndexing(ctx context.Context) {
	l.setSemanticState(domain.SemanticIndexing, "")
	l.publishSemanticIndexEvent(ctx)
}

// recordSemanticOutcome updates Local's semantic status once one round of
// indexing work completes: "error" (carrying err's message) on failure,
// "ready" on success, and either way a fresh semantic.index event.
func (l *Local) recordSemanticOutcome(ctx context.Context, err error) {
	if err != nil {
		l.setSemanticState(domain.SemanticError, err.Error())
	} else {
		l.setSemanticState(domain.SemanticReady, "")
	}
	l.publishSemanticIndexEvent(ctx)
}

// publishSemanticIndexEvent computes Local's current semantic status and
// publishes it as a semantic.index event. Best effort: a stats-query
// failure here is logged and otherwise swallowed -- it must never break the
// indexing it is only reporting on.
func (l *Local) publishSemanticIndexEvent(ctx context.Context) {
	st, err := l.SemanticStatus(ctx)
	if err != nil {
		l.logger.Warn("semantic: computing status for semantic.index event failed", "error", err)
		return
	}
	l.emit(domain.EventSemanticIndex, domain.SemanticIndexEvent{
		State: st.State, Embedded: st.Embedded, Total: st.Total,
	})
}

// SemanticReindex synchronously rebuilds the semantic vector index from
// scratch: every registered service's operations and doc sections, plus
// every memory. Unlike the background per-sync/per-write hook, it runs to
// completion before returning, so the CLI's debug command (or a test) can
// rely on the index being fully caught up afterward. It publishes a
// semantic.index event at the start, about once a second while it runs
// (semanticEventThrottle), and at the end. A no-op returning nil when
// semantic search is disabled.
func (l *Local) SemanticReindex(ctx context.Context) error {
	l.semReindexMu.Lock()
	defer l.semReindexMu.Unlock()

	idx := l.semanticIndex()
	if idx == nil {
		return nil
	}
	l.beginSemanticIndexing(ctx)

	for _, k := range []semantic.Kind{semantic.KindOperation, semantic.KindDoc, semantic.KindMemory} {
		if err := idx.Clear(ctx, k); err != nil {
			l.recordSemanticOutcome(ctx, err)
			return err
		}
	}

	svcs, err := l.cat.ListServices(ctx)
	if err != nil {
		l.recordSemanticOutcome(ctx, err)
		return err
	}

	var firstErr error
	lastEmit := time.Now()
	for _, s := range svcs {
		if err := l.indexServiceSemantics(ctx, s.Name); err != nil && firstErr == nil {
			firstErr = err
		}
		if time.Since(lastEmit) >= semanticEventThrottle {
			lastEmit = time.Now()
			l.publishSemanticIndexEvent(ctx)
		}
	}
	if err := l.indexMemoriesSemantics(ctx, nil); err != nil && firstErr == nil {
		firstErr = err
	}

	l.recordSemanticOutcome(ctx, firstErr)
	return firstErr
}

// semanticAdapter adapts *semantic.Index to search.Semantic (kind string <->
// semantic.Kind, semantic.Hit -> search.SemanticHit), and -- critically --
// turns a query failure (e.g. an unreachable embedding endpoint) into a
// logged warning and no hits rather than an error: internal/search
// propagates whatever error its Semantic backend returns straight out of
// Operations()/Docs(), which would otherwise take down lexical search too
// (PLAN §16 / task spec: "search must still work lexically").
type semanticAdapter struct {
	idx    *semantic.Index
	logger *slog.Logger
	// cfg says which kinds are embedded at all: a kind that is off answers
	// no hits rather than whatever rows a previous configuration left.
	cfg config.Semantic
}

// adapterKindNames maps search's kind strings to config.SemanticKinds.
var adapterKindNames = map[string]string{
	string(semantic.KindOperation): config.SemanticKindOperations,
	string(semantic.KindDoc):       config.SemanticKindDocs,
	string(semantic.KindMemory):    config.SemanticKindMemories,
}

var _ search.Semantic = (*semanticAdapter)(nil)

func (a *semanticAdapter) Query(ctx context.Context, kind string, text string, limit int) ([]search.SemanticHit, error) {
	if name, ok := adapterKindNames[kind]; ok && !a.cfg.Embeds(name) {
		return nil, nil
	}
	hits, err := a.idx.Query(ctx, semantic.Kind(kind), text, limit)
	if err != nil {
		if a.logger != nil {
			a.logger.Warn("semantic query failed; falling back to lexical results", "kind", kind, "error", err)
		}
		return nil, nil
	}
	out := make([]search.SemanticHit, len(hits))
	for i, h := range hits {
		out[i] = search.SemanticHit{ID: h.ID, Score: h.Score}
	}
	return out, nil
}
