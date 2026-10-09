package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/device"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/expr"
	"github.com/gs-sinha/sapien/internal/runtime"
)

// maxPersistedLogLines caps body.logs.lines in the persisted step record;
// the full slice is always in the step's logcat.txt artifact.
const maxPersistedLogLines = 500

// uiDevice returns the run's device, opening it on the first ui step.
func (ec *execCtx) uiDevice(ctx context.Context) (device.Device, error) {
	if ec.device != nil {
		return ec.device, nil
	}
	if ec.opts.UIDevice == nil {
		return nil, errs.New(errs.Invalid, "this run has no device for ui steps").
			WithHint("ui steps run through the local engine (sapien flow run / run_flow), which drives a local Android emulator")
	}
	d, err := ec.opts.UIDevice(ctx)
	if err != nil {
		return nil, err
	}
	ec.device = d
	return d, nil
}

// closeDevice ends the run's device session, if one was opened.
func (ec *execCtx) closeDevice(ctx context.Context) {
	if ec.device != nil {
		_ = ec.device.Close(ctx)
		ec.device = nil
	}
}

// executeUIStep runs a ui step's actions in order against its app, then
// evaluates its extract and assert against the step's own value: out (from
// `read` actions and extract), body.logs (the app's logs and parsed API
// calls while the step ran), body.screen, body.actions. The first action
// that cannot be carried out, or the first failing assert_* action, ends
// the step; the screen and its page source are saved as artifacts.
func (r *Runner) executeUIStep(ctx context.Context, ec *execCtx, step domain.Step, idx int, stepsSoFar map[string]expr.StepValue) (domain.StepResult, expr.StepValue) {
	started := ec.now()
	result := domain.StepResult{StepID: step.ID, Index: idx, Operation: "ui:" + step.UI.App, Started: started}
	outMap := map[string]any{}
	redactor := runtime.NewRedactor(ec.opts.Env.Env.Redaction, nil)

	var assertions []domain.AssertionResult
	var actionRecs []any
	var body map[string]any
	var renderedActions []any

	finish := func(status domain.StepStatus, stepErr error) (domain.StepResult, expr.StepValue) {
		result.Status = status
		result.Attempts = 1
		result.Assertions = assertions
		result.Out = outMap
		result.Finished = ec.now()
		result.Request = &domain.RequestRecord{Method: "UI", URL: "app://" + step.UI.App, Body: map[string]any{"actions": renderedActions}}
		raw := expr.StepValue{Out: outMap, Request: map[string]any{"method": "UI", "url": "app://" + step.UI.App}}
		if body != nil {
			raw.Body = body
			result.Response = &domain.ResponseRecord{Body: persistedUIBody(body, redactor)}
		}
		if stepErr != nil {
			e := errs.As(stepErr)
			result.Error = &domain.ErrorInfo{Code: string(e.Code), Message: redactor.String(e.Message), Details: e.Details}
			if e.Hint != "" {
				result.Warnings = append(result.Warnings, e.Hint)
			}
		}
		return result, raw
	}

	scope := expr.Scope{Inputs: ec.run.Inputs, Env: ec.envVars, Steps: stepsSoFar, Iter: ec.iter}
	if step.When != "" {
		ok, werr := ec.eval.EvalBool(step.When, scope)
		if werr != nil {
			return finish(domain.StepErrored, werr)
		}
		if !ok {
			result.SkipReason = "when"
			return finish(domain.StepSkipped, nil)
		}
	}
	r.emitStep(ec, step.ID, domain.StepResolving, 0)

	if step.Timeout != "" {
		d, err := time.ParseDuration(step.Timeout)
		if err != nil {
			return finish(domain.StepErrored, errs.Wrap(errs.Invalid, err, "invalid timeout %q", step.Timeout))
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	actions := make([]domain.UIAction, len(step.UI.Actions))
	for i, a := range step.UI.Actions {
		ra, err := interpolateUIAction(ec.eval, a, scope)
		if err != nil {
			return finish(domain.StepErrored, err)
		}
		actions[i] = ra
		renderedActions = append(renderedActions, uiActionRecord(ra))
	}

	dev, err := ec.uiDevice(ctx)
	if err != nil {
		return finish(domain.StepErrored, err)
	}
	app, err := dev.App(ctx, step.UI.App)
	if err != nil {
		return finish(domain.StepErrored, err)
	}
	mark := app.LogMark()
	artifactDir := ec.artifactDir(step.ID)

	r.emitStep(ec, step.ID, domain.StepRequesting, 1)
	var failErr error
	for i, a := range actions {
		t0 := time.Now()
		outcome, aerr := app.Do(ctx, a)
		rec := uiActionRecord(a)
		rec["duration_ms"] = time.Since(t0).Milliseconds()
		if outcome.Warning != "" {
			result.Warnings = append(result.Warnings, outcome.Warning)
		}
		if a.Kind == domain.UIRead && aerr == nil {
			outMap[a.As] = outcome.Text
			rec["text"] = outcome.Text
		}
		if len(outcome.PNG) > 0 {
			name := a.Name
			if name == "" {
				name = fmt.Sprintf("action-%d", i+1)
			}
			ec.saveArtifact(&result, artifactDir, "screenshot", name+".png", outcome.PNG)
		}
		if as := outcome.Assert; as != nil {
			assertions = append(assertions, domain.AssertionResult{Expr: as.Expr, Passed: as.Passed, Actual: as.Actual, Message: as.Message})
			rec["passed"] = as.Passed
			actionRecs = append(actionRecs, rec)
			if !as.Passed {
				failErr = errAssertAction
				break
			}
			continue
		}
		if aerr != nil {
			rec["error"] = errs.As(aerr).Message
			actionRecs = append(actionRecs, rec)
			failErr = aerr
			break
		}
		rec["ok"] = true
		actionRecs = append(actionRecs, rec)
	}

	// The step's own value: logs while it ran, where the app ended up, and
	// what each action did -- built even for a failed step, since that is
	// when they matter most.
	lines, api := app.Logs(mark)
	apiAny := make([]any, len(api))
	for i, c := range api {
		apiAny[i] = c
	}
	linesAny := make([]any, len(lines))
	for i, l := range lines {
		linesAny[i] = l
	}
	body = map[string]any{
		"logs":    map[string]any{"api": apiAny, "lines": linesAny},
		"screen":  map[string]any{"activity": app.Activity(context.WithoutCancel(ctx))},
		"actions": actionRecs,
	}
	if len(lines) > 0 {
		var sb strings.Builder
		for _, l := range lines {
			sb.WriteString(redactor.Text(l))
			sb.WriteByte('\n')
		}
		ec.saveArtifact(&result, artifactDir, "logcat", "logcat.txt", []byte(sb.String()))
	}
	shotCtx := context.WithoutCancel(ctx)
	if png, err := app.Screenshot(shotCtx); err == nil {
		name := "final.png"
		if failErr != nil {
			name = "failure.png"
		}
		ec.saveArtifact(&result, artifactDir, "screenshot", name, png)
	}

	if failErr != nil {
		if failErr != errAssertAction {
			if src, err := app.Source(shotCtx); err == nil {
				ec.saveArtifact(&result, artifactDir, "source", "source.xml", []byte(src))
			}
		}
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			return finish(domain.StepCancelled, errs.Wrap(errs.Cancelled, ctx.Err(), "ui step cancelled"))
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return finish(domain.StepFailed, errs.New(device.ErrUIAction, "ui step timed out after %s: %s", step.Timeout, errs.As(failErr).Message))
		case failErr == errAssertAction:
			return finish(domain.StepFailed, nil)
		case errs.CodeOf(failErr) == device.ErrUIAction:
			return finish(domain.StepFailed, failErr)
		default:
			return finish(domain.StepErrored, failErr)
		}
	}

	// extract, then assert, exactly as a call step does (see executeStep),
	// against this step's own value.
	current := expr.StepValue{Body: body, Out: outMap, Request: map[string]any{"method": "UI", "url": "app://" + step.UI.App}}
	scope.Current = &current
	extractErrs := runExtract(ec.eval, step.Extract, &current, scope)

	r.emitStep(ec, step.ID, domain.StepAsserting, 1)
	for _, a := range step.Assert {
		ia, ierr := interpolateAssertion(ec.eval, a, scope)
		if ierr != nil {
			assertions = append(assertions, domain.AssertionResult{Expr: a.Expr, Error: annotateOutErr(ierr, extractErrs).Error(), Soft: a.Soft})
			continue
		}
		compiled, cerr := expr.CompileAssertion(ia)
		if cerr != nil {
			assertions = append(assertions, domain.AssertionResult{Expr: a.Expr, Error: cerr.Error(), Soft: a.Soft})
			continue
		}
		res := evalAssertion(ec.eval, compiled, scope, nil, extractErrs)
		res.Soft = a.Soft
		assertions = append(assertions, res)
	}
	for _, a := range assertions {
		if !a.Passed && !a.Soft {
			if len(extractErrs) > 0 {
				result.Warnings = append(result.Warnings, extractWarnings(extractErrs)...)
			}
			return finish(domain.StepFailed, nil)
		}
	}
	if len(extractErrs) > 0 {
		return finish(domain.StepErrored, firstExtractErr(extractErrs))
	}
	return finish(domain.StepPassed, nil)
}

// errAssertAction marks a step ended by a failing assert_* action: the
// failure is the recorded assertion itself, not an error.
var errAssertAction = errs.New(errs.AssertionFailed, "ui assertion failed")

// persistedUIBody is body as stored on the run: API records redacted, log
// lines redacted and capped (the artifact has them all).
func persistedUIBody(body map[string]any, redactor *runtime.Redactor) map[string]any {
	out := map[string]any{}
	for k, v := range body {
		out[k] = v
	}
	lg, _ := body["logs"].(map[string]any)
	if lg == nil {
		return out
	}
	lines, _ := lg["lines"].([]any)
	truncated := false
	if len(lines) > maxPersistedLogLines {
		lines = lines[len(lines)-maxPersistedLogLines:]
		truncated = true
	}
	red := make([]any, len(lines))
	for i, l := range lines {
		s, _ := l.(string)
		red[i] = redactor.Text(s)
	}
	newLogs := map[string]any{"api": redactor.Value(lg["api"]), "lines": red}
	if truncated {
		newLogs["lines_truncated"] = true
	}
	out["logs"] = newLogs
	return out
}

// interpolateUIAction renders every `${...}` in a's string arguments.
func interpolateUIAction(eval *expr.Evaluator, a domain.UIAction, scope expr.Scope) (domain.UIAction, error) {
	var firstErr error
	render := func(s string) string {
		if firstErr != nil || !strings.Contains(s, "${") {
			return s
		}
		v, _, err := eval.Interpolate(s, scope)
		if err != nil {
			firstErr = err
			return s
		}
		return headerString(v)
	}
	a.URL = render(a.URL)
	a.Text = render(a.Text)
	a.Eq = render(a.Eq)
	a.Contains = render(a.Contains)
	a.Matches = render(a.Matches)
	if a.Target != nil {
		t := *a.Target
		t.ID = render(t.ID)
		t.Text = render(t.Text)
		t.TextContains = render(t.TextContains)
		t.Hint = render(t.Hint)
		t.XPath = render(t.XPath)
		a.Target = &t
	}
	return a, firstErr
}

// uiActionRecord is the JSON shape of a rendered action in a step record.
func uiActionRecord(a domain.UIAction) map[string]any {
	m := map[string]any{"kind": a.Kind}
	if a.Target != nil {
		m["target"] = a.Target.String()
	}
	if a.Kind == domain.UIType {
		m["text"] = a.Text
	}
	if a.URL != "" {
		m["url"] = a.URL
	}
	return m
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// artifactDir is where a step's files go for this run (iteration-specific
// inside a loop block); "" when the run keeps no artifacts.
func (ec *execCtx) artifactDir(stepID string) string {
	if ec.opts.ArtifactsDir == "" {
		return ""
	}
	dir := stepID
	if ec.iter != nil {
		dir = fmt.Sprintf("%s.%d", stepID, ec.iter.Index)
	}
	if ec.phase != "" {
		dir = ec.phase + "-" + dir
	}
	return filepath.Join(ec.opts.ArtifactsDir, ec.run.ID, dir)
}

// saveArtifact writes data under dir and records it on result.
func (ec *execCtx) saveArtifact(result *domain.StepResult, dir, kind, name string, data []byte) {
	if dir == "" {
		return
	}
	name = unsafeFileChars.ReplaceAllString(name, "_")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return
	}
	result.Artifacts = append(result.Artifacts, domain.StepArtifact{Kind: kind, Name: name, Path: p})
}
