package runner

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/expr"
	"github.com/gs-sinha/sapien/internal/runtime"
)

const (
	defaultPollInterval = time.Second
	defaultPollTimeout  = 30 * time.Second
)

// executeStep drives one step through resolving -> requesting -> (polling)*
// -> asserting -> a terminal status (PLAN.md §9). It returns the persisted
// StepResult alongside the raw (unredacted) expr.StepValue later steps use
// to reference this one as steps.<id>.
func (r *Runner) executeStep(ctx context.Context, ec *execCtx, step domain.Step, idx int, op *domain.Operation, stepsSoFar map[string]expr.StepValue) (domain.StepResult, expr.StepValue) {
	started := ec.now()
	result := domain.StepResult{StepID: step.ID, Index: idx, Operation: op.ID, Started: started}
	outMap := map[string]any{}

	var usedSecrets []string
	var req runtime.Request
	var haveReq bool
	var resp *runtime.Response
	var assertions []domain.AssertionResult
	attempts := 0

	fail := func(status domain.StepStatus, err error) (domain.StepResult, expr.StepValue) {
		return assembleStep(ec, result, status, err, usedSecrets, req, haveReq, resp, assertions, outMap, attempts, started)
	}

	// `when` is checked before anything else: no resolving event, no
	// request, no assertions, and (PLAN §34f.7) the step is left out of
	// stepsSoFar entirely -- the caller (Run) skips its usual `stepsSoFar[id]
	// = raw` assignment when it sees Status == StepSkipped coming back from
	// here. An evaluation error fails the step exactly like a bad assert
	// expression does. ec.iter is non-nil exactly when step is a loop
	// block's nested step (PLAN §34f.8), giving `when` (and everything else
	// below) access to iter.item/iter.index alongside this block iteration's
	// siblings via stepsSoFar.
	if step.When != "" {
		ok, werr := ec.eval.EvalBool(step.When, expr.Scope{Inputs: ec.run.Inputs, Env: ec.envVars, Steps: stepsSoFar, Iter: ec.iter})
		if werr != nil {
			return fail(domain.StepErrored, werr)
		}
		if !ok {
			result.SkipReason = "when"
			return fail(domain.StepSkipped, nil)
		}
	}

	r.emitStep(ec, step.ID, domain.StepResolving, 0)

	plainScope := func() expr.Scope {
		return expr.Scope{Inputs: ec.run.Inputs, Env: ec.envVars, Steps: stepsSoFar, Iter: ec.iter}
	}
	secretScope := func() expr.Scope {
		s := plainScope()
		s.AllowSecrets = true
		s.SecretResolver = func(name string) (string, error) { return ec.secrets.Get(name) }
		return s
	}

	if op.HTTP == nil {
		return fail(domain.StepErrored, errs.New(errs.Invalid, "operation %s has no HTTP binding", op.ID))
	}

	interpInputAny, u, err := ec.eval.InterpolateValue(step.Input, plainScope())
	usedSecrets = append(usedSecrets, u...)
	if err != nil {
		return fail(domain.StepErrored, err)
	}
	inputMap, _ := interpInputAny.(map[string]any)

	var explicitPath, explicitQuery map[string]any
	var explicitHeaders map[string]string
	if step.Params != nil {
		var pv, qv any
		pv, u, err = ec.eval.InterpolateValue(step.Params.Path, plainScope())
		usedSecrets = append(usedSecrets, u...)
		if err != nil {
			return fail(domain.StepErrored, err)
		}
		explicitPath, _ = pv.(map[string]any)

		qv, u, err = ec.eval.InterpolateValue(step.Params.Query, plainScope())
		usedSecrets = append(usedSecrets, u...)
		if err != nil {
			return fail(domain.StepErrored, err)
		}
		explicitQuery, _ = qv.(map[string]any)

		explicitHeaders, u, err = interpolateHeaderMap(ec.eval, step.Params.Headers, secretScope())
		usedSecrets = append(usedSecrets, u...)
		if err != nil {
			return fail(domain.StepErrored, err)
		}
	}

	interpBody, u, err := ec.eval.InterpolateValue(step.Body, plainScope())
	usedSecrets = append(usedSecrets, u...)
	if err != nil {
		return fail(domain.StepErrored, err)
	}

	interpHeaders, u, err := interpolateHeaderMap(ec.eval, step.Headers, secretScope())
	usedSecrets = append(usedSecrets, u...)
	if err != nil {
		return fail(domain.StepErrored, err)
	}

	// Bind: Input keys resolve by name against the operation's declared
	// path/query/header parameters; Params.* is the explicit, disambiguated
	// form and always wins on collision.
	pathParams := map[string]any{}
	queryParams := map[string]any{}
	headerParams := map[string]string{}
	for k, v := range inputMap {
		p, ok := findParam(op, k)
		if !ok {
			return fail(domain.StepErrored, errs.New(errs.Invalid,
				"operation %s has no path/query/header parameter named %q", op.ID, k).WithDetail("param", k))
		}
		switch p.In {
		case domain.InPath:
			pathParams[p.Name] = v
		case domain.InQuery:
			queryParams[p.Name] = v
		case domain.InHeader:
			headerParams[p.Name] = headerString(v)
		}
	}
	for k, v := range explicitPath {
		pathParams[k] = v
	}
	for k, v := range explicitQuery {
		queryParams[k] = v
	}
	for k, v := range explicitHeaders {
		headerParams[k] = v
	}

	baseURL, err := ec.opts.Env.BaseURL(op.ServiceID)
	if err != nil {
		return fail(domain.StepErrored, err)
	}

	urlStr, err := runtime.BuildURL(baseURL, op.HTTP.Path, pathParams, queryParams)
	if err != nil {
		return fail(domain.StepErrored, err)
	}

	scratch, err := http.NewRequest(op.HTTP.Method, urlStr, nil)
	if err != nil {
		return fail(domain.StepErrored, errs.Wrap(errs.Invalid, err, "building request for %s", op.ID))
	}
	authUsed, err := env.ApplyAuth(scratch, ec.opts.Env.AuthFor(op.ServiceID), ec.secrets)
	if err != nil {
		return fail(domain.StepErrored, err)
	}
	usedSecrets = append(usedSecrets, authUsed...)

	finalHeaders := map[string]string{}
	for k, v := range headerParams {
		finalHeaders[k] = v
	}
	for k, vs := range scratch.Header {
		finalHeaders[k] = strings.Join(vs, ", ")
	}
	for k, v := range interpHeaders {
		finalHeaders[k] = v
	}

	contentType := "application/json"
	if op.RequestBody != nil && op.RequestBody.ContentType != "" {
		contentType = op.RequestBody.ContentType
	}

	var timeout time.Duration
	if step.Timeout != "" {
		timeout, err = time.ParseDuration(step.Timeout)
		if err != nil {
			return fail(domain.StepErrored, errs.Wrap(errs.Invalid, err, "invalid timeout %q", step.Timeout))
		}
	}

	req = runtime.Request{
		Method:      op.HTTP.Method,
		URL:         scratch.URL.String(),
		Headers:     finalHeaders,
		Body:        interpBody,
		ContentType: contentType,
		Timeout:     timeout,
	}
	haveReq = true

	pollInterval := defaultPollInterval
	pollTimeout := defaultPollTimeout
	if step.Poll != nil {
		if step.Poll.Interval != "" {
			pollInterval, err = time.ParseDuration(step.Poll.Interval)
			if err != nil {
				return fail(domain.StepErrored, errs.Wrap(errs.Invalid, err, "invalid poll.interval %q", step.Poll.Interval))
			}
		}
		if step.Poll.Timeout != "" {
			pollTimeout, err = time.ParseDuration(step.Poll.Timeout)
			if err != nil {
				return fail(domain.StepErrored, errs.Wrap(errs.Invalid, err, "invalid poll.timeout %q", step.Poll.Timeout))
			}
		}
	}

	deadline := ec.now().Add(pollTimeout)
	lastStatus := 0

	for {
		attempts++
		r.emitStep(ec, step.ID, domain.StepRequesting, attempts)

		var doErr error
		resp, doErr = ec.client.Do(ctx, req)
		if doErr != nil {
			if errs.CodeOf(doErr) == errs.Cancelled {
				return fail(domain.StepCancelled, doErr)
			}
			if step.Until == "" {
				return fail(domain.StepErrored, doErr)
			}
			if ec.now().After(deadline) {
				return fail(domain.StepFailed, untilTimeoutErr(attempts, lastStatus))
			}
			r.emitStep(ec, step.ID, domain.StepPolling, attempts)
			if serr := ec.sleep(ctx, pollInterval); serr != nil {
				return fail(domain.StepCancelled, errs.Wrap(errs.Cancelled, serr, "poll wait cancelled"))
			}
			continue
		}

		lastStatus = resp.Status
		if step.Until == "" {
			break
		}

		current := stepValueFromResponse(req, resp)
		ok, evalErr := ec.eval.EvalBool(step.Until, expr.Scope{
			Inputs: ec.run.Inputs, Env: ec.envVars, Steps: stepsSoFar, Current: &current, Iter: ec.iter,
		})
		if evalErr == nil && ok {
			break
		}
		if ec.now().After(deadline) {
			return fail(domain.StepFailed, untilTimeoutErr(attempts, lastStatus))
		}
		r.emitStep(ec, step.ID, domain.StepPolling, attempts)
		if serr := ec.sleep(ctx, pollInterval); serr != nil {
			return fail(domain.StepCancelled, errs.Wrap(errs.Cancelled, serr, "poll wait cancelled"))
		}
	}

	// Asserting.
	r.emitStep(ec, step.ID, domain.StepAsserting, attempts)
	current := stepValueFromResponse(req, resp)
	scope := expr.Scope{Inputs: ec.run.Inputs, Env: ec.envVars, Steps: stepsSoFar, Current: &current, Iter: ec.iter}

	for _, a := range step.Assert {
		ia, ierr := interpolateAssertion(ec.eval, a, scope)
		if ierr != nil {
			assertions = append(assertions, domain.AssertionResult{Expr: a.Expr, Passed: false, Error: ierr.Error(), Soft: a.Soft})
			continue
		}
		compiled, cerr := expr.CompileAssertion(ia)
		if cerr != nil {
			assertions = append(assertions, domain.AssertionResult{Expr: a.Expr, Passed: false, Error: cerr.Error(), Soft: a.Soft})
			continue
		}
		res := evalAssertion(ec.eval, compiled, scope, op)
		res.Soft = a.Soft
		assertions = append(assertions, res)
	}

	// Extract, in a deterministic (sorted) order so a later expression in
	// the same step may reference an earlier one's output via `out`.
	if len(step.Extract) > 0 {
		names := make([]string, 0, len(step.Extract))
		for name := range step.Extract {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			current.Out = outMap
			scope.Current = &current
			v, eerr := ec.eval.Eval(step.Extract[name], scope)
			if eerr != nil {
				e := errs.As(eerr).WithDetail("extract", name)
				return fail(domain.StepErrored, e)
			}
			outMap[name] = v
		}
	}

	// A soft assertion that did not hold is a warning on the step, never a
	// failure (PLAN §34d follow-up from the 58-step field report).
	anyFailed := false
	for _, a := range assertions {
		if !a.Passed && !a.Soft {
			anyFailed = true
			break
		}
	}
	if anyFailed {
		return fail(domain.StepFailed, nil)
	}
	return fail(domain.StepPassed, nil)
}

// assembleStep builds the final StepResult/StepValue pair for any exit path
// (success or failure), redacting the persisted request/response/error while
// leaving the returned expr.StepValue raw for later steps to reference.
func assembleStep(ec *execCtx, result domain.StepResult, status domain.StepStatus, stepErr error, usedSecrets []string, req runtime.Request, haveReq bool, resp *runtime.Response, assertions []domain.AssertionResult, outMap map[string]any, attempts int, started time.Time) (domain.StepResult, expr.StepValue) {
	redactor := runtime.NewRedactor(ec.opts.Env.Env.Redaction, usedSecrets)

	result.Status = status
	result.Attempts = attempts
	result.Assertions = assertions
	result.Out = outMap
	result.Started = started
	result.Finished = ec.now()

	var raw expr.StepValue
	switch {
	case resp != nil:
		rr := redactor.RequestRecord(resp.Request)
		result.Request = &rr
		rsp := redactor.ResponseRecord(resp)
		result.Response = &rsp
		tm := resp.Timings
		result.Timings = &tm
		raw = expr.StepValue{
			Request:   requestToMap(resp.Request),
			Status:    resp.Status,
			Headers:   lowerHeaderMap(resp.Headers),
			Body:      resp.Body,
			LatencyMs: resp.Timings.TotalMs,
			Out:       outMap,
		}
	case haveReq:
		rr := redactor.RequestRecord(req)
		result.Request = &rr
		raw = expr.StepValue{Request: requestToMap(req), Out: outMap}
	default:
		raw = expr.StepValue{Out: outMap}
	}

	if stepErr != nil {
		e := errs.As(stepErr)
		result.Error = &domain.ErrorInfo{
			Code:    string(e.Code),
			Message: redactor.String(e.Message),
			Details: e.Details,
		}
	}
	return result, raw
}

func untilTimeoutErr(attempts, lastStatus int) error {
	return errs.New(errs.UntilTimeout, "poll timed out after %d attempt(s)", attempts).
		WithDetail("attempts", attempts).
		WithDetail("last_status", lastStatus)
}

// interpolateHeaderMap interpolates each value of a string-keyed header map.
// A value's native type (when it is exactly one ${expr}) is stringified,
// since header values are always strings on the wire.
func interpolateHeaderMap(eval *expr.Evaluator, m map[string]string, scope expr.Scope) (map[string]string, []string, error) {
	if len(m) == 0 {
		return nil, nil, nil
	}
	out := make(map[string]string, len(m))
	var used []string
	for k, v := range m {
		val, u, err := eval.Interpolate(v, scope)
		if err != nil {
			return nil, nil, err
		}
		used = append(used, u...)
		out[k] = headerString(val)
	}
	return out, used, nil
}

// findParam finds a path/query/header parameter by name: path and query
// names must match exactly; header names match case-insensitively.
func findParam(op *domain.Operation, name string) (*domain.Param, bool) {
	for i := range op.Params {
		p := &op.Params[i]
		switch p.In {
		case domain.InHeader:
			if strings.EqualFold(p.Name, name) {
				return p, true
			}
		case domain.InPath, domain.InQuery:
			if p.Name == name {
				return p, true
			}
		}
	}
	return nil, false
}

// pickResponseSchema selects the response schema for status: an exact status
// match first, then a "NXX" pattern, then "default".
func pickResponseSchema(op *domain.Operation, status int) (*domain.Schema, bool) {
	exact := strconv.Itoa(status)
	pattern := fmt.Sprintf("%dXX", status/100)
	var def *domain.Schema
	haveDefault := false
	for i := range op.Responses {
		resp := &op.Responses[i]
		if resp.Status == exact {
			return resp.Schema, resp.Schema != nil
		}
	}
	for i := range op.Responses {
		resp := &op.Responses[i]
		if strings.EqualFold(resp.Status, pattern) {
			return resp.Schema, resp.Schema != nil
		}
	}
	for i := range op.Responses {
		resp := &op.Responses[i]
		if resp.Status == "default" {
			def = resp.Schema
			haveDefault = true
		}
	}
	if haveDefault {
		return def, def != nil
	}
	return nil, false
}

// interpolateAssertion returns a copy of a with any `${...}` templates in
// its structured comparison values -- eq, neq, contains, and matches (when
// matches is itself a template) -- interpolated against scope, before the
// assertion is compiled to CEL (PLAN §8). This is what makes
// `eq: "${steps.allocate.out.riderId}"` compare against the value that step
// produced rather than the literal template text. A value that is exactly
// one ${expr} keeps its native type, so a templated `eq` against a number
// stays numeric rather than becoming a string. scope must not allow
// secrets: secret.* is never permitted in an assertion value.
func interpolateAssertion(eval *expr.Evaluator, a domain.Assertion, scope expr.Scope) (domain.Assertion, error) {
	var err error
	if a.Eq != nil {
		if a.Eq, _, err = eval.InterpolateValue(a.Eq, scope); err != nil {
			return a, err
		}
	}
	if a.Neq != nil {
		if a.Neq, _, err = eval.InterpolateValue(a.Neq, scope); err != nil {
			return a, err
		}
	}
	if a.Contains != nil {
		if a.Contains, _, err = eval.InterpolateValue(a.Contains, scope); err != nil {
			return a, err
		}
	}
	if a.Matches != "" && expr.IsTemplate(a.Matches) {
		v, _, ierr := eval.Interpolate(a.Matches, scope)
		if ierr != nil {
			return a, ierr
		}
		a.Matches = matchesString(v)
	}
	return a, nil
}

// matchesString coerces an interpolated `matches` value to the string its
// regex pattern needs. matches is ordinarily already a string; a templated
// value that evaluates to a non-string (e.g. a number) is stringified
// rather than rejected.
func matchesString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func evalAssertion(eval *expr.Evaluator, compiled expr.Compiled, scope expr.Scope, op *domain.Operation) domain.AssertionResult {
	ar := domain.AssertionResult{Expr: compiled.Expr, Message: compiled.Message}

	if compiled.Kind == "schema" {
		status := 0
		if scope.Current != nil {
			status = scope.Current.Status
		}
		schema, ok := pickResponseSchema(op, status)
		if !ok {
			ar.Message = fmt.Sprintf("no response schema for status %d in contract", status)
			return ar
		}
		var body any
		if scope.Current != nil {
			body = scope.Current.Body
		}
		problems := ValidateSchema(schema, body)
		if len(problems) > 0 {
			if len(problems) > 5 {
				problems = problems[:5]
			}
			ar.Message = strings.Join(problems, "; ")
			return ar
		}
		ar.Passed = true
		return ar
	}

	ok, err := eval.EvalBool(compiled.Expr, scope)
	if err != nil {
		ar.Error = err.Error()
		return ar
	}
	ar.Passed = ok
	if actual, dok := expr.Describe(compiled.Expr, scope); dok {
		ar.Actual = actual
	}
	return ar
}

func stepValueFromResponse(req runtime.Request, resp *runtime.Response) expr.StepValue {
	return expr.StepValue{
		Request:   requestToMap(req),
		Status:    resp.Status,
		Headers:   lowerHeaderMap(resp.Headers),
		Body:      resp.Body,
		LatencyMs: resp.Timings.TotalMs,
		Out:       map[string]any{},
	}
}

func requestToMap(req runtime.Request) map[string]any {
	return map[string]any{
		"method":  req.Method,
		"url":     req.URL,
		"headers": lowerHeaderMap(req.Headers),
		"body":    req.Body,
	}
}

func lowerHeaderMap(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = v
	}
	return out
}

func headerString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if !math.IsInf(t, 0) && !math.IsNaN(t) && t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}
