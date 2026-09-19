package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/events"
	"github.com/gs-sinha/sapien/internal/expr"
	"github.com/gs-sinha/sapien/internal/runs"
	"github.com/gs-sinha/sapien/internal/runtime"
	"github.com/gs-sinha/sapien/internal/store"
)

// Operations resolves a flow step's `call` reference to its normalized
// operation. Implementations should return errs.OperationNotFound when id is
// unknown.
type Operations interface {
	Operation(ctx context.Context, id string) (*domain.Operation, error)
}

// Options configures one Run/Call.
type Options struct {
	Env     *env.Resolved   // required
	Secrets env.SecretStore // may be nil -> no secrets resolvable
	Runs    *runs.Store     // may be nil -> not persisted
	Bus     *events.Bus     // may be nil

	Observer func(domain.Event) // may be nil

	ContinueOnFailure bool
	AllowProduction   bool
	Trigger           string

	// Sleep is used for `poll` waits between attempts. nil means a real
	// sleep honoring ctx cancellation. Tests inject an instant no-op.
	Sleep func(ctx context.Context, d time.Duration) error

	// Now is used to measure poll deadlines. nil means time.Now.
	Now func() time.Time

	// Client executes HTTP requests. nil means a runtime.Client built from
	// Env.Transport()/Env.Env.Production.
	Client *runtime.Client

	// Resume configures a partial/resumed execution (engine.RunOptions'
	// ResumeFrom/FromStep/UntilStep; PLAN §9). nil means a normal, full run.
	Resume *Resume
}

// Resume configures Run to reuse an earlier run's step results and/or
// execute only part of the flow's main steps. See Run's doc for the exact
// semantics.
type Resume struct {
	// From is the earlier run whose step results are reused: every setup
	// step and every main step before the resume point is copied instead
	// of executed. nil means no reuse -- FromStep/UntilStep alone still
	// make this a partial run, just with no earlier data to seed from.
	From *domain.Run
	// FromStep is the first main step id to execute (inclusive). Empty
	// means: with From set, the default resume point (the first main step
	// that did not pass in From, or the step after the last one From
	// recorded if every recorded step passed); with From nil, execute
	// every main step from the first.
	FromStep string
	// UntilStep is the last main step id to execute (inclusive). Empty
	// means execute through the flow's last main step. Teardown always
	// runs regardless.
	UntilStep string
}

// teardownTimeout bounds the whole teardown phase once the run itself is
// cancelled or its caller's context is gone: long enough to release
// resources, short enough that an abandoned run cannot linger.
const teardownTimeout = 2 * time.Minute

// Runner executes flows sequentially, one step at a time.
type Runner struct {
	ops Operations

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

// New returns a Runner that resolves `call` references through ops.
func New(ops Operations) *Runner {
	return &Runner{ops: ops, active: map[string]context.CancelFunc{}}
}

// Cancel cancels the in-flight run identified by runID, if any is currently
// active on this Runner. It returns false if runID is unknown (never
// started, or already finished).
func (r *Runner) Cancel(runID string) bool {
	r.mu.Lock()
	cancel, ok := r.active[runID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

func (r *Runner) register(id string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[id] = cancel
}

func (r *Runner) unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, id)
}

// execCtx bundles the state shared by every step of one Run/Call, threaded
// through executeStep without a long parameter list.
type execCtx struct {
	opts    Options
	run     *domain.Run
	client  *runtime.Client
	secrets env.SecretStore
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	eval    *expr.Evaluator
	envVars map[string]string

	// phase is the StepResult.Phase value for whatever step is currently
	// executing: "" for a main step (domain's "empty means steps"),
	// "setup", or "teardown". finishStep and emitStep both read it, so
	// Run only has to set it once per phase loop rather than thread it
	// through every call.
	phase string

	// iter and parent are set for the duration of one loop block iteration
	// (PLAN §34f.8), by runBlockIteration, and restored (nil/"") afterward:
	// executeStep reads iter to populate the `iter` root in every scope it
	// builds, and finishStep reads both to stamp StepResult.Iteration/
	// Parent and emitStep's event payload, so nested execution needs no
	// extra parameters threaded through the whole call chain.
	iter   *expr.IterValue
	parent string

	// idxCounter assigns each StepResult a globally monotonic Index across
	// every phase and every loop iteration (PLAN §34f.8: "idx for nested
	// results: keep a monotonically increasing execution index so ordering
	// is stable"), via nextIndex(). Before loop blocks, Index was simply a
	// step's position within its own list; that position is no longer
	// well-defined for a nested execution, so every StepResult -- nested or
	// not -- now gets its Index from here instead.
	idxCounter int
}

// nextIndex returns the next value in ec's monotonic Index sequence,
// starting at 0.
func (ec *execCtx) nextIndex() int {
	n := ec.idxCounter
	ec.idxCounter++
	return n
}

// Run executes f against opts, producing a domain.Run. Pre-flight problems
// (a nil Env, a blocked production environment, an unresolvable operation, or
// a missing required input) are returned as an error with no run created;
// every other outcome (including a failed or errored run) is returned as
// (run, nil) — use ErrorFor to turn a finished run into an error for CLI
// exit codes.
func (r *Runner) Run(ctx context.Context, f *domain.Flow, inputs map[string]any, opts Options) (*domain.Run, error) {
	if opts.Env == nil {
		return nil, errs.New(errs.Invalid, "runner: Options.Env is required")
	}
	if err := env.CheckProduction(opts.Env.Env, opts.AllowProduction); err != nil {
		return nil, err
	}

	plan, err := buildResumePlan(f, opts.Resume)
	if err != nil {
		return nil, err
	}

	setupOps, err := r.resolveOps(ctx, f.Setup)
	if err != nil {
		return nil, err
	}
	stepOps, err := r.resolveOps(ctx, f.Steps)
	if err != nil {
		return nil, err
	}
	teardownOps, err := r.resolveOps(ctx, f.Teardown)
	if err != nil {
		return nil, err
	}
	opHashes := map[string]string{}
	addOpHashes(opHashes, setupOps)
	addOpHashes(opHashes, stepOps)
	addOpHashes(opHashes, teardownOps)

	var resumeRunID, resumeSnapshot string
	if plan.reuse {
		resumeRunID = opts.Resume.From.ID
		resumeSnapshot = opts.Resume.From.FlowSnapshot
	}

	eval := expr.New()
	effectiveProvided := inputs
	if plan.reuse {
		effectiveProvided = mergeResumeInputs(opts.Resume.From.Inputs, inputs)
	}
	mergedInputs, err := resolveInputs(f, effectiveProvided, opts.Env.Env.Vars, eval)
	if err != nil {
		return nil, err
	}

	run := &domain.Run{
		FlowID:          f.ID,
		FlowSnapshot:    f.Source,
		Environment:     opts.Env.Env.Name,
		Inputs:          mergedInputs,
		Status:          domain.RunRunning,
		Trigger:         opts.Trigger,
		OperationHashes: opHashes,
		ResumedFrom:     resumeRunID,
	}

	if opts.Runs != nil {
		if err := opts.Runs.Create(ctx, run); err != nil {
			return nil, err
		}
	} else {
		run.ID = store.NewID("run")
		run.Started = time.Now().UTC()
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.register(run.ID, cancel)
	defer r.unregister(run.ID)
	defer cancel()

	secrets := opts.Secrets
	if secrets == nil {
		secrets = noSecretStore{}
	}
	client := opts.Client
	if client == nil {
		client = runtime.New(runtime.Options{Transport: opts.Env.Transport(), Production: opts.Env.Env.Production})
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	sleepFn := opts.Sleep
	if sleepFn == nil {
		sleepFn = defaultSleep
	}

	ec := &execCtx{
		opts:    opts,
		run:     run,
		client:  client,
		secrets: secrets,
		now:     nowFn,
		sleep:   sleepFn,
		eval:    eval,
		envVars: opts.Env.Env.Vars,
	}

	r.emit(opts, domain.EventRunStarted, run)

	stepsSoFar := map[string]expr.StepValue{}
	var stopRemaining, runCancelled bool
	var engineErr error

	// ---- setup: every step is reused when resuming, else it runs (and
	// participates in the same stop-on-failure/cancellation policy as the
	// main steps that follow it). Loop blocks are not allowed in setup
	// (LOOP_IN_PHASE), so every step here is a plain call step.
	ec.phase = "setup"
	for _, step := range f.Setup {
		op := setupOps[step.ID]

		if sr, ok := reuseIfEligible(plan, true, step); ok {
			warning := definitionChangedWarning(f.Source, resumeSnapshot, step.ID, resumeRunID)
			result := reuseStepResult(sr, resumeRunID, ec.nextIndex(), warning)
			stepsSoFar[step.ID] = stepValueFromResult(result)
			if ferr := r.finishStep(runCtx, ec, result); ferr != nil && engineErr == nil {
				engineErr = ferr
			}
			continue
		}

		if stopRemaining || runCtx.Err() != nil {
			if runCtx.Err() != nil {
				runCancelled = true
			}
			skip := skippedStepResult(step, ec.nextIndex(), op, nowFn())
			if ferr := r.finishStep(runCtx, ec, skip); ferr != nil && engineErr == nil {
				engineErr = ferr
			}
			stepsSoFar[step.ID] = expr.StepValue{}
			stopRemaining = true
			continue
		}

		result, raw := r.executeStep(runCtx, ec, step, ec.nextIndex(), op, stepsSoFar)
		if ferr := r.finishStep(runCtx, ec, result); ferr != nil && engineErr == nil {
			engineErr = ferr
		}
		rememberStep(stepsSoFar, result, raw)

		switch result.Status {
		case domain.StepFailed, domain.StepErrored:
			stopRemaining = !opts.ContinueOnFailure
		case domain.StepCancelled:
			stopRemaining = true
			runCancelled = true
		}
	}

	// ---- main steps: reused before the resume point, skipped with no
	// data before it when there's nothing to reuse from, skipped after
	// UntilStep, then the usual stop-on-failure/cancellation policy. A loop
	// block (PLAN §34f.8) is reused/executed as a unit -- see reuseBlock and
	// executeBlock -- but otherwise participates in this same window and
	// stop-on-failure/cancellation policy exactly like a call step. ----
	ec.phase = ""
	for i, step := range f.Steps {
		op := stepOps[step.ID]

		if sr, ok := reuseIfEligible(plan, i < plan.fromIdx, step); ok {
			if step.IsBlock() {
				if ferr := r.reuseBlock(runCtx, ec, plan, step, sr, resumeRunID, stepsSoFar); ferr != nil && engineErr == nil {
					engineErr = ferr
				}
				continue
			}
			warning := definitionChangedWarning(f.Source, resumeSnapshot, step.ID, resumeRunID)
			result := reuseStepResult(sr, resumeRunID, ec.nextIndex(), warning)
			stepsSoFar[step.ID] = stepValueFromResult(result)
			if ferr := r.finishStep(runCtx, ec, result); ferr != nil && engineErr == nil {
				engineErr = ferr
			}
			continue
		}

		if i < plan.fromIdx {
			// FromStep given without From (or From reused nothing for
			// this id): no earlier data exists, so this step is skipped
			// entirely and deliberately left out of stepsSoFar -- a later
			// reference to it is a hard error (see below), not a silent
			// null (PLAN §9).
			skip := skippedStepResult(step, ec.nextIndex(), op, nowFn())
			if ferr := r.finishStep(runCtx, ec, skip); ferr != nil && engineErr == nil {
				engineErr = ferr
			}
			continue
		}

		if plan.untilIdx >= 0 && i > plan.untilIdx {
			skip := skippedStepResult(step, ec.nextIndex(), op, nowFn())
			if ferr := r.finishStep(runCtx, ec, skip); ferr != nil && engineErr == nil {
				engineErr = ferr
			}
			stepsSoFar[step.ID] = expr.StepValue{}
			continue
		}

		if stopRemaining || runCtx.Err() != nil {
			if runCtx.Err() != nil {
				runCancelled = true
			}
			skip := skippedStepResult(step, ec.nextIndex(), op, nowFn())
			if ferr := r.finishStep(runCtx, ec, skip); ferr != nil && engineErr == nil {
				engineErr = ferr
			}
			stepsSoFar[step.ID] = expr.StepValue{}
			stopRemaining = true
			continue
		}

		if refs := referencedSkippedSteps(step, plan.skippedNoData); len(refs) > 0 {
			result := forwardRefError(step, ec.nextIndex(), op, refs, nowFn())
			if ferr := r.finishStep(runCtx, ec, result); ferr != nil && engineErr == nil {
				engineErr = ferr
			}
			stepsSoFar[step.ID] = expr.StepValue{}
			stopRemaining = true
			continue
		}

		var result domain.StepResult
		var raw expr.StepValue
		if step.IsBlock() {
			result, raw = r.executeBlock(runCtx, ec, step, ec.nextIndex(), stepOps, stepsSoFar, &engineErr)
		} else {
			result, raw = r.executeStep(runCtx, ec, step, ec.nextIndex(), op, stepsSoFar)
		}
		if ferr := r.finishStep(runCtx, ec, result); ferr != nil && engineErr == nil {
			engineErr = ferr
		}
		rememberStep(stepsSoFar, result, raw)

		switch result.Status {
		case domain.StepFailed, domain.StepErrored:
			stopRemaining = !opts.ContinueOnFailure
		case domain.StepCancelled:
			stopRemaining = true
			runCancelled = true
		}
	}

	// ---- teardown: always runs, in order, continuing past its own
	// failures, on a context detached from every cancellation (the run's
	// own Cancel() and the caller's ctx alike) but bounded by
	// teardownTimeout, so a cancelled or abandoned run still releases what
	// it created without being able to hang forever ----
	ec.phase = "teardown"
	tdCtx, tdCancel := context.WithTimeout(context.WithoutCancel(ctx), teardownTimeout)
	defer tdCancel()
	for _, step := range f.Teardown {
		op := teardownOps[step.ID]

		if refs := referencedSkippedSteps(step, plan.skippedNoData); len(refs) > 0 {
			result := forwardRefError(step, ec.nextIndex(), op, refs, nowFn())
			if ferr := r.finishStep(tdCtx, ec, result); ferr != nil && engineErr == nil {
				engineErr = ferr
			}
			stepsSoFar[step.ID] = expr.StepValue{}
			continue
		}

		result, raw := r.executeStep(tdCtx, ec, step, ec.nextIndex(), op, stepsSoFar)
		if ferr := r.finishStep(tdCtx, ec, result); ferr != nil && engineErr == nil {
			engineErr = ferr
		}
		rememberStep(stepsSoFar, result, raw)
	}

	run.Summary = runs.Summarize(run.Steps)
	run.Status = runs.DeriveStatus(run.Steps, runCancelled, engineErr)
	run.Finished = time.Now().UTC()
	run.DurationMs = run.Finished.Sub(run.Started).Milliseconds()
	if run.Status == domain.RunErrored {
		run.Error = deriveRunError(run.Steps, engineErr)
	}

	if opts.Runs != nil {
		if err := opts.Runs.Update(ctx, run); err != nil && engineErr == nil {
			engineErr = err
		}
	}

	r.emit(opts, domain.EventRunFinished, run)

	return run, nil
}

// resolveOps resolves each of steps' `call` reference to its normalized
// operation, keyed by step id, recursing one level into each loop block's
// own nested steps (PLAN §34f.8); a pre-flight error (like the original
// single-list version) if any is unresolvable. A block has no operation of
// its own, so it gets no entry in the result -- callers look it up by id
// and treat a miss as "this is a block" (or a step resolveOps was never
// asked about).
func (r *Runner) resolveOps(ctx context.Context, steps []domain.Step) (map[string]*domain.Operation, error) {
	ops := map[string]*domain.Operation{}
	var walk func([]domain.Step) error
	walk = func(list []domain.Step) error {
		for _, step := range list {
			if step.IsBlock() {
				if err := walk(step.Steps); err != nil {
					return err
				}
				continue
			}
			op, err := r.ops.Operation(ctx, step.Call)
			if err != nil {
				return err
			}
			ops[step.ID] = op
		}
		return nil
	}
	if err := walk(steps); err != nil {
		return nil, err
	}
	return ops, nil
}

// addOpHashes merges ops' operation hashes into dst.
func addOpHashes(dst map[string]string, ops map[string]*domain.Operation) {
	for _, op := range ops {
		if op != nil {
			dst[op.ID] = op.Hash
		}
	}
}

// reuseIfEligible returns the earlier StepResult for step from plan, if
// resuming is active, this step is within the reused window (eligible),
// and the earlier run actually recorded a step with this id.
func reuseIfEligible(plan *resumePlan, eligible bool, step domain.Step) (domain.StepResult, bool) {
	if !plan.reuse || !eligible {
		return domain.StepResult{}, false
	}
	sr, ok := plan.earlierByID[step.ID]
	return sr, ok
}

// skippedStepResult builds a StepResult for a step that is not executed at
// all this run (an earlier failure/cancellation, or past UntilStep).
func skippedStepResult(step domain.Step, idx int, op *domain.Operation, now time.Time) domain.StepResult {
	return domain.StepResult{
		StepID:    step.ID,
		Index:     idx,
		Operation: opID(op),
		Status:    domain.StepSkipped,
		Started:   now,
		Finished:  now,
	}
}

// opID returns op.ID, or "" if op is nil -- a loop block has no operation
// of its own (PLAN §34f.8), so resolveOps gives it no entry.
func opID(op *domain.Operation) string {
	if op == nil {
		return ""
	}
	return op.ID
}

// rememberStep records a just-executed step's raw value for later steps'
// steps.<id> references, unless it was skipped by its own `when` evaluating
// false (PLAN §34f.7): that step is deliberately left out of stepsSoFar
// entirely (never assigned even an empty placeholder), so an unguarded
// steps.<id> reference to it fails clearly at evaluation time instead of
// silently seeing zero values (see expr.friendlyEvalErr). This is the only
// way executeStep itself can return StepSkipped -- the other skip path
// (skippedStepResult, for a step never reached at all) is a separate branch
// in Run's phase loops that assigns stepsSoFar directly.
func rememberStep(stepsSoFar map[string]expr.StepValue, result domain.StepResult, raw expr.StepValue) {
	if result.Status == domain.StepSkipped && result.SkipReason == "when" {
		return
	}
	stepsSoFar[result.StepID] = raw
}

// Call executes a single operation as an ad hoc one-step flow.
func (r *Runner) Call(ctx context.Context, operationID string, params map[string]any, body any, headers map[string]string, opts Options) (*domain.Run, error) {
	step := domain.Step{ID: "call", Call: operationID, Input: params, Body: body, Headers: headers}
	f := &domain.Flow{Version: 1, ID: "call", Steps: []domain.Step{step}}
	f.Source = renderCallFlowYAML(operationID, params, body, headers)
	return r.Run(ctx, f, nil, opts)
}

// finishStep stamps result.Phase from ec.phase (and, inside a loop block's
// iteration, Iteration/Parent from ec.iter/ec.parent -- PLAN §34f.8),
// records it onto ec.run, persists it (if a store is configured), and emits
// its terminal run.step event -- for every phase and every nested execution
// alike.
func (r *Runner) finishStep(ctx context.Context, ec *execCtx, result domain.StepResult) error {
	result.Phase = ec.phase
	if ec.iter != nil {
		idx := ec.iter.Index
		result.Iteration = &idx
		result.Parent = ec.parent
	}
	ec.run.Steps = append(ec.run.Steps, result)
	r.emitStep(ec, result.StepID, result.Status, result.Attempts)
	if ec.opts.Runs != nil {
		return ec.opts.Runs.AppendStep(ctx, ec.run.ID, result)
	}
	return nil
}

func (r *Runner) emit(opts Options, typ domain.EventType, payload any) {
	if opts.Bus != nil {
		events.Emit(opts.Bus, typ, payload)
	}
	if opts.Observer != nil {
		opts.Observer(domain.Event{Type: typ, Time: time.Now(), Payload: payload})
	}
}

func (r *Runner) emitStep(ec *execCtx, stepID string, status domain.StepStatus, attempt int) {
	payload := map[string]any{
		"run_id":  ec.run.ID,
		"step_id": stepID,
		"status":  string(status),
		"attempt": attempt,
		"phase":   ec.phase,
	}
	if ec.iter != nil {
		payload["iteration"] = ec.iter.Index
		payload["parent"] = ec.parent
	}
	r.emit(ec.opts, domain.EventRunStep, payload)
}

// ErrorFor turns a finished run's terminal status into an error for CLI exit
// codes (errs.ExitCode): nil for a passed run, errs.AssertionFailed for a
// failed run (Details{"run_id","failed_steps"}), errs.Cancelled for a
// cancelled run, and an errs.Internal error wrapping run.Error for an
// errored run.
func ErrorFor(run *domain.Run) error {
	if run == nil {
		return nil
	}
	switch run.Status {
	case domain.RunPassed:
		return nil
	case domain.RunFailed:
		var failedSteps []string
		for _, st := range run.Steps {
			if st.Status == domain.StepFailed {
				failedSteps = append(failedSteps, st.StepID)
			}
		}
		return errs.New(errs.AssertionFailed, "run %s failed", run.ID).
			WithDetail("run_id", run.ID).
			WithDetail("failed_steps", failedSteps)
	case domain.RunCancelled:
		return errs.New(errs.Cancelled, "run %s cancelled", run.ID).WithDetail("run_id", run.ID)
	case domain.RunErrored:
		var cause error = errors.New("run errored")
		if run.Error != nil {
			cause = fmt.Errorf("%s: %s", run.Error.Code, run.Error.Message)
		}
		return errs.Wrap(errs.Internal, cause, "run %s errored", run.ID).WithDetail("run_id", run.ID)
	default:
		return nil
	}
}

func deriveRunError(steps []domain.StepResult, engineErr error) *domain.ErrorInfo {
	if engineErr != nil {
		return errs.ToInfo(engineErr)
	}
	for _, s := range steps {
		if s.Status == domain.StepErrored && s.Error != nil {
			return s.Error
		}
	}
	return &domain.ErrorInfo{Code: string(errs.Internal), Message: "run errored"}
}

// resolveInputs merges provided over each flow input's interpolated default,
// erroring on a missing required input.
func resolveInputs(f *domain.Flow, provided map[string]any, envVars map[string]string, eval *expr.Evaluator) (map[string]any, error) {
	merged := map[string]any{}
	for name, spec := range f.Inputs {
		if v, ok := provided[name]; ok {
			merged[name] = v
			continue
		}
		if spec.Default != nil {
			val, err := resolveDefault(spec.Default, envVars, eval)
			if err != nil {
				return nil, err
			}
			merged[name] = val
			continue
		}
		if spec.Required {
			return nil, errs.New(errs.InputMissing, "missing required input %q", name).WithDetail("input", name)
		}
	}
	for k, v := range provided {
		if _, ok := f.Inputs[k]; !ok {
			merged[k] = v
		}
	}
	return merged, nil
}

func resolveDefault(def any, envVars map[string]string, eval *expr.Evaluator) (any, error) {
	s, ok := def.(string)
	if !ok {
		return def, nil
	}
	val, _, err := eval.Interpolate(s, expr.Scope{Env: envVars})
	if err != nil {
		return nil, err
	}
	return val, nil
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// noSecretStore is used when Options.Secrets is nil: every secret lookup
// fails with errs.SecretMissing (rather than a nil-interface panic), and
// mutation is rejected as read-only.
type noSecretStore struct{}

func (noSecretStore) Get(name string) (string, error) {
	return "", errs.New(errs.SecretMissing, "no secret store configured").WithDetail("name", name)
}
func (noSecretStore) Set(string, string) error {
	return errs.New(errs.Invalid, "no secret store configured")
}
func (noSecretStore) Delete(string) error {
	return errs.New(errs.Invalid, "no secret store configured")
}
func (noSecretStore) List() ([]string, error) { return nil, nil }

// renderCallFlowYAML renders the minimal YAML snapshot for a Call()'s ad hoc
// one-step flow. It relies on JSON being a syntactic subset of YAML rather
// than pulling in a YAML encoder.
func renderCallFlowYAML(operationID string, params map[string]any, body any, headers map[string]string) string {
	var b strings.Builder
	b.WriteString("version: 1\nid: call\nsteps:\n  - id: call\n")
	fmt.Fprintf(&b, "    call: %s\n", operationID)
	if len(params) > 0 {
		if j, err := json.Marshal(params); err == nil {
			fmt.Fprintf(&b, "    input: %s\n", j)
		}
	}
	if body != nil {
		if j, err := json.Marshal(body); err == nil {
			fmt.Fprintf(&b, "    body: %s\n", j)
		}
	}
	if len(headers) > 0 {
		if j, err := json.Marshal(headers); err == nil {
			fmt.Fprintf(&b, "    headers: %s\n", j)
		}
	}
	return b.String()
}
