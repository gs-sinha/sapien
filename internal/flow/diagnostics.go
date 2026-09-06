package flow

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/spec"
)

// Diagnostic codes (domain.Diagnostic.Code), grouped by the pass that
// produces them. See Reference()'s "Diagnostic codes" section for the
// agent-facing one-line meaning of each.
const (
	// From Parse: the flow YAML failed the schema.
	CodeFlowVersion = "FLOW_VERSION"
	CodeNoSteps     = "NO_STEPS"
	CodeReservedKey = "RESERVED_KEY"
	CodeUnknownKey  = "UNKNOWN_KEY"
	CodeSchema      = "SCHEMA"

	// Structural, checked by Validate directly on the parsed flow.
	CodeStepIDDuplicate  = "STEP_ID_DUPLICATE"
	CodeStepIDInvalid    = "STEP_ID_INVALID"
	CodeDuplicateExtract = "DUPLICATE_EXTRACT"
	CodeInvalidDuration  = "INVALID_DURATION"
	CodeAssertionInvalid = "ASSERTION_INVALID"

	// Catalog-dependent, checked by Validate against a Catalog.
	CodeUnknownOperation     = "UNKNOWN_OPERATION"
	CodeDeprecatedOperation  = "DEPRECATED_OPERATION"
	CodeUnknownInputName     = "UNKNOWN_INPUT_NAME"
	CodeMissingRequiredParam = "MISSING_REQUIRED_PARAM"
	CodeMissingBody          = "MISSING_BODY"
	CodeUnexpectedBody       = "UNEXPECTED_BODY"
	CodeUnknownBodyField     = "UNKNOWN_BODY_FIELD"
	CodeUnknownField         = "UNKNOWN_FIELD"

	// Expression static-reference checks.
	CodeExprSyntax       = "EXPR_SYNTAX"
	CodeUnknownStep      = "UNKNOWN_STEP"
	CodeStepOrder        = "STEP_ORDER"
	CodeUnknownFlowInput = "UNKNOWN_FLOW_INPUT"
	CodeContextRoot      = "CONTEXT_ROOT"
	CodeSecretContext    = "SECRET_CONTEXT"

	// Example resolution (PLAN §34b), checked by Materialize against an
	// ExampleResolver.
	CodeUnknownExample           = "UNKNOWN_EXAMPLE"
	CodeExampleOperationMismatch = "EXAMPLE_OPERATION_MISMATCH"
)

// reservedKeys are the flow-DSL keys the schema rejects today but are
// parked for a future version (PLAN.md §8). `setup`/`teardown` used to be
// here too; they are implemented now (PLAN §8, §9) so a flow may use them
// freely.
var reservedKeys = map[string]bool{
	"when":     true,
	"parallel": true,
	"foreach":  true,
	"retry":    true,
	"use":      true,
	"needs":    true,
	"datasets": true,
}

var quotedRe = regexp.MustCompile(`"([^"]+)"`)

// problemsToDiagnostics translates spec.Problem (schema-validation
// findings, already carrying a best-effort source line) into
// domain.Diagnostic, picking a specific code for the shapes PLAN §23.1
// calls out and falling back to CodeSchema otherwise. Every result is an
// error: a schema violation always makes a flow invalid.
//
// A step with neither `call` nor `example` fails the schema's
// `anyOf: [{required: [call]}, {required: [example]}]` as two separate
// leaf problems at the same instance location -- "missing required
// property "call"" and "missing required property "example"", one per
// failing anyOf branch (see the step schema in spec/flow.schema.json). The
// pre-pass below recognizes that pair and reports it as the one reworded
// diagnostic a human or agent actually wants, instead of two confusing
// halves of an alternative that was never really two separate problems.
func problemsToDiagnostics(problems []spec.Problem) []domain.Diagnostic {
	var out []domain.Diagnostic
	consumed := make([]bool, len(problems))
	for i, p := range problems {
		if consumed[i] || !isSingleMissingRequired(p.Message, "call") {
			continue
		}
		for j := i + 1; j < len(problems); j++ {
			if consumed[j] || problems[j].Path != p.Path || !isSingleMissingRequired(problems[j].Message, "example") {
				continue
			}
			out = append(out, missingCallOrExampleDiag(p.Line, ""))
			consumed[i], consumed[j] = true, true
			break
		}
	}
	for i, p := range problems {
		if consumed[i] {
			continue
		}
		out = append(out, problemDiagnostics(p)...)
	}
	return out
}

// isSingleMissingRequired reports whether msg is exactly the single-name
// form describeKind's *kind.Required case produces for name, e.g.
// `missing required property "call"`.
func isSingleMissingRequired(msg, name string) bool {
	return msg == fmt.Sprintf("missing required property %q", name)
}

// missingCallOrExampleDiag is the diagnostic for a step that sets neither
// `call` nor `example`, shared by the schema-level pre-pass above (stepID
// unknown at that point, so "") and Validate's own direct check on a
// hand-built domain.Flow that bypassed Parse.
func missingCallOrExampleDiag(line int, stepID string) domain.Diagnostic {
	return domain.Diagnostic{
		Code:     CodeSchema,
		Severity: domain.SeverityError,
		Message:  "step must set `call` or `example` (or both, when `call` matches the example's operation)",
		Line:     line,
		StepID:   stepID,
	}
}

func problemDiagnostics(p spec.Problem) []domain.Diagnostic {
	msg := p.Message

	if strings.Contains(msg, "additional propert") {
		names := quotedNames(msg)
		if len(names) == 0 {
			return []domain.Diagnostic{schemaDiag(p)}
		}
		out := make([]domain.Diagnostic, 0, len(names))
		for _, name := range names {
			if reservedKeys[name] {
				out = append(out, domain.Diagnostic{
					Code:     CodeReservedKey,
					Severity: domain.SeverityError,
					Message:  fmt.Sprintf("`%s` is reserved for a future version of the flow DSL", name),
					Line:     p.Line,
				})
			} else {
				out = append(out, domain.Diagnostic{
					Code:     CodeUnknownKey,
					Severity: domain.SeverityError,
					Message:  fmt.Sprintf("unknown key `%s`", name),
					Line:     p.Line,
				})
			}
		}
		return out
	}

	if strings.Contains(msg, "missing required propert") {
		names := quotedNames(msg)
		var out []domain.Diagnostic
		if p.Path == "" {
			for _, name := range names {
				switch name {
				case "version":
					out = append(out, domain.Diagnostic{
						Code: CodeFlowVersion, Severity: domain.SeverityError,
						Message: "flow is missing required `version` (must be 1)", Line: p.Line,
					})
				case "steps":
					out = append(out, domain.Diagnostic{
						Code: CodeNoSteps, Severity: domain.SeverityError,
						Message: "flow must have at least one step", Line: p.Line,
					})
				}
			}
		}
		if len(out) == 0 {
			out = append(out, schemaDiag(p))
		}
		return out
	}

	switch p.Path {
	case "version":
		return []domain.Diagnostic{{
			Code: CodeFlowVersion, Severity: domain.SeverityError,
			Message: "version " + msg, Line: p.Line,
		}}
	case "steps":
		return []domain.Diagnostic{{
			Code: CodeNoSteps, Severity: domain.SeverityError,
			Message: "steps " + msg, Line: p.Line,
		}}
	}

	return []domain.Diagnostic{schemaDiag(p)}
}

func schemaDiag(p spec.Problem) domain.Diagnostic {
	return domain.Diagnostic{Code: CodeSchema, Severity: domain.SeverityError, Message: p.String(), Line: p.Line}
}

func quotedNames(msg string) []string {
	matches := quotedRe.FindAllStringSubmatch(msg, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}
