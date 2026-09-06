package flow

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/expr"
)

// Catalog is the small slice of the API catalog the validator needs: does
// an operation exist, what should we suggest instead when it doesn't, and
// what fields does its request/response shape have. internal/catalog
// implements this against the workspace's normalized API model; tests
// implement it against a small in-memory fixture.
type Catalog interface {
	// Operation returns the operation named id, or ok == false if there is
	// no such operation.
	Operation(ctx context.Context, id string) (*domain.Operation, bool)
	// Suggest returns up to n operation IDs close to ref, best guess first,
	// for an unknown-operation diagnostic's Suggestions.
	Suggest(ctx context.Context, ref string, n int) []string
	// Fields returns the flattened field index for operationID (e.g.
	// "request.body.customerId", "response.201.body.orderId"), or nil if
	// the operation is unknown or has no indexed fields.
	Fields(ctx context.Context, operationID string) []domain.Field
}

// Validator checks a parsed flow against a Catalog, producing agent-facing
// diagnostics (PLAN.md §23.1).
type Validator struct {
	cat      Catalog
	resolver ExampleResolver
}

// Option configures a Validator constructed by NewValidator.
type Option func(*Validator)

// WithExampleResolver makes a step's `example:` reference (PLAN §34b)
// resolve against r: Validate materializes the flow through r before
// running every other check (catalog lookup, parameter binding, expression
// references), so those checks see the example's call/input/body/headers
// merged in exactly as they will run. Without this option (the default,
// nil resolver), any `example:` step is reported UNKNOWN_EXAMPLE -- a nil
// resolver still "resolves" in the sense that Materialize runs and finds
// nothing, rather than the check being skipped.
func WithExampleResolver(r ExampleResolver) Option {
	return func(v *Validator) { v.resolver = r }
}

// NewValidator returns a Validator backed by cat.
func NewValidator(cat Catalog, opts ...Option) *Validator {
	v := &Validator{cat: cat}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

var stepIDPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// Validate checks f -- structurally (version, steps, step ids), against the
// catalog (operation existence, deprecation, parameter binding, request
// body), every expression's static references, and (see WithExampleResolver)
// every step's `example:` reference -- and returns every finding. Valid is
// true iff no diagnostic has error severity.
func (v *Validator) Validate(ctx context.Context, f *domain.Flow) *domain.ValidationResult {
	_, res := v.validateMaterialized(ctx, f)
	return res
}

// validateMaterialized is Validate's implementation, additionally
// returning the materialized flow (f with every `example:` step expanded
// against v.resolver, see Materialize) so ValidateSource can hand callers
// -- Create, Update, a run driven through Flows().Get, get_flow -- the same
// expanded steps it just validated instead of the pre-expansion source.
func (v *Validator) validateMaterialized(ctx context.Context, f *domain.Flow) (*domain.Flow, *domain.ValidationResult) {
	if f == nil {
		return nil, &domain.ValidationResult{Diagnostics: []domain.Diagnostic{{
			Code: CodeSchema, Severity: domain.SeverityError, Message: "flow is nil",
		}}}
	}

	var diags []domain.Diagnostic

	if f.Version != 1 {
		diags = append(diags, domain.Diagnostic{
			Code: CodeFlowVersion, Severity: domain.SeverityError,
			Message: fmt.Sprintf("flow version must be 1, got %d", f.Version),
		})
	}
	if len(f.Steps) == 0 {
		diags = append(diags, domain.Diagnostic{
			Code: CodeNoSteps, Severity: domain.SeverityError,
			Message: "flow must have at least one step",
		})
	}

	matF, mdiags := Materialize(ctx, f, v.resolver)
	diags = append(diags, mdiags...)

	// combined walks setup, then main steps, then teardown, in that
	// execution order: a step's position in this slice is what
	// checkStepRef's ordering check (refIdx >= idx -> CodeStepOrder) is
	// compared against, so "a step may reference any setup step and
	// earlier steps in its own list; teardown may also reference every
	// setup and main step" (PLAN §8, §9) falls out of plain position
	// ordering without any extra rule: every setup position is less than
	// every main position, and every main position is less than every
	// teardown position.
	combined := AllSteps(matF)

	vd := &validation{
		ctx:        ctx,
		cat:        v.cat,
		flow:       matF,
		stepIndex:  map[string]int{},
		opByStepID: map[string]*domain.Operation{},
		fieldCache: map[string]*fieldIndex{},
	}
	for _, st := range combined {
		if st.ID != "" {
			vd.allStepIDs = append(vd.allStepIDs, st.ID)
		}
	}

	// Step ids must be unique across setup, steps, and teardown together
	// (PLAN §8), so seenIDs is not reset between lists.
	seenIDs := map[string]bool{}
	for i, st := range combined {
		switch {
		case !stepIDPattern.MatchString(st.ID):
			diags = append(diags, domain.Diagnostic{
				Code: CodeStepIDInvalid, Severity: domain.SeverityError,
				Message: fmt.Sprintf("step id `%s` is invalid; must match ^[a-zA-Z_][a-zA-Z0-9_-]*$", st.ID),
				Line:    st.Line, StepID: st.ID,
			})
		case seenIDs[st.ID]:
			diags = append(diags, domain.Diagnostic{
				Code: CodeStepIDDuplicate, Severity: domain.SeverityError,
				Message: fmt.Sprintf("duplicate step id `%s`", st.ID),
				Line:    st.Line, StepID: st.ID,
			})
		default:
			seenIDs[st.ID] = true
		}
		if _, exists := vd.stepIndex[st.ID]; !exists {
			vd.stepIndex[st.ID] = i
		}

		switch {
		case st.Call == "" && st.Example == "":
			diags = append(diags, missingCallOrExampleDiag(st.Line, st.ID))
			continue
		case st.Call == "":
			// st.Example was set but didn't resolve (or resolved with a
			// call/operation mismatch); Materialize already reported
			// UNKNOWN_EXAMPLE/EXAMPLE_OPERATION_MISMATCH for it above.
			continue
		}

		op, ok := v.cat.Operation(ctx, st.Call)
		if !ok {
			sugg := v.cat.Suggest(ctx, st.Call, 5)
			msg := fmt.Sprintf("unknown operation `%s`", st.Call)
			if len(sugg) > 0 {
				msg = fmt.Sprintf("unknown operation `%s`; did you mean `%s`?", st.Call, sugg[0])
			}
			diags = append(diags, domain.Diagnostic{
				Code: CodeUnknownOperation, Severity: domain.SeverityError,
				Message: msg, Line: st.Line, StepID: st.ID, Suggestions: sugg,
			})
			continue
		}
		vd.opByStepID[st.ID] = op
		if op.Deprecated {
			diags = append(diags, domain.Diagnostic{
				Code: CodeDeprecatedOperation, Severity: domain.SeverityWarning,
				Message: fmt.Sprintf("operation `%s` is deprecated", st.Call), Line: st.Line, StepID: st.ID,
			})
		}
	}

	for i, st := range combined {
		if op := vd.opByStepID[st.ID]; op != nil {
			diags = append(diags, checkBinding(st, op, vd.fieldsFor(op))...)
		}
		diags = append(diags, vd.checkExpressionsForStep(st, i)...)
		diags = append(diags, checkDurations(st)...)
	}

	diags = append(diags, findDuplicateExtracts(f)...)

	valid := true
	for _, d := range diags {
		if d.Severity == domain.SeverityError {
			valid = false
			break
		}
	}
	return matF, &domain.ValidationResult{Valid: valid, Diagnostics: diags}
}

// ValidateSource parses src and, if parsing succeeds, validates the result,
// returning the materialized flow (every step's `example:` reference
// expanded against the Validator's resolver, see WithExampleResolver and
// Materialize) so callers act on the same steps that were just checked. A
// parse failure (malformed YAML or a schema violation) is reported through
// the same ValidationResult shape, using the diagnostics Parse attached to
// the error, so callers have one result type regardless of where a flow
// fails. Never panics; never returns a nil result.
func (v *Validator) ValidateSource(ctx context.Context, src string) (*domain.Flow, *domain.ValidationResult) {
	f, err := Parse(src)
	if err != nil {
		var diags []domain.Diagnostic
		if e := errs.As(err); e != nil {
			if d, ok := e.Details["diagnostics"].([]domain.Diagnostic); ok {
				diags = d
			}
		}
		if len(diags) == 0 {
			diags = []domain.Diagnostic{{Code: CodeSchema, Severity: domain.SeverityError, Message: err.Error()}}
		}
		return nil, &domain.ValidationResult{Valid: false, Diagnostics: diags}
	}
	return v.validateMaterialized(ctx, f)
}

func checkDurations(st domain.Step) []domain.Diagnostic {
	var diags []domain.Diagnostic
	check := func(field, value string) {
		if value == "" {
			return
		}
		if _, err := time.ParseDuration(value); err != nil {
			diags = append(diags, domain.Diagnostic{
				Code: CodeInvalidDuration, Severity: domain.SeverityError,
				Message: fmt.Sprintf("invalid %s `%s`: %v", field, value, err),
				Line:    st.Line, StepID: st.ID,
			})
		}
	}
	check("timeout", st.Timeout)
	if st.Poll != nil {
		check("poll.interval", st.Poll.Interval)
		check("poll.timeout", st.Poll.Timeout)
	}
	return diags
}

// validation holds the state shared across one Validate call's per-step
// checks: the resolved operation for each step, the declared step order,
// and a cache of each operation's field index (Catalog.Fields is called at
// most once per distinct operation).
type validation struct {
	ctx  context.Context
	cat  Catalog
	flow *domain.Flow
	// stepIndex maps a step id to its first occurrence's position in the
	// combined setup+steps+teardown order (see AllSteps), which is what
	// checkStepRef's ordering check compares against.
	stepIndex  map[string]int
	opByStepID map[string]*domain.Operation
	allStepIDs []string
	fieldCache map[string]*fieldIndex
}

func (vd *validation) fieldsFor(op *domain.Operation) *fieldIndex {
	if op == nil {
		return nil
	}
	if fi, ok := vd.fieldCache[op.ID]; ok {
		return fi
	}
	fi := buildFieldIndex(vd.cat.Fields(vd.ctx, op.ID))
	vd.fieldCache[op.ID] = fi
	return fi
}

// ---- parameter binding & body checks -------------------------------------

type paramMaps struct {
	path   map[string]domain.Param
	query  map[string]domain.Param
	header map[string]domain.Param // keyed by lower-cased name
}

func buildParamMaps(op *domain.Operation) paramMaps {
	pm := paramMaps{path: map[string]domain.Param{}, query: map[string]domain.Param{}, header: map[string]domain.Param{}}
	for _, p := range op.Params {
		switch p.In {
		case domain.InPath:
			pm.path[p.Name] = p
		case domain.InQuery:
			pm.query[p.Name] = p
		case domain.InHeader:
			pm.header[strings.ToLower(p.Name)] = p
		}
	}
	return pm
}

func (pm paramMaps) matchesAny(key string) bool {
	if _, ok := pm.path[key]; ok {
		return true
	}
	if _, ok := pm.query[key]; ok {
		return true
	}
	if _, ok := pm.header[strings.ToLower(key)]; ok {
		return true
	}
	return false
}

func isProvided(p domain.Param, st domain.Step) bool {
	inInput := func(name string, ci bool) bool {
		for k := range st.Input {
			if (ci && strings.EqualFold(k, name)) || (!ci && k == name) {
				return true
			}
		}
		return false
	}
	switch p.In {
	case domain.InPath:
		if inInput(p.Name, false) {
			return true
		}
		if st.Params != nil {
			if _, ok := st.Params.Path[p.Name]; ok {
				return true
			}
		}
	case domain.InQuery:
		if inInput(p.Name, false) {
			return true
		}
		if st.Params != nil {
			if _, ok := st.Params.Query[p.Name]; ok {
				return true
			}
		}
	case domain.InHeader:
		if inInput(p.Name, true) {
			return true
		}
		if st.Params != nil {
			for k := range st.Params.Headers {
				if strings.EqualFold(k, p.Name) {
					return true
				}
			}
		}
		// A plain `headers:` entry is the same bytes on the wire as a
		// header param bound by name, so it satisfies the param too. This
		// is what lets a saved example (whose recorded headers come back
		// canonicalised, e.g. "Orgid") and `sapien call -H orgId:1` behave
		// like `-p orgId=1`.
		for k := range st.Headers {
			if strings.EqualFold(k, p.Name) {
				return true
			}
		}
	}
	return false
}

func paramType(p domain.Param) string {
	if p.Schema != nil && p.Schema.Kind != "" {
		return string(p.Schema.Kind)
	}
	return "any"
}

func paramSuggestionList(op *domain.Operation) []string {
	out := make([]string, 0, len(op.Params))
	for _, p := range op.Params {
		out = append(out, fmt.Sprintf("%s (%s)", p.Name, p.In))
	}
	return out
}

func unknownInputDiag(st domain.Step, op *domain.Operation, key string) domain.Diagnostic {
	return domain.Diagnostic{
		Code: CodeUnknownInputName, Severity: domain.SeverityError,
		Message:     fmt.Sprintf("unknown input name `%s` for operation `%s`", key, op.ID),
		Line:        st.Line,
		StepID:      st.ID,
		Suggestions: paramSuggestionList(op),
	}
}

// checkBinding validates a step's input/params/body against op: unknown
// parameter names, missing required parameters, and the request body's
// presence and (best-effort) top-level shape.
func checkBinding(st domain.Step, op *domain.Operation, fi *fieldIndex) []domain.Diagnostic {
	var diags []domain.Diagnostic
	pm := buildParamMaps(op)

	inputKeys := make([]string, 0, len(st.Input))
	for k := range st.Input {
		inputKeys = append(inputKeys, k)
	}
	sort.Strings(inputKeys)
	for _, key := range inputKeys {
		if !pm.matchesAny(key) {
			diags = append(diags, unknownInputDiag(st, op, key))
		}
	}

	if st.Params != nil {
		diags = append(diags, checkExplicitParamKeys(st, op, st.Params.Path, pm.path)...)
		diags = append(diags, checkExplicitParamKeys(st, op, st.Params.Query, pm.query)...)
		if len(st.Params.Headers) > 0 {
			keys := make([]string, 0, len(st.Params.Headers))
			for k := range st.Params.Headers {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if _, ok := pm.header[strings.ToLower(key)]; !ok {
					diags = append(diags, unknownInputDiag(st, op, key))
				}
			}
		}
	}

	for _, p := range op.Params {
		if !p.Required || isProvided(p, st) {
			continue
		}
		diags = append(diags, domain.Diagnostic{
			Code: CodeMissingRequiredParam, Severity: domain.SeverityError,
			Message: fmt.Sprintf("missing required param `%s` (%s, %s)", p.Name, p.In, paramType(p)),
			Line:    st.Line, StepID: st.ID,
		})
	}

	if op.RequestBody != nil && op.RequestBody.Required && st.Body == nil {
		diags = append(diags, domain.Diagnostic{
			Code: CodeMissingBody, Severity: domain.SeverityError,
			Message: fmt.Sprintf("operation `%s` requires a request body", op.ID),
			Line:    st.Line, StepID: st.ID,
		})
	}
	if st.Body != nil && op.RequestBody == nil {
		diags = append(diags, domain.Diagnostic{
			Code: CodeUnexpectedBody, Severity: domain.SeverityWarning,
			Message: fmt.Sprintf("operation `%s` does not take a request body", op.ID),
			Line:    st.Line, StepID: st.ID,
		})
	}
	if bodyMap, ok := st.Body.(map[string]any); ok && op.RequestBody != nil && fi != nil && len(fi.requestTop) > 0 {
		keys := make([]string, 0, len(bodyMap))
		for k := range bodyMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !fi.requestTop[k] {
				diags = append(diags, domain.Diagnostic{
					Code: CodeUnknownBodyField, Severity: domain.SeverityWarning,
					Message:     fmt.Sprintf("unknown body field `%s` for operation `%s`", k, op.ID),
					Line:        st.Line,
					StepID:      st.ID,
					Suggestions: sortedKeys(fi.requestTop),
				})
			}
		}
	}
	return diags
}

func checkExplicitParamKeys(st domain.Step, op *domain.Operation, values map[string]any, known map[string]domain.Param) []domain.Diagnostic {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var diags []domain.Diagnostic
	for _, k := range keys {
		if _, ok := known[k]; !ok {
			diags = append(diags, unknownInputDiag(st, op, k))
		}
	}
	return diags
}

// ---- field index (request/response shapes) -------------------------------

// fieldIndex is the per-operation, precomputed view buildFieldIndex derives
// from Catalog.Fields: top-level request body field names, and the lowest
// 2xx response's body field names (top-level, for the first-segment check,
// and in full for the deeper-segment check).
type fieldIndex struct {
	requestTop  map[string]bool
	responseTop map[string]bool
	responseAll map[string]bool // full relative path, e.g. "items[].riderId"
}

var responseBodyRe = regexp.MustCompile(`^response\.(\d{3})\.body\.(.+)$`)

func buildFieldIndex(fields []domain.Field) *fieldIndex {
	fi := &fieldIndex{requestTop: map[string]bool{}, responseTop: map[string]bool{}, responseAll: map[string]bool{}}
	for _, f := range fields {
		if rest, ok := stripPrefix(f.Path, "request.body."); ok {
			fi.requestTop[topSegment(rest)] = true
		}
	}
	prefix := lowestSuccessBodyPrefix(fields)
	if prefix == "" {
		return fi
	}
	for _, f := range fields {
		if rest, ok := stripPrefix(f.Path, prefix); ok {
			fi.responseTop[topSegment(rest)] = true
			fi.responseAll[rest] = true
		}
	}
	return fi
}

func stripPrefix(path, prefix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return "", false
	}
	return rest, true
}

func topSegment(rest string) string {
	if idx := strings.IndexAny(rest, ".["); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

// lowestSuccessBodyPrefix finds the lowest 2xx status among fields'
// "response.<status>.body.*" paths and returns "response.<status>.body.",
// or "" if none has any indexed body field.
func lowestSuccessBodyPrefix(fields []domain.Field) string {
	best := -1
	for _, f := range fields {
		m := responseBodyRe.FindStringSubmatch(f.Path)
		if m == nil {
			continue
		}
		status, err := strconv.Atoi(m[1])
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		if best == -1 || status < best {
			best = status
		}
	}
	if best == -1 {
		return ""
	}
	return fmt.Sprintf("response.%d.body.", best)
}

// normalizeArrayPath rewrites a Ref.Path such as ["items", "0", "riderId"]
// (from `body.items[0].riderId`) into ["items[]", "riderId"], matching the
// field index's array convention.
func normalizeArrayPath(path []string) []string {
	out := make([]string, 0, len(path))
	for _, seg := range path {
		if isDigits(seg) && len(out) > 0 {
			out[len(out)-1] += "[]"
			continue
		}
		out = append(out, seg)
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ---- expression static-reference checks ----------------------------------

// checkExpressionsForStep collects every expression a step evaluates
// (templates inside input/params/body/headers; bare CEL in until, extract,
// and assert) and checks each one's syntax and static references.
func (vd *validation) checkExpressionsForStep(st domain.Step, idx int) []domain.Diagnostic {
	op := vd.opByStepID[st.ID]
	var diags []domain.Diagnostic

	add := func(text string, hasCurrent, secretAllowed bool, line int) {
		diags = append(diags, vd.checkExprText(text, st, op, idx, hasCurrent, secretAllowed, line)...)
	}
	walk := func(v any, secretAllowed bool) {
		for _, t := range collectTemplates(v) {
			add(t, false, secretAllowed, st.Line)
		}
	}

	walk(st.Input, false)
	if st.Params != nil {
		walk(st.Params.Path, false)
		walk(st.Params.Query, false)
		walk(stringMapToAny(st.Params.Headers), true)
	}
	walk(st.Body, false)
	walk(stringMapToAny(st.Headers), true)

	if st.Until != "" {
		add(st.Until, true, false, st.Line)
	}
	if len(st.Extract) > 0 {
		names := make([]string, 0, len(st.Extract))
		for name := range st.Extract {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			add(st.Extract[name], true, false, st.Line)
		}
	}
	for _, a := range st.Assert {
		if a.Expr != "" {
			add(a.Expr, true, false, a.Line)
			continue
		}
		for _, t := range assertionTemplates(a) {
			add(t, true, false, a.Line)
		}
		compiled, err := expr.CompileAssertion(a)
		if err != nil {
			diags = append(diags, domain.Diagnostic{
				Code: CodeAssertionInvalid, Severity: domain.SeverityError,
				Message: err.Error(), Line: a.Line, StepID: st.ID,
			})
			continue
		}
		if compiled.Kind == "cel" {
			add(compiled.Expr, true, false, a.Line)
		}
	}
	return diags
}

func (vd *validation) checkExprText(text string, st domain.Step, op *domain.Operation, idx int, hasCurrent, secretAllowed bool, line int) []domain.Diagnostic {
	if err := expr.Parse(text); err != nil {
		return []domain.Diagnostic{{
			Code: CodeExprSyntax, Severity: domain.SeverityError,
			Message: err.Error(), Line: line, StepID: st.ID,
		}}
	}
	var diags []domain.Diagnostic
	for _, r := range expr.Roots(text) {
		diags = append(diags, vd.checkRef(r, st, op, idx, hasCurrent, secretAllowed, line)...)
	}
	return diags
}

func (vd *validation) checkRef(r expr.Ref, st domain.Step, op *domain.Operation, idx int, hasCurrent, secretAllowed bool, line int) []domain.Diagnostic {
	switch r.Root {
	case "steps":
		return vd.checkStepRef(r, st, idx, line)
	case "inputs":
		return vd.checkInputRef(r, st, line)
	case "env":
		return nil
	case "secret":
		if secretAllowed {
			return nil
		}
		name := ""
		if len(r.Path) > 0 {
			name = r.Path[0]
		}
		return []domain.Diagnostic{{
			Code: CodeSecretContext, Severity: domain.SeverityError,
			Message: fmt.Sprintf("`secret.%s` is only allowed in headers or params.headers values", name),
			Line:    line, StepID: st.ID,
		}}
	case "body":
		if !hasCurrent {
			return contextRootDiag("body", st, line)
		}
		if op != nil {
			return vd.checkFieldPath(op, r.Path, st, line)
		}
		return nil
	case "status", "headers", "latency_ms", "request", "out":
		if !hasCurrent {
			return contextRootDiag(r.Root, st, line)
		}
		return nil
	default:
		return nil
	}
}

func contextRootDiag(root string, st domain.Step, line int) []domain.Diagnostic {
	return []domain.Diagnostic{{
		Code: CodeContextRoot, Severity: domain.SeverityError,
		Message: fmt.Sprintf("`%s` is only available inside this step's own assert/extract/until; reference an earlier step's response with steps.<id>.%s", root, root),
		Line:    line, StepID: st.ID,
	}}
}

func (vd *validation) checkStepRef(r expr.Ref, st domain.Step, idx int, line int) []domain.Diagnostic {
	refIdx, ok := vd.stepIndex[r.StepID]
	if !ok {
		return []domain.Diagnostic{{
			Code: CodeUnknownStep, Severity: domain.SeverityError,
			Message:     fmt.Sprintf("unknown step `%s`", r.StepID),
			Line:        line,
			StepID:      st.ID,
			Suggestions: append([]string(nil), vd.allStepIDs...),
		}}
	}
	if refIdx >= idx {
		return []domain.Diagnostic{{
			Code: CodeStepOrder, Severity: domain.SeverityError,
			Message: fmt.Sprintf("step `%s` runs after `%s`", r.StepID, st.ID),
			Line:    line, StepID: st.ID,
		}}
	}
	if len(r.Path) >= 1 && r.Path[0] == "body" {
		if refOp := vd.opByStepID[r.StepID]; refOp != nil {
			return vd.checkFieldPath(refOp, r.Path[1:], st, line)
		}
	}
	return nil
}

func (vd *validation) checkInputRef(r expr.Ref, st domain.Step, line int) []domain.Diagnostic {
	if len(r.Path) == 0 {
		return nil
	}
	name := r.Path[0]
	if vd.flow.Inputs != nil {
		if _, ok := vd.flow.Inputs[name]; ok {
			return nil
		}
	}
	return []domain.Diagnostic{{
		Code: CodeUnknownFlowInput, Severity: domain.SeverityError,
		Message:     fmt.Sprintf("unknown flow input `%s`", name),
		Line:        line,
		StepID:      st.ID,
		Suggestions: inputNames(vd.flow),
	}}
}

func inputNames(f *domain.Flow) []string {
	out := make([]string, 0, len(f.Inputs))
	for k := range f.Inputs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (vd *validation) checkFieldPath(op *domain.Operation, path []string, st domain.Step, line int) []domain.Diagnostic {
	if len(path) == 0 {
		return nil
	}
	fi := vd.fieldsFor(op)
	if fi == nil || len(fi.responseTop) == 0 {
		return nil // no indexed 2xx body fields for this operation; skip
	}
	first := path[0]
	if !fi.responseTop[first] {
		return []domain.Diagnostic{{
			Code: CodeUnknownField, Severity: domain.SeverityError,
			Message:     fmt.Sprintf("unknown field `%s` on `%s`'s response", first, op.ID),
			Line:        line,
			StepID:      st.ID,
			Suggestions: nearestSuggestions(first, sortedKeys(fi.responseTop), 5),
		}}
	}
	if len(path) > 1 {
		joined := strings.Join(normalizeArrayPath(path), ".")
		if !fi.responseAll[joined] {
			return []domain.Diagnostic{{
				Code: CodeUnknownField, Severity: domain.SeverityWarning,
				Message:     fmt.Sprintf("unknown field `%s` on `%s`'s response", strings.Join(path, "."), op.ID),
				Line:        line,
				StepID:      st.ID,
				Suggestions: nearestSuggestions(joined, sortedKeys(fi.responseAll), 5),
			}}
		}
	}
	return nil
}

// ---- duplicate extract names (source-based) ------------------------------

// findDuplicateExtracts re-parses f.Source's yaml.Node tree to catch a
// literal duplicate key in a step's `extract:` mapping -- information a
// decoded map[string]string (domain.Step.Extract) can no longer distinguish
// from a single key, since the second occurrence just overwrites the first
// during decode. A flow with no Source (e.g. built programmatically rather
// than via Parse) can't be checked this way and is silently skipped. Checks
// `setup:` and `teardown:` the same way as `steps:`.
func findDuplicateExtracts(f *domain.Flow) []domain.Diagnostic {
	if f == nil || f.Source == "" {
		return nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(f.Source), &root); err != nil {
		return nil
	}
	doc := docContent(&root)
	if doc == nil {
		return nil
	}

	var diags []domain.Diagnostic
	for _, key := range []string{"setup", "steps", "teardown"} {
		diags = append(diags, findDuplicateExtractsIn(mapValue(doc, key))...)
	}
	return diags
}

// findDuplicateExtractsIn checks one steps-list node (`setup:`, `steps:`,
// or `teardown:`) for a duplicate key within any step's `extract:` mapping.
func findDuplicateExtractsIn(stepsNode *yaml.Node) []domain.Diagnostic {
	if stepsNode == nil || stepsNode.Kind != yaml.SequenceNode {
		return nil
	}

	var diags []domain.Diagnostic
	for _, stepNode := range stepsNode.Content {
		if stepNode.Kind != yaml.MappingNode {
			continue
		}
		extractNode := mapValue(stepNode, "extract")
		if extractNode == nil || extractNode.Kind != yaml.MappingNode {
			continue
		}
		stepID := ""
		if idNode := mapValue(stepNode, "id"); idNode != nil {
			stepID = idNode.Value
		}
		seen := map[string]bool{}
		for k := 0; k+1 < len(extractNode.Content); k += 2 {
			key := extractNode.Content[k]
			if seen[key.Value] {
				diags = append(diags, domain.Diagnostic{
					Code: CodeDuplicateExtract, Severity: domain.SeverityError,
					Message: fmt.Sprintf("duplicate extract name `%s`", key.Value),
					Line:    key.Line, StepID: stepID,
				})
				continue
			}
			seen[key.Value] = true
		}
	}
	return diags
}

func docContent(root *yaml.Node) *yaml.Node {
	if root == nil || root.Kind == 0 {
		return nil
	}
	n := root
	for n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind == yaml.DocumentNode {
		return nil
	}
	return n
}

func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
