package flow

import (
	"context"
	"fmt"

	"github.com/gs-sinha/sapien/internal/domain"
)

// ExampleResolver is the small, read-only surface Materialize (and, through
// it, Validate) needs to resolve a step's `example:` reference (PLAN §34b),
// mirroring Catalog's shape for operations: existence plus "did you mean"
// suggestions for an unknown id.
type ExampleResolver interface {
	// Example returns the saved example named id, or ok == false if there
	// is no such example.
	Example(ctx context.Context, id string) (*domain.SavedExample, bool)
	// SuggestExamples returns up to n example IDs close to ref, best guess
	// first, for an UNKNOWN_EXAMPLE diagnostic's Suggestions.
	SuggestExamples(ctx context.Context, ref string, n int) []string
}

// allSteps returns f's setup, main, and teardown steps concatenated, in the
// order they execute: setup, then steps, then teardown. This is the order
// the validator assigns step positions in for `steps.<id>` reference checks
// (PLAN §8: a step may reference any setup step and earlier steps in its
// own list; teardown may also reference every setup and main step) and the
// order Uses/Materialize walk the three lists in.
func AllSteps(f *domain.Flow) []domain.Step {
	if f == nil {
		return nil
	}
	out := make([]domain.Step, 0, len(f.Setup)+len(f.Steps)+len(f.Teardown))
	out = append(out, f.Setup...)
	out = append(out, f.Steps...)
	out = append(out, f.Teardown...)
	return out
}

// Materialize resolves every step's `example:` reference against r,
// filling the step's call/input/body/headers from the saved example (PLAN
// §8, §34b), and returns a new *domain.Flow with those steps expanded so
// every consumer -- validate, create, run, get_flow -- sees the same
// bindings. f itself is not mutated; a step with no `example` is copied
// through unchanged. Step.Example is kept on the result for display.
//
// Merge rules (explicit step fields win throughout): `call`, when set on
// the step, must equal the example's operation -- otherwise
// EXAMPLE_OPERATION_MISMATCH is reported and the step's own `call` is kept
// as authoritative; when the step has no `call`, the example's operation
// fills it. `input` keys and `headers` merge per key with the step's value
// winning on a collision. `body` on the step, when set, replaces the
// example's entirely (never merged: bodies are not maps in general).
// `${...}` templates inside the example's fields are copied as-is, so they
// interpolate at run time exactly like a step's own templates, and are
// checked the same way by Validate.
//
// A step whose `example` doesn't resolve (r is nil, or the id is unknown to
// it) is returned unchanged plus an UNKNOWN_EXAMPLE diagnostic. A step with
// neither `call` nor `example` is also returned unchanged; Materialize
// leaves that one for Validate to report (missingCallOrExampleDiag), since
// resolving nothing isn't this function's problem to name.
//
// Setup and Teardown are materialized the same way as Steps, independently:
// an `example:` reference in either resolves against r exactly like one in
// the main step list.
func Materialize(ctx context.Context, f *domain.Flow, r ExampleResolver) (*domain.Flow, []domain.Diagnostic) {
	if f == nil {
		return f, nil
	}
	out := *f
	var diags []domain.Diagnostic
	out.Setup, diags = materializeSteps(ctx, f.Setup, r, diags)
	out.Steps, diags = materializeSteps(ctx, f.Steps, r, diags)
	out.Teardown, diags = materializeSteps(ctx, f.Teardown, r, diags)
	return &out, diags
}

// materializeSteps materializes one list (Setup, Steps, or Teardown),
// appending its diagnostics to diags. A nil/empty steps returns nil so an
// omitted `setup:`/`teardown:` round-trips as the zero value.
func materializeSteps(ctx context.Context, steps []domain.Step, r ExampleResolver, diags []domain.Diagnostic) ([]domain.Step, []domain.Diagnostic) {
	if len(steps) == 0 {
		return nil, diags
	}
	out := make([]domain.Step, len(steps))
	for i, st := range steps {
		merged, d := materializeStep(ctx, st, r)
		out[i] = merged
		diags = append(diags, d...)
	}
	return out, diags
}

func materializeStep(ctx context.Context, st domain.Step, r ExampleResolver) (domain.Step, []domain.Diagnostic) {
	if st.Example == "" {
		return st, nil
	}

	var ex *domain.SavedExample
	var ok bool
	if r != nil {
		ex, ok = r.Example(ctx, st.Example)
	}
	if !ok {
		var sugg []string
		if r != nil {
			sugg = r.SuggestExamples(ctx, st.Example, 5)
		}
		msg := fmt.Sprintf("unknown example `%s`", st.Example)
		if len(sugg) > 0 {
			msg = fmt.Sprintf("unknown example `%s`; did you mean `%s`?", st.Example, sugg[0])
		}
		return st, []domain.Diagnostic{{
			Code: CodeUnknownExample, Severity: domain.SeverityError,
			Message: msg, Line: st.Line, StepID: st.ID, Suggestions: sugg,
		}}
	}

	merged := st
	var diags []domain.Diagnostic
	switch {
	case merged.Call == "":
		merged.Call = ex.Operation
	case merged.Call != ex.Operation:
		diags = append(diags, domain.Diagnostic{
			Code: CodeExampleOperationMismatch, Severity: domain.SeverityError,
			Message: fmt.Sprintf("step calls `%s` but example `%s` is for `%s`", st.Call, st.Example, ex.Operation),
			Line:    st.Line, StepID: st.ID,
		})
	}
	merged.Input = mergeAnyMaps(ex.Input, st.Input)
	if st.Body != nil {
		merged.Body = st.Body
	} else {
		merged.Body = ex.Body
	}
	merged.Headers = mergeStringMaps(ex.Headers, st.Headers)
	return merged, diags
}

// mergeAnyMaps merges base and override, override winning per key. Returns
// nil (rather than an empty, non-nil map) when both are empty so a
// no-example step's zero-value Input round-trips unchanged.
func mergeAnyMaps(base, override map[string]any) map[string]any {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeStringMaps is mergeAnyMaps for Headers (map[string]string).
func mergeStringMaps(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}
