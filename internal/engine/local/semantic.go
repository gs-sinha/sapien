// Semantic search wiring (PLAN.md §16, Phase 6). Off by default: enabled
// either via config.Config.Semantic (see internal/config) or by tests
// injecting Options.Embedder directly. When enabled, operations/docs/
// memories are embedded into internal/semantic's vector index in a
// background worker fed by a bounded queue, so a sync/apply or memory
// write is never blocked on an embedding call; a failed or unreachable
// embedding endpoint degrades to lexical-only search rather than surfacing
// an error (semanticAdapter.Query logs and returns no hits).
package local

import (
	"context"
	"log/slog"

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

// setupSemantic builds l.semIdx (and wires it into l.srch via
// search.Searcher.WithSemantic) and starts the background indexing worker,
// per Options.Embedder (bypasses config entirely) or cfg.Semantic.Enabled.
// It is a no-op, leaving l.semIdx nil, when semantic search is disabled.
func (l *Local) setupSemantic(cfg config.Config, opts Options) error {
	embedder := opts.Embedder
	if embedder == nil && cfg.Semantic.Enabled {
		emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
			Kind:      cfg.Semantic.Kind,
			BaseURL:   cfg.Semantic.BaseURL,
			Model:     cfg.Semantic.Model,
			APIKey:    cfg.Semantic.APIKey,
			BatchSize: cfg.Semantic.BatchSize,
		})
		if err != nil {
			return err
		}
		embedder = emb
	}
	if embedder == nil {
		return nil
	}

	l.semIdx = semantic.NewIndex(l.db, embedder)
	l.srch.WithSemantic(&semanticAdapter{idx: l.semIdx, logger: l.logger})

	ctx, cancel := context.WithCancel(context.Background())
	l.semCancel = cancel
	l.semQueue = make(chan semanticJob, semanticQueueSize)
	l.semDone = make(chan struct{})
	go l.semanticWorker(ctx)
	return nil
}

// stopSemantic stops the background indexing worker, if one was started,
// and waits for it to actually exit before returning -- so Close never
// closes l.db out from under a job the worker is still (or about to be)
// running against it. Safe to call more than once, and when semantic
// search was never enabled.
func (l *Local) stopSemantic() {
	if l.semCancel != nil {
		l.semCancel()
	}
	if l.semDone != nil {
		<-l.semDone
	}
}

// semanticWorker is the single background goroutine that drains l.semQueue,
// running until ctx is done (Close, via stopSemantic), then signals semDone.
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
	if l.semIdx == nil {
		return
	}
	select {
	case l.semQueue <- semanticJob{kind: semanticJobService, service: service}:
	default:
		l.logger.Warn("semantic index queue full; dropping job", "service", service)
	}
}

// enqueueSemanticMemoryIndex schedules a background (re)index of mems, or
// -- when mems is nil -- of every memory in the store. A no-op when
// semantic search is disabled.
func (l *Local) enqueueSemanticMemoryIndex(mems []domain.Memory) {
	if l.semIdx == nil {
		return
	}
	select {
	case l.semQueue <- semanticJob{kind: semanticJobMemories, memories: mems}:
	default:
		l.logger.Warn("semantic index queue full; dropping memory reindex job")
	}
}

func (l *Local) runSemanticJob(ctx context.Context, job semanticJob) {
	switch job.kind {
	case semanticJobService:
		l.indexServiceSemantics(ctx, job.service)
	case semanticJobMemories:
		l.indexMemoriesSemantics(ctx, job.memories)
	}
}

// indexServiceSemantics embeds service's operations (with their fields) and
// doc sections. Errors are logged at warn and otherwise swallowed: semantic
// indexing is best-effort and must never surface as a sync/apply failure.
func (l *Local) indexServiceSemantics(ctx context.Context, service string) {
	if l.semIdx == nil {
		return
	}

	ops, err := l.cat.ListOperations(ctx, service)
	if err != nil {
		l.logger.Warn("semantic: list operations failed", "service", service, "error", err)
		return
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
	if _, err := l.semIdx.IndexOperationsWithTasks(ctx, ops, fields, taskPhrases); err != nil {
		l.logger.Warn("semantic: index operations failed", "service", service, "error", err)
	}

	docs, err := l.cat.ListDocs(ctx, service)
	if err != nil {
		l.logger.Warn("semantic: list docs failed", "service", service, "error", err)
		return
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
	if _, err := l.semIdx.IndexDocs(ctx, sections); err != nil {
		l.logger.Warn("semantic: index docs failed", "service", service, "error", err)
	}
}

// indexMemoriesSemantics embeds mems, or every memory in the store when
// mems is nil.
func (l *Local) indexMemoriesSemantics(ctx context.Context, mems []domain.Memory) {
	if l.semIdx == nil {
		return
	}
	if mems == nil {
		all, err := l.memStore.List(ctx, domain.MemoryQuery{Limit: 100000})
		if err != nil {
			l.logger.Warn("semantic: list memories failed", "error", err)
			return
		}
		mems = all
	}
	if len(mems) == 0 {
		return
	}
	if _, err := l.semIdx.IndexMemories(ctx, mems); err != nil {
		l.logger.Warn("semantic: index memories failed", "error", err)
	}
}

// SemanticStats returns the semantic vector index's summary (PLAN §16): row
// counts per kind and per (model, dim). Returns the zero Stats, no error,
// when semantic search is disabled.
func (l *Local) SemanticStats(ctx context.Context) (semantic.Stats, error) {
	if l.semIdx == nil {
		return semantic.Stats{}, nil
	}
	return l.semIdx.Stats(ctx)
}

// SemanticReindex synchronously rebuilds the semantic vector index from
// scratch: every registered service's operations and doc sections, plus
// every memory. Unlike the background per-sync/per-write hook, it runs to
// completion before returning, so the CLI's debug command (or a test) can
// rely on the index being fully caught up afterward. A no-op returning nil
// when semantic search is disabled.
func (l *Local) SemanticReindex(ctx context.Context) error {
	if l.semIdx == nil {
		return nil
	}

	for _, k := range []semantic.Kind{semantic.KindOperation, semantic.KindDoc, semantic.KindMemory} {
		if err := l.semIdx.Clear(ctx, k); err != nil {
			return err
		}
	}

	svcs, err := l.cat.ListServices(ctx)
	if err != nil {
		return err
	}
	for _, s := range svcs {
		l.indexServiceSemantics(ctx, s.Name)
	}
	l.indexMemoriesSemantics(ctx, nil)
	return nil
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
}

var _ search.Semantic = (*semanticAdapter)(nil)

func (a *semanticAdapter) Query(ctx context.Context, kind string, text string, limit int) ([]search.SemanticHit, error) {
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
