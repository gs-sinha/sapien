package local

import (
	"context"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/flow"
	"github.com/gs-sinha/sapien/internal/runner"
)

// runnerAPI implements engine.RunnerAPI over a Local (PLAN §9).
type runnerAPI struct{ l *Local }

var _ engine.RunnerAPI = (*runnerAPI)(nil)

// operationsAdapter adapts *catalog.Catalog to runner.Operations.
type operationsAdapter struct{ cat *catalog.Catalog }

var _ runner.Operations = (*operationsAdapter)(nil)

func (o *operationsAdapter) Operation(ctx context.Context, id string) (*domain.Operation, error) {
	return o.cat.GetOperation(ctx, id)
}

// RunFlow loads flowID and runs it.
func (r *runnerAPI) RunFlow(ctx context.Context, flowID string, opts engine.RunOptions) (*domain.Run, error) {
	l := r.l
	f, err := l.Flows().Get(ctx, flowID)
	if err != nil {
		return nil, err
	}
	return l.runFlow(ctx, f, opts)
}

// RunFlowSource parses and validates yamlSrc, then runs it without saving.
func (r *runnerAPI) RunFlowSource(ctx context.Context, yamlSrc string, opts engine.RunOptions) (*domain.Run, error) {
	l := r.l
	v := flow.NewValidator(&flowCatalogAdapter{cat: l.cat}, flow.WithExampleResolver(newFlowExampleResolver(l.Examples())))
	f, result := v.ValidateSource(ctx, yamlSrc)
	if !result.Valid {
		return nil, flowInvalidErr(result)
	}
	return l.runFlow(ctx, f, opts)
}

// Call executes a single operation as a one-step run.
func (r *runnerAPI) Call(ctx context.Context, req engine.CallRequest) (*domain.Run, error) {
	l := r.l
	resolved, err := l.resolveRunEnv(ctx, req.Env)
	if err != nil {
		return nil, err
	}
	runOpts := runner.Options{
		Env:             resolved,
		Secrets:         l.secrets,
		Runs:            l.runsStore,
		Bus:             l.bus,
		AllowProduction: req.AllowProduction,
		Trigger:         req.Trigger,
	}
	run, err := l.runner.Call(ctx, req.Operation, req.Params, req.Body, req.Headers, runOpts)
	// Usage feedback (search ranking tuning task, part 2). run.OperationHashes
	// is keyed by the operation's canonical ID (not req.Operation, which may
	// be any resolvable ref) regardless of whether the call itself passed,
	// failed, or errored — Run always resolves and records it before
	// executing a step, so this fires even for a run that fails an assertion.
	if run != nil {
		for opID := range run.OperationHashes {
			noteOperationUse(ctx, l, opID)
		}
	}
	return run, err
}

// Cancel cancels the in-flight run identified by runID.
func (r *runnerAPI) Cancel(ctx context.Context, runID string) error {
	if !r.l.runner.Cancel(runID) {
		return errs.New(errs.RunNotFound, "run %q not found or already finished", runID)
	}
	return nil
}

// runFlow resolves f's environment and executes it through the shared
// *runner.Runner, threading opts through to runner.Options.
func (l *Local) runFlow(ctx context.Context, f *domain.Flow, opts engine.RunOptions) (*domain.Run, error) {
	resolved, err := l.resolveRunEnv(ctx, opts.Environment)
	if err != nil {
		return nil, err
	}
	runOpts := runner.Options{
		Env:               resolved,
		Secrets:           l.secrets,
		Runs:              l.runsStore,
		Bus:               l.bus,
		Observer:          opts.Observer,
		ContinueOnFailure: opts.ContinueOnFailure,
		AllowProduction:   opts.AllowProduction,
		Trigger:           opts.Trigger,
	}
	resume, err := l.resumeFor(ctx, f, opts)
	if err != nil {
		return nil, err
	}
	runOpts.Resume = resume
	return l.runner.Run(ctx, f, opts.Inputs, runOpts)
}

// resumeFor turns engine.RunOptions' ResumeFrom/FromStep/UntilStep into a
// runner.Resume: the earlier run is loaded and must belong to the same flow
// (an ad-hoc source run carries the parsed flow's id, so it qualifies when
// the source's id matches). nil when the run is a normal, full run.
func (l *Local) resumeFor(ctx context.Context, f *domain.Flow, opts engine.RunOptions) (*runner.Resume, error) {
	if opts.ResumeFrom == "" && opts.FromStep == "" && opts.UntilStep == "" {
		return nil, nil
	}
	resume := &runner.Resume{FromStep: opts.FromStep, UntilStep: opts.UntilStep}
	if opts.ResumeFrom != "" {
		earlier, err := l.Runs().Get(ctx, opts.ResumeFrom)
		if err != nil {
			return nil, err
		}
		if earlier.FlowID != "" && f.ID != "" && earlier.FlowID != f.ID {
			return nil, errs.New(errs.Invalid, "run %s belongs to flow %q, not %q", earlier.ID, earlier.FlowID, f.ID).
				WithDetail("run_id", earlier.ID).WithDetail("flow_id", earlier.FlowID).
				WithHint("resume from a run of the same flow, or omit resume_from")
		}
		resume.From = earlier
	}
	return resume, nil
}

// resolveRunEnv loads envName (or the workspace default, when "") and
// resolves it against the registered services. No OpenAPI-servers fallback
// is used (PLAN wiring note): a service with no base_url in either the
// environment file or its own service.yaml is simply left unresolved,
// surfacing as a request-time error for the steps that need it.
func (l *Local) resolveRunEnv(ctx context.Context, envName string) (*env.Resolved, error) {
	envDoc, err := env.Load(l.ws, envName)
	if err != nil {
		return nil, err
	}
	services, err := l.cat.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	return env.Resolve(envDoc, services, nil), nil
}

// RunError turns a finished run's terminal status into an error for CLI
// exit codes (errs.ExitCode). It is not part of engine.Engine (which has no
// method for it); callers that need it call this package-level helper
// directly, the same way they'd call runner.ErrorFor if they had a
// *runner.Runner of their own.
func RunError(run *domain.Run) error {
	return runner.ErrorFor(run)
}
