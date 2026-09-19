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

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/expr"
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

	// combined walks setup, then main steps, then teardown, recursing one
	// level into each loop block's own nested steps right after the block
	// (see AllSteps): a step's position in this slice is what
	// checkStepRef's ordering check (refIdx >= idx -> CodeStepOrder) is
	// compared against, so "a step may reference any setup step and
	// earlier steps in its own list; teardown may also reference every
	// setup and main step" (PLAN §8, §9) falls out of plain position
	// ordering without any extra rule: every setup position is less than
	// every main position, and every main position is less than every
	// teardown position. PLAN §34f.8 relaxes ordering further for a step
	// referencing a sibling inside its own block; see blockOf below.
	combined := AllSteps(matF)

	vd := &validation{
		ctx:          ctx,
		cat:          v.cat,
		flow:         matF,
		stepIndex:    map[string]int{},
		opByStepID:   map[string]*domain.Operation{},
		fieldCache:   map[string]*fieldIndex{},
		maybeSkipped: map[string]bool{},
		hasWhen:      map[string]bool{},
		blockOf:      map[string]string{},
	}
	for _, fs := range combined {
		st := fs.Step
		if st.ID != "" {
			vd.allStepIDs = append(vd.allStepIDs, st.ID)
		}
		vd.blockOf[st.ID] = fs.Parent
		// maybeSkipped records every step whose result may be absent from
		// `steps` at run time even though it's a valid, in-order reference:
		// a step with its own `when`, or (PLAN §34f.8) any step nested in a
		// loop block, since the block may run zero iterations, stop early
		// (break_when/on_error), or simply not have reached this iteration
		// yet on the first pass. See checkStepRef's MAYBE_SKIPPED check.
		if st.When != "" || fs.Parent != "" {
			vd.maybeSkipped[st.ID] = true
		}
		if st.When != "" {
			vd.hasWhen[st.ID] = true
		}
	}

	// Step ids must be unique across setup, steps, teardown, and every
	// nested block together (PLAN §8, §34f.8), so seenIDs is not reset
	// between lists or between a block and its parent.
	seenIDs := map[string]bool{}
	for i, fs := range combined {
		st := fs.Step
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

		diags = append(diags, checkBlockShape(fs)...)
		if st.IsBlock() {
			// A block never has call/example (BLOCK_SHAPE already reported
			// it if it does); nothing further to resolve against the
			// catalog for the block step itself.
			continue
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

	for i, fs := range combined {
		st := fs.Step
		if op := vd.opByStepID[st.ID]; op != nil {
			diags = append(diags, checkBinding(st, op, vd.fieldsFor(op))...)
		}
		if !st.IsBlock() {
			// blockCtx is fs.Parent: a nested step's own when/input/body/
			// headers/until/extract/assert may reference any sibling in the
			// same block (subject to MAYBE_SKIPPED), and sees `loop`.
			diags = append(diags, vd.checkExpressionsForStep(st, i, fs.Parent, fs.Parent != "")...)
		} else {
			// A block's own `when` (if any) behaves like any top-level
			// step's: evaluated before any iteration, no sibling/loop
			// access. Its block-only fields (foreach, repeat.until/while,
			// break_when) get their own treatment (see checkBlockFields):
			// foreach like `when` above, the other three with sameBlock
			// access to the block's own children plus `loop`.
			if st.When != "" {
				diags = append(diags, vd.checkExprText(st.When, "when", st, nil, i, false, false, false, st.Line, fs.Parent)...)
			}
			diags = append(diags, vd.checkBlockFields(st, i)...)
		}
		diags = append(diags, checkDurations(st)...)
	}

	diags = append(diags, findDuplicateExtracts(f)...)

	diags = dedupeDiagnostics(diags)

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
	if st.Repeat != nil {
		check("repeat.interval", st.Repeat.Interval)
	}
	return diags
}

// callOnlyFieldsSet reports whether st sets any field that only makes sense
// on a call step (never on a loop block): example, input, params, body,
// headers, extract, assert, until, poll, timeout. `call` and `when` are
// checked separately -- `call` because BLOCK_SHAPE names it specially, and
// `when` because it is valid on both a block and a call step (PLAN §34f.7).
func callOnlyFieldsSet(st domain.Step) bool {
	return st.Example != "" || len(st.Input) > 0 || st.Params != nil || st.Body != nil ||
		len(st.Headers) > 0 || len(st.Extract) > 0 || len(st.Assert) > 0 ||
		st.Until != "" || st.Poll != nil || st.Timeout != ""
}

// blockOnlyFieldsSet reports whether st sets any field that only makes
// sense on a loop block (never on a call step): foreach, repeat, max,
// break_when, on_error.
func blockOnlyFieldsSet(st domain.Step) bool {
	return st.Foreach != "" || st.Repeat != nil || st.Max != 0 || st.BreakWhen != "" || st.OnError != ""
}

// checkBlockShape validates a step's shape (PLAN §34f.8): a block (Steps
// set) must not carry a call-only field or `call` itself, must set exactly
// one of foreach/repeat, and a repeat block must set max (1..1000) and at
// least one of until/while; a call step (no Steps) must not carry a
// block-only field; a block nested inside another block is NESTED_LOOP, and
// a block in setup/teardown is LOOP_IN_PHASE (both checked here too, since
// they are also a shape problem at heart).
func checkBlockShape(fs FlatStep) []domain.Diagnostic {
	st := fs.Step
	var diags []domain.Diagnostic
	shape := func(format string, args ...any) {
		diags = append(diags, domain.Diagnostic{
			Code: CodeBlockShape, Severity: domain.SeverityError,
			Message: fmt.Sprintf(format, args...), Line: st.Line, StepID: st.ID,
		})
	}

	if !st.IsBlock() {
		if blockOnlyFieldsSet(st) {
			shape("step `%s` sets a loop-only field (foreach/repeat/max/break_when/on_error) but has no `steps:`", st.ID)
		}
		return diags
	}

	if fs.Parent != "" {
		diags = append(diags, domain.Diagnostic{
			Code: CodeNestedLoop, Severity: domain.SeverityError,
			Message: fmt.Sprintf("block `%s` is nested inside block `%s`; loop blocks cannot nest", st.ID, fs.Parent),
			Line:    st.Line, StepID: st.ID,
		})
	}
	if fs.Phase != "" {
		diags = append(diags, domain.Diagnostic{
			Code: CodeLoopInPhase, Severity: domain.SeverityError,
			Message: fmt.Sprintf("block `%s` is in %s:, which may not contain loop blocks", st.ID, fs.Phase),
			Line:    st.Line, StepID: st.ID,
		})
	}

	if st.Call != "" {
		shape("block `%s` sets `call`; a block runs its nested `steps:`, not an operation of its own", st.ID)
	}
	if callOnlyFieldsSet(st) {
		shape("block `%s` sets a call-only field (example/input/params/body/headers/extract/assert/until/poll/timeout)", st.ID)
	}

	switch {
	case st.Foreach == "" && st.Repeat == nil:
		shape("block `%s` sets neither `foreach` nor `repeat`", st.ID)
	case st.Foreach != "" && st.Repeat != nil:
		shape("block `%s` sets both `foreach` and `repeat`; exactly one is allowed", st.ID)
	case st.Foreach != "":
		if st.Max != 0 && (st.Max < 1 || st.Max > 1000) {
			shape("block `%s`: max must be 1..1000, got %d", st.ID, st.Max)
		}
	case st.Repeat != nil:
		if st.Max != 0 {
			shape("block `%s`: `max` applies to foreach; a repeat block sets `repeat.max` instead", st.ID)
		}
		if st.Repeat.Max < 1 || st.Repeat.Max > 1000 {
			shape("block `%s`: repeat.max is required and must be 1..1000, got %d", st.ID, st.Repeat.Max)
		}
		if st.Repeat.Until == "" && st.Repeat.While == "" {
			shape("block `%s`: repeat requires at least one of until/while", st.ID)
		}
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
	// maybeSkipped is the set of step ids a `steps.<id>` reference should be
	// has()-guarded against (MAYBE_SKIPPED); see its assignment above.
	maybeSkipped map[string]bool
	// hasWhen is the subset of maybeSkipped that carries its own `when`.
	hasWhen map[string]bool
	// blockOf maps a step id to its immediately enclosing loop block's id,
	// or "" for a top-level step or a block itself (PLAN §34f.8; blocks
	// cannot nest). checkStepRef uses it to relax ordering between two
	// steps of the same block.
	blockOf map[string]string
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
// blockCtx and hasIter describe the step's own position (PLAN §34f.8): for
// a nested step, blockCtx is its parent block's id (giving its own
// expressions sameBlock access to sibling steps) and hasIter is true (`iter`
// is available); both are "" / false for a top-level step.
func (vd *validation) checkExpressionsForStep(st domain.Step, idx int, blockCtx string, hasIter bool) []domain.Diagnostic {
	op := vd.opByStepID[st.ID]
	var diags []domain.Diagnostic

	add := func(text, field string, hasCurrent, secretAllowed bool, line int) {
		diags = append(diags, vd.checkExprText(text, field, st, op, idx, hasCurrent, secretAllowed, hasIter, line, blockCtx)...)
	}
	walk := func(v any, secretAllowed bool, field string) {
		for _, t := range collectTemplates(v) {
			add(t, field, false, secretAllowed, st.Line)
		}
	}

	// `when` gates the whole step and is evaluated before the request is
	// built, so it sees the same roots as input/body/headers (no current
	// step context) and is checked here first.
	if st.When != "" {
		add(st.When, "when", false, false, st.Line)
	}

	walk(st.Input, false, "input")
	if st.Params != nil {
		walk(st.Params.Path, false, "params.path")
		walk(st.Params.Query, false, "params.query")
		walk(stringMapToAny(st.Params.Headers), true, "params.headers")
	}
	walk(st.Body, false, "body")
	walk(stringMapToAny(st.Headers), true, "headers")

	if st.Until != "" {
		add(st.Until, "until", true, false, st.Line)
	}
	if len(st.Extract) > 0 {
		names := make([]string, 0, len(st.Extract))
		for name := range st.Extract {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			add(st.Extract[name], "extract."+name, true, false, st.Line)
		}
	}
	for _, a := range st.Assert {
		if a.Expr != "" {
			add(a.Expr, "assert", true, false, a.Line)
			continue
		}
		for _, t := range assertionTemplates(a) {
			add(t, "assert", true, false, a.Line)
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
			add(compiled.Expr, "assert", true, false, a.Line)
		}
	}
	return diags
}

// checkBlockFields checks a loop block's own block-only expressions
// (PLAN §34f.8): `foreach` is evaluated once, before any iteration, so it
// gets the same (no sibling/loop) treatment as a top-level step's `when`;
// `repeat.until`, `repeat.while`, and `break_when` are evaluated once per
// iteration and so get sameBlock access to the block's own nested steps
// (blockCtx = the block's own id) plus `loop`.
func (vd *validation) checkBlockFields(st domain.Step, idx int) []domain.Diagnostic {
	var diags []domain.Diagnostic
	if st.Foreach != "" {
		diags = append(diags, vd.checkExprText(st.Foreach, "foreach", st, nil, idx, false, false, false, st.Line, vd.blockOf[st.ID])...)
	}
	if st.Repeat != nil {
		if st.Repeat.Until != "" {
			diags = append(diags, vd.checkExprText(st.Repeat.Until, "repeat.until", st, nil, idx, false, false, true, st.Line, st.ID)...)
		}
		if st.Repeat.While != "" {
			diags = append(diags, vd.checkExprText(st.Repeat.While, "repeat.while", st, nil, idx, false, false, true, st.Line, st.ID)...)
		}
	}
	if st.BreakWhen != "" {
		diags = append(diags, vd.checkExprText(st.BreakWhen, "break_when", st, nil, idx, false, false, true, st.Line, st.ID)...)
	}
	return diags
}

// checkExprText checks one CEL-typed field's raw text (field names it, for
// diagnostics, e.g. "when", "until", "extract.tid", "foreach"): first that
// any `${...}` template in it -- bare, or inside a string literal --
// expands cleanly (expr.ExpandTemplates; a `${` that survives, unterminated
// or left behind by a string literal that never closes, is
// CodeTemplateInExpr, not the generic syntax error below, since nothing
// downstream would otherwise explain it), then the usual syntax
// (expr.Parse) and static-reference (expr.Roots/checkRef) checks -- which
// see the SAME expanded source internally, so a reference that was only
// visible inside a template gets STEP_ORDER/UNKNOWN_STEP/MAYBE_SKIPPED/etc
// exactly like one written as bare CEL.
func (vd *validation) checkExprText(text, field string, st domain.Step, op *domain.Operation, idx int, hasCurrent, secretAllowed, hasIter bool, line int, blockCtx string) []domain.Diagnostic {
	templateDiag := func(msg string) []domain.Diagnostic {
		return []domain.Diagnostic{{
			Code: CodeTemplateInExpr, Severity: domain.SeverityError,
			Message: fmt.Sprintf("step `%s`'s `%s` %s", st.ID, field, msg),
			Line:    line, StepID: st.ID,
		}}
	}
	expanded, xerr := expr.ExpandTemplates(text)
	if xerr != nil {
		return templateDiag(fmt.Sprintf("has a malformed `${...}` template: %s", xerr.Error()))
	}
	if strings.Contains(expanded, "${") {
		return templateDiag("has a `${...}` template that could not be expanded here; check for an unterminated string around it")
	}

	if err := expr.Parse(text); err != nil {
		return []domain.Diagnostic{{
			Code: CodeExprSyntax, Severity: domain.SeverityError,
			Message: err.Error(), Line: line, StepID: st.ID,
		}}
	}
	var diags []domain.Diagnostic
	for _, r := range expr.Roots(text) {
		diags = append(diags, vd.checkRef(r, st, op, idx, hasCurrent, secretAllowed, hasIter, line, text, blockCtx)...)
	}
	return diags
}

func (vd *validation) checkRef(r expr.Ref, st domain.Step, op *domain.Operation, idx int, hasCurrent, secretAllowed, hasIter bool, line int, text, blockCtx string) []domain.Diagnostic {
	switch r.Root {
	case "steps":
		return vd.checkStepRef(r, st, idx, line, text, blockCtx)
	case "inputs":
		return vd.checkInputRef(r, st, line)
	case "env":
		return nil
	case "iter":
		if !hasIter {
			return []domain.Diagnostic{{
				Code: CodeContextRoot, Severity: domain.SeverityError,
				Message: "`iter` is only available inside a loop block's own nested-step expressions (or its break_when/repeat.until/repeat.while), not in its foreach/when",
				Line:    line, StepID: st.ID,
			}}
		}
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
	case "status", "headers", "latency_ms", "request":
		if !hasCurrent {
			return contextRootDiag(r.Root, st, line)
		}
		return nil
	case "out":
		if !hasCurrent {
			return contextRootDiag("out", st, line)
		}
		return vd.checkOutRef(r, st, line)
	default:
		return nil
	}
}

// checkOutRef validates an `out.<name>` reference (assert/until/extract, all
// of which reach here with hasCurrent true) against st's own `extract:`:
// `<name>` must be a key that step declares, since `out` only ever holds
// this step's own extracted values (PLAN's doc promise for both assert and
// until -- see internal/runner/step.go's extract-before-assert ordering).
// A reference with no path segment at all (bare `out`, e.g. `size(out) == 0`
// or `has(out.tid)`'s own `out` operand handled elsewhere) needs no name
// check.
func (vd *validation) checkOutRef(r expr.Ref, st domain.Step, line int) []domain.Diagnostic {
	if len(r.Path) == 0 {
		return nil
	}
	name := r.Path[0]
	if _, ok := st.Extract[name]; ok {
		return nil
	}
	names := make([]string, 0, len(st.Extract))
	for n := range st.Extract {
		names = append(names, n)
	}
	sort.Strings(names)
	return []domain.Diagnostic{{
		Code: CodeUnknownOut, Severity: domain.SeverityError,
		Message:     fmt.Sprintf("out.%s is not extracted by step `%s`; add it to `extract:` or fix the name", name, st.ID),
		Line:        line,
		StepID:      st.ID,
		Suggestions: names,
	}}
}

// dedupeDiagnostics drops an exact repeat of an earlier diagnostic (same
// code, severity, message, line, column, step, and suggestions).
// checkExpressionsForStep checks a structured assertion's `eq`/`neq`/etc.
// value for step/input/field references twice -- once via its own
// extracted `${...}` text, once via the compiled CEL text the last `add`
// call always checks (needed for the assertion's `path` itself) -- and
// since ExpandTemplates now lets that second pass see through into the same
// template the first pass already checked (e.g. a `steps.<id>` inside a
// templated `eq`), a single reference could otherwise be reported twice.
func dedupeDiagnostics(diags []domain.Diagnostic) []domain.Diagnostic {
	seen := make(map[string]bool, len(diags))
	out := make([]domain.Diagnostic, 0, len(diags))
	for _, d := range diags {
		key := strings.Join([]string{
			d.Code, string(d.Severity), d.Message,
			strconv.Itoa(d.Line), strconv.Itoa(d.Column), d.StepID,
			strings.Join(d.Suggestions, "\x1f"),
		}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

func contextRootDiag(root string, st domain.Step, line int) []domain.Diagnostic {
	return []domain.Diagnostic{{
		Code: CodeContextRoot, Severity: domain.SeverityError,
		Message: fmt.Sprintf("`%s` is only available inside this step's own assert/extract/until; reference an earlier step's response with steps.<id>.%s", root, root),
		Line:    line, StepID: st.ID,
	}}
}

// checkStepRef validates a steps.<id> reference. blockCtx is the checking
// step's own block context (see checkExpressionsForStep/checkBlockFields):
// when the referenced step is nested in the SAME block (blockCtx != "" and
// vd.blockOf[r.StepID] == blockCtx), the usual forward-only ordering rule
// is relaxed -- a sibling may reference another sibling in either
// declaration order, since steps.<id> always resolves to that id's latest
// execution, which on iteration N>=1 may be a sibling that (in declaration
// order) comes "after" this one but already ran on iteration N-1 (PLAN
// §34f.8). MAYBE_SKIPPED still applies to that reference like any other.
func (vd *validation) checkStepRef(r expr.Ref, st domain.Step, idx int, line int, text, blockCtx string) []domain.Diagnostic {
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
	sameBlock := blockCtx != "" && vd.blockOf[r.StepID] == blockCtx
	if refIdx >= idx && !sameBlock {
		return []domain.Diagnostic{{
			Code: CodeStepOrder, Severity: domain.SeverityError,
			Message: fmt.Sprintf("step `%s` runs after `%s`", r.StepID, st.ID),
			Line:    line, StepID: st.ID,
		}}
	}
	var diags []domain.Diagnostic
	if len(r.Path) >= 1 && r.Path[0] == "body" {
		if refOp := vd.opByStepID[r.StepID]; refOp != nil {
			diags = append(diags, vd.checkFieldPath(refOp, r.Path[1:], st, line)...)
		}
	}
	// Inside one iteration an earlier sibling with no `when` of its own has
	// always run by the time a later sibling reads it (had it failed, the
	// iteration would have ended there), so that one reference needs no
	// guard: the block-level reasons a nested step may be absent -- zero
	// iterations, an early break -- cannot apply to a reader that is itself
	// running inside the same iteration.
	ranThisIteration := sameBlock && refIdx < idx && !vd.hasWhen[r.StepID]
	if vd.maybeSkipped[r.StepID] && !ranThisIteration && !hasSkipGuard(text, r.StepID) {
		diags = append(diags, domain.Diagnostic{
			Code: CodeMaybeSkipped, Severity: domain.SeverityWarning,
			Message: fmt.Sprintf("step `%s` may be skipped; guard this reference with has(steps.%s) or steps.?%s", r.StepID, r.StepID, r.StepID),
			Line:    line, StepID: st.ID,
		})
	}
	return diags
}

// hasSkipGuard reports whether text (the whole expression or template body
// containing the steps.<stepID> reference being checked) appears to guard
// against stepID being skipped, via has(steps.<stepID> or the
// optional-chaining steps.?<stepID> syntax anywhere in text. This is
// deliberately a textual check, not a structural one: it does not verify
// the guard actually dominates this specific reference (an unrelated
// has(steps.other_step) earlier in a long `&&` chain would still silence
// the warning here), and a guard written in a different field/expression
// than the reference is invisible to it. MAYBE_SKIPPED is a warning, never
// an error, precisely because this heuristic can both over- and
// under-fire; see PLAN §34f.7/8.
func hasSkipGuard(text, stepID string) bool {
	return strings.Contains(text, "has(steps."+stepID) || strings.Contains(text, "steps.?"+stepID)
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
// `teardown:`, or a loop block's own nested `steps:`) for a duplicate key
// within any step's `extract:` mapping, recursing into a block's own
// `steps:` node (PLAN §34f.8; blocks cannot nest, so one level suffices).
func findDuplicateExtractsIn(stepsNode *yaml.Node) []domain.Diagnostic {
	if stepsNode == nil || stepsNode.Kind != yaml.SequenceNode {
		return nil
	}

	var diags []domain.Diagnostic
	for _, stepNode := range stepsNode.Content {
		if stepNode.Kind != yaml.MappingNode {
			continue
		}
		stepID := ""
		if idNode := mapValue(stepNode, "id"); idNode != nil {
			stepID = idNode.Value
		}
		if nestedSteps := mapValue(stepNode, "steps"); nestedSteps != nil {
			diags = append(diags, findDuplicateExtractsIn(nestedSteps)...)
			continue
		}
		extractNode := mapValue(stepNode, "extract")
		if extractNode == nil || extractNode.Kind != yaml.MappingNode {
			continue
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
