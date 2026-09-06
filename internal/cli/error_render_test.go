package cli_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/growsimplee/sapien/internal/cli"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// A flow-invalid error must say what is wrong in human mode, not only how
// many problems there are; the diagnostics detail is []domain.Diagnostic
// in-process and []any after a daemon round trip.
func TestWriteError_PrintsDiagnostics(t *testing.T) {
	diags := []domain.Diagnostic{{
		Code: "MISSING_REQUIRED_PARAM", Severity: domain.SeverityError, Line: 4, StepID: "call",
		Message: "missing required param `orgId` (header, string)",
	}, {
		Code: "UNKNOWN_EXAMPLE", Severity: domain.SeverityError, Line: 3,
		Message: "unknown example `book-on-demnd`", Suggestions: []string{"book-on-demand-hyperlocal"},
	}}
	err := errs.New(errs.FlowInvalid, "flow validation failed: 2 problem(s)").WithDetail("diagnostics", diags)

	var stderr bytes.Buffer
	cli.WriteError(&cli.App{Stderr: &stderr}, err)
	out := stderr.String()
	assert.Contains(t, out, "error: E_FLOW_INVALID: flow validation failed: 2 problem(s)")
	assert.Contains(t, out, "  error line 4 step call: MISSING_REQUIRED_PARAM: missing required param `orgId` (header, string)")
	assert.Contains(t, out, "  error line 3: UNKNOWN_EXAMPLE: unknown example `book-on-demnd` (did you mean: book-on-demand-hyperlocal)")

	// The generic shape a Remote engine hands back.
	generic := errs.New(errs.FlowInvalid, "flow validation failed: 1 problem(s)").
		WithDetail("diagnostics", []any{map[string]any{"code": "MISSING_BODY", "severity": "error", "message": "operation requires a request body"}})
	stderr.Reset()
	cli.WriteError(&cli.App{Stderr: &stderr}, generic)
	assert.Contains(t, stderr.String(), "  error: MISSING_BODY: operation requires a request body")
}
