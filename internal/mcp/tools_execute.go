package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/diagnose"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// ExecuteAPIInput is execute_api's arguments. ID may be omitted when Example
// is given (the operation is then taken from the example); if both are
// given they must name the same operation. When Example is given, its
// Input/Body/Headers are the base request, with Params/Body/Headers here
// overriding it field by field (Body is replaced whole, since a body has no
// universal merge semantics).
type ExecuteAPIInput struct {
	ID      string            `json:"id,omitempty" jsonschema:"operation id (or \"METHOD /path\"); may be omitted when example is given"`
	Example string            `json:"example,omitempty" jsonschema:"saved example id (see list_examples/get_example) to use as the base request"`
	Env     string            `json:"env" jsonschema:"environment name"`
	Params  map[string]any    `json:"params,omitempty" jsonschema:"path/query/header parameter values by name; overrides the example's input, if any"`
	Body    any               `json:"body,omitempty" jsonschema:"request body; overrides the example's body, if any"`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"extra request headers; overrides the example's headers, if any"`
}

// executeAPI runs a single operation as a one-step run (PLAN §23). Its
// permission class is execute_read for GET/HEAD/OPTIONS, execute_mutation
// otherwise; the target environment must be allowed for the client, and
// production requires AllowProduction.
func (s *server) executeAPI(ctx context.Context, req *sdkmcp.CallToolRequest, in ExecuteAPIInput) (*sdkmcp.CallToolResult, any, error) {
	if in.ID == "" && in.Example == "" {
		return errResult(errs.New(errs.Invalid, "execute_api: give id, example, or both")), nil, nil
	}

	var ex *domain.SavedExample
	if in.Example != "" {
		var err error
		ex, err = s.eng.Examples().Get(ctx, in.Example)
		if err != nil {
			return errResult(err), nil, nil
		}
	}

	opRef := in.ID
	if opRef == "" {
		opRef = ex.Operation
	}
	op, err := s.eng.Catalog().ResolveOperation(ctx, opRef)
	if err != nil {
		return errResult(err), nil, nil
	}
	if ex != nil && in.ID != "" && ex.Operation != op.ID {
		return errResult(errs.New(errs.Invalid, "execute_api: example %q is for operation %q, not %q", in.Example, ex.Operation, op.ID)), nil, nil
	}

	class := executeClassFor(methodOf(*op))
	_, perm, denied := s.checkPermission(req.Session, class)
	if denied != nil {
		return denied, nil, nil
	}
	if denied := s.checkEnvironment(ctx, perm, in.Env); denied != nil {
		return denied, nil, nil
	}

	params, body, headers := in.Params, in.Body, in.Headers
	if ex != nil {
		params = mergeExampleParams(ex.Input, in.Params)
		if in.Body == nil {
			body = ex.Body
		}
		headers = mergeExampleHeaders(ex.Headers, in.Headers)
	}

	run, err := s.eng.Runner().Call(ctx, engine.CallRequest{
		Operation:       op.ID,
		Params:          params,
		Body:            body,
		Headers:         headers,
		Env:             in.Env,
		AllowProduction: perm.AllowProduction,
		Trigger:         "mcp",
	})
	if err != nil {
		return errResult(err), nil, nil
	}
	rv := buildRunView(run, "", true, true)
	hints := hintsFor(ctx, s.eng, run)
	out := RunViewWithHints{RunView: rv, Hints: hints}
	text := renderRunText(rv)
	if ex != nil {
		text = fmt.Sprintf("from example %s\n", ex.ID) + text
	}
	return result(appendHintsText(text, hints), out), nil, nil
}

// mergeExampleParams overlays override onto base (an example's saved
// Input), so the call's own params win field by field while everything the
// example set stays for fields the call didn't mention.
func mergeExampleParams(base, override map[string]any) map[string]any {
	if len(base) == 0 {
		return override
	}
	merged := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

// mergeExampleHeaders is mergeExampleParams for string-valued headers.
func mergeExampleHeaders(base, override map[string]string) map[string]string {
	if len(base) == 0 {
		return override
	}
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

// RunViewWithHints wraps a RunView with the diagnose.Hint results a failed
// or errored run produced, so a tool's structured output can carry them
// without changing RunView's own shape (execute_api, run_flow, and get_run
// all use it; run_flow and get_run live in tools_flows.go, owned by another
// agent, but hintsFor and appendHintsText below are exported at the package
// level precisely so that file can call them too). RunView is embedded
// anonymously so its fields (id, status, steps, ...) marshal at exactly the
// top level they always have; Hints is one more field, omitted when a run
// has none.
type RunViewWithHints struct {
	RunView
	Hints []diagnose.Hint `json:"hints,omitempty"`
}

// hintsFor runs diagnose against run and returns whatever hints it found
// (nil for a nil run, or a run nothing failed on). diagnose.Run never fails
// its caller, so this can't either.
func hintsFor(ctx context.Context, eng engine.Engine, run *domain.Run) []diagnose.Hint {
	if run == nil {
		return nil
	}
	return diagnose.Run(ctx, eng, run)
}

// appendHintsText appends the same "Might explain it:" block
// internal/cli/format_run.go's printHints renders for the CLI, to a tool
// result's text content: one line per hint (kind, title, and the token it
// matched on), followed by the concrete CLI command that opens it. It
// returns text unchanged when hints is empty.
func appendHintsText(text string, hints []diagnose.Hint) string {
	if len(hints) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\nMight explain it:\n")
	for _, h := range hints {
		fmt.Fprintf(&b, "  %-8s %s", string(h.Kind), h.Title)
		if h.MatchedOn != "" {
			fmt.Fprintf(&b, "  (matched: %s)", h.MatchedOn)
		}
		b.WriteString("\n")
		if open := hintOpenCommand(h); open != "" {
			fmt.Fprintf(&b, "           %s\n", open)
		}
	}
	return b.String()
}

// hintOpenCommand mirrors internal/cli/format_run.go's function of the same
// purpose: the CLI command that reopens h's source, or "" for a hint kind
// (contract) with nothing further to open.
func hintOpenCommand(h diagnose.Hint) string {
	switch h.Kind {
	case diagnose.KindDoc:
		cmd := fmt.Sprintf("sapien docs show %s %s", h.Ref.Service, h.Ref.Path)
		if h.Ref.Section != "" {
			cmd += fmt.Sprintf(" --section %q", h.Ref.Section)
		}
		return cmd
	case diagnose.KindMemory:
		return fmt.Sprintf("sapien memory show %s", h.Ref.MemoryID)
	default:
		return ""
	}
}
