package enginetest

import (
	"context"
	"sort"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

func newRunID() string { return "run_" + ulid.Make().String() }

// redactOpts strips the non-transportable Observer callback before an
// engine.RunOptions is recorded in f.Calls, so recorded calls never carry a
// func value.
func redactOpts(opts engine.RunOptions) engine.RunOptions {
	opts.Observer = nil
	return opts
}

// cannedRun builds a deterministic, always-passing two-phase run: a single
// step that resolved, requested, and asserted successfully.
func cannedRun(flowID string, opts engine.RunOptions) *domain.Run {
	now := time.Now().UTC()
	finished := now.Add(12 * time.Millisecond)
	steps := []domain.StepResult{
		{
			StepID:    "step1",
			Index:     0,
			Operation: "",
			Status:    domain.StepPassed,
			Attempts:  1,
			Request:   &domain.RequestRecord{Method: "GET", URL: "https://example.invalid/v1/resource"},
			Response:  &domain.ResponseRecord{Status: 200, Body: map[string]any{"ok": true}, Size: 13},
			Timings:   &domain.Timings{TotalMs: 12.5},
			Assertions: []domain.AssertionResult{
				{Expr: "status == 200", Passed: true},
			},
			Started:  now,
			Finished: finished,
		},
	}
	return &domain.Run{
		ID:          newRunID(),
		FlowID:      flowID,
		Environment: opts.Environment,
		Inputs:      opts.Inputs,
		Status:      domain.RunPassed,
		Started:     now,
		Finished:    finished,
		DurationMs:  finished.Sub(now).Milliseconds(),
		Steps:       steps,
		Summary:     domain.RunSummary{StepsTotal: 1, StepsPassed: 1, Assertions: 1},
		Trigger:     opts.Trigger,
	}
}

// storeRun installs run and its ordering under f.mu. The caller must already
// hold the lock.
func (f *Fake) storeRunLocked(run *domain.Run) {
	f.runs[run.ID] = *run
	f.runSeq[run.ID] = f.nextSeqLocked()
}

func (r *runnerAPI) RunFlow(ctx context.Context, flowID string, opts engine.RunOptions) (*domain.Run, error) {
	f := r.f()
	f.mu.Lock()
	if _, ok := f.flows[flowID]; !ok {
		f.recordLocked("Runner.RunFlow", map[string]any{"flow_id": flowID, "opts": redactOpts(opts)})
		f.mu.Unlock()
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", flowID).WithDetail("id", flowID)
	}
	f.recordLocked("Runner.RunFlow", map[string]any{"flow_id": flowID, "opts": redactOpts(opts)})

	run := f.RunResult
	if run == nil {
		run = cannedRun(flowID, opts)
	} else {
		cp := *run
		if cp.ID == "" {
			cp.ID = newRunID()
		}
		run = &cp
	}
	f.storeRunLocked(run)
	f.mu.Unlock()

	notifyObserver(opts, run)
	f.Publish(domain.Event{Type: domain.EventRunFinished, Time: run.Finished, Payload: run.ID})

	result := *run
	return &result, nil
}

func (r *runnerAPI) RunFlowSource(ctx context.Context, yamlSrc string, opts engine.RunOptions) (*domain.Run, error) {
	f := r.f()
	flow, err := parseFlow(yamlSrc)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.recordLocked("Runner.RunFlowSource", map[string]any{"yaml": yamlSrc, "opts": redactOpts(opts)})

	run := f.RunResult
	if run == nil {
		run = cannedRun(flow.ID, opts)
	} else {
		cp := *run
		if cp.ID == "" {
			cp.ID = newRunID()
		}
		run = &cp
	}
	f.storeRunLocked(run)
	f.mu.Unlock()

	notifyObserver(opts, run)
	f.Publish(domain.Event{Type: domain.EventRunFinished, Time: run.Finished, Payload: run.ID})

	result := *run
	return &result, nil
}

func notifyObserver(opts engine.RunOptions, run *domain.Run) {
	if opts.Observer == nil {
		return
	}
	opts.Observer(domain.Event{Type: domain.EventRunStarted, Time: run.Started, Payload: run.ID})
	for _, st := range run.Steps {
		opts.Observer(domain.Event{Type: domain.EventRunStep, Time: st.Finished, Payload: st})
	}
	opts.Observer(domain.Event{Type: domain.EventRunFinished, Time: run.Finished, Payload: run.ID})
}

func (r *runnerAPI) Call(ctx context.Context, req engine.CallRequest) (*domain.Run, error) {
	f := r.f()
	f.mu.Lock()

	op, err := f.resolveOperationLocked(req.Operation)
	if err != nil {
		f.recordLocked("Runner.Call", req)
		f.mu.Unlock()
		return nil, err
	}
	f.recordLocked("Runner.Call", req)

	run := f.RunResult
	if run == nil {
		now := time.Now().UTC()
		finished := now.Add(5 * time.Millisecond)
		method, path := "GET", "/"+req.Operation
		if op.HTTP != nil {
			method, path = op.HTTP.Method, op.HTTP.Path
		}
		run = &domain.Run{
			ID:          newRunID(),
			Environment: req.Env,
			Status:      domain.RunPassed,
			Started:     now,
			Finished:    finished,
			DurationMs:  finished.Sub(now).Milliseconds(),
			Trigger:     req.Trigger,
			Steps: []domain.StepResult{{
				StepID:    "call",
				Index:     0,
				Operation: op.ID,
				Status:    domain.StepPassed,
				Attempts:  1,
				Request:   &domain.RequestRecord{Method: method, URL: "https://example.invalid" + path, Headers: req.Headers, Body: req.Body},
				Response:  &domain.ResponseRecord{Status: 200, Body: map[string]any{"ok": true}, Size: 13},
				Timings:   &domain.Timings{TotalMs: 5},
				Started:   now,
				Finished:  finished,
			}},
			Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1},
		}
	} else {
		cp := *run
		if cp.ID == "" {
			cp.ID = newRunID()
		}
		run = &cp
	}
	f.storeRunLocked(run)
	f.mu.Unlock()

	f.Publish(domain.Event{Type: domain.EventRunFinished, Time: run.Finished, Payload: run.ID})

	result := *run
	return &result, nil
}

func (r *runnerAPI) Cancel(ctx context.Context, runID string) error {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Runner.Cancel", runID)

	run, ok := f.runs[runID]
	if !ok {
		return errs.New(errs.RunNotFound, "run %q not found", runID).WithDetail("id", runID)
	}
	run.Status = domain.RunCancelled
	f.runs[runID] = run
	return nil
}

func (r *runAPI) List(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Runs.List", filter)

	var out []domain.Run
	for _, run := range f.runs {
		if filter.FlowID != "" && run.FlowID != filter.FlowID {
			continue
		}
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		if filter.Operation != "" {
			found := false
			for _, st := range run.Steps {
				if st.Operation == filter.Operation {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		cp := run
		cp.Steps = nil // RunAPI.List: "Steps omitted"
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return f.runSeq[out[i].ID] > f.runSeq[out[j].ID] })

	if filter.Offset > 0 {
		if filter.Offset >= len(out) {
			out = nil
		} else {
			out = out[filter.Offset:]
		}
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (r *runAPI) Get(ctx context.Context, id string) (*domain.Run, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Runs.Get", id)

	run, ok := f.runs[id]
	if !ok {
		return nil, errs.New(errs.RunNotFound, "run %q not found", id).WithDetail("id", id)
	}
	cp := run
	return &cp, nil
}

func (r *runAPI) Pin(ctx context.Context, id string, pinned bool) error {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Runs.Pin", map[string]any{"id": id, "pinned": pinned})

	run, ok := f.runs[id]
	if !ok {
		return errs.New(errs.RunNotFound, "run %q not found", id).WithDetail("id", id)
	}
	run.Pinned = pinned
	f.runs[id] = run
	return nil
}

func (r *runAPI) Purge(ctx context.Context, keep int) (int, error) {
	f := r.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Runs.Purge", keep)

	type item struct {
		id     string
		seq    int
		pinned bool
	}
	items := make([]item, 0, len(f.runs))
	for id, run := range f.runs {
		items = append(items, item{id: id, seq: f.runSeq[id], pinned: run.Pinned})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].seq > items[j].seq })

	removed, kept := 0, 0
	for _, it := range items {
		if it.pinned {
			continue // pinned runs are never purged
		}
		kept++
		if kept > keep {
			delete(f.runs, it.id)
			delete(f.runSeq, it.id)
			removed++
		}
	}
	return removed, nil
}

var (
	_ engine.RunnerAPI = (*runnerAPI)(nil)
	_ engine.RunAPI    = (*runAPI)(nil)
)
