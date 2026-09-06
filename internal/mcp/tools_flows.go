package mcp

import (
	"context"
	"fmt"
	"github.com/growsimplee/sapien/internal/diagnose"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/flowpatch"
)

// --- list_flows -----------------------------------------------------

// ListFlowsInput is list_flows' arguments.
type ListFlowsInput struct {
	Query string `json:"query,omitempty" jsonschema:"filter by name, tag, or operation substring"`
}

// ListFlowsOutput is list_flows' structured output.
type ListFlowsOutput struct {
	Flows []domain.FlowSummary `json:"flows"`
}

func (s *server) listFlows(ctx context.Context, req *sdkmcp.CallToolRequest, in ListFlowsInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadFlows); denied != nil {
		return denied, nil, nil
	}
	flows, err := s.eng.Flows().List(ctx, in.Query)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := ListFlowsOutput{Flows: flows}
	var b strings.Builder
	for _, f := range flows {
		fmt.Fprintf(&b, "- %s (%d steps): %s [%s]\n", f.ID, f.StepCount, f.Name, strings.Join(f.Tags, ","))
	}
	if len(flows) == 0 {
		b.WriteString("no flows\n")
	}
	return result(b.String(), out), nil, nil
}

// --- get_flow -----------------------------------------------------

// GetFlowInput is get_flow's arguments.
type GetFlowInput struct {
	ID     string `json:"id" jsonschema:"flow id"`
	Detail string `json:"detail,omitempty" jsonschema:"yaml (default): the full YAML source; outline: one line per step (phase, id, operation, assertion count), for skimming a long flow cheaply"`
}

// GetFlowOutput is get_flow's structured output. YAML (Flow.Source) is
// valid input to update_flow's flow_yaml, create_flow's flow_yaml (to save
// a copy elsewhere), and validate_flow: an assertion get_flow renders as
// `{expr: ...}` is accepted back as `expr:` on input (PLAN §37; see
// spec/flow.schema.json's structuredAssertion and internal/domain.Assertion).
type GetFlowOutput struct {
	Flow domain.Flow `json:"flow"`
	YAML string      `json:"yaml"`
}

func (s *server) getFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in GetFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadFlows); denied != nil {
		return denied, nil, nil
	}
	flow, err := s.eng.Flows().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := GetFlowOutput{Flow: *flow, YAML: flow.Source}
	text := flow.Source
	if in.Detail == "outline" {
		text = renderFlowOutline(flow)
		out.YAML = ""
	}
	if text == "" {
		text = fmt.Sprintf("flow %s (%d steps)\n", flow.ID, len(flow.Steps))
	}
	return result(text, out), nil, nil
}

// renderFlowOutline is get_flow(detail=outline): the flow's shape without
// its bodies or prose, one line per step.
func renderFlowOutline(f *domain.Flow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "flow %s: %d setup, %d steps, %d teardown\n", f.ID, len(f.Setup), len(f.Steps), len(f.Teardown))
	phase := func(name string, steps []domain.Step) {
		if len(steps) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s:\n", name)
		for _, st := range steps {
			call := st.Call
			if call == "" && st.Example != "" {
				call = "example " + st.Example
			}
			extras := ""
			if n := len(st.Assert); n > 0 {
				extras += fmt.Sprintf(" [%d assert]", n)
			}
			if len(st.Extract) > 0 {
				keys := make([]string, 0, len(st.Extract))
				for k := range st.Extract {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				extras += " extract:" + strings.Join(keys, ",")
			}
			if st.Until != "" {
				extras += " until"
			}
			fmt.Fprintf(&b, "  - %s: %s%s\n", st.ID, call, extras)
		}
	}
	phase("setup", f.Setup)
	phase("steps", f.Steps)
	phase("teardown", f.Teardown)
	return b.String()
}

// --- validate_flow -----------------------------------------------------

// ValidateFlowInput is validate_flow's arguments.
type ValidateFlowInput struct {
	FlowYAML string `json:"flow_yaml,omitempty" jsonschema:"flow YAML source to validate; or pass path"`
	Path     string `json:"path,omitempty" jsonschema:"a flow file inside the workspace flows directory, already edited on disk, to validate without echoing its text"`
}

func (s *server) validateFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in ValidateFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	src := in.FlowYAML
	switch {
	case src != "" && in.Path != "":
		return errResult(errs.New(errs.Invalid, "validate_flow: pass flow_yaml or path, not both")), nil, nil
	case src == "" && in.Path == "":
		return errResult(errs.New(errs.Invalid, "validate_flow requires flow_yaml or path")), nil, nil
	case in.Path != "":
		read, rerr := readWorkspaceFlowFile(s.workspaceDir(), in.Path)
		if rerr != nil {
			return errResult(rerr), nil, nil
		}
		src = read
	}
	res, err := s.eng.Flows().Validate(ctx, src)
	if err != nil {
		return errResult(err), nil, nil
	}
	return result(renderValidation(res), res), nil, nil
}

// renderValidation renders a ValidationResult so the repair loop (PLAN
// §23.1) can be driven from the text alone if a host lacks structured
// content support.
func renderValidation(r *domain.ValidationResult) string {
	if r.Valid {
		return "valid\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "invalid: %d diagnostic(s)\n", len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "- [%s] %s", d.Code, d.Message)
		if d.Line > 0 {
			fmt.Fprintf(&b, " (line %d)", d.Line)
		}
		if d.StepID != "" {
			fmt.Fprintf(&b, " (step %s)", d.StepID)
		}
		b.WriteString("\n")
		if len(d.Suggestions) > 0 {
			fmt.Fprintf(&b, "    did you mean: %s\n", strings.Join(d.Suggestions, ", "))
		}
	}
	return b.String()
}

// --- shared lean output (create_flow, update_flow, patch_flow) --------
//
// docs/feedback/2026-09-05-41-step-flow-session.md item 4: "create_flow
// echoes the whole flow back, which overflows the token cap on any real
// flow... Return id, path, step count, diagnostics; never the document you
// were just handed." FlowSaveResult is that lean shape, shared by all three
// write tools; get_flow (unchanged) remains the way to read a flow back.

// FlowSaveResult is the lean output of create_flow, update_flow, and
// patch_flow: enough to confirm what was written and where, without ever
// echoing the flow document itself back at the caller.
type FlowSaveResult struct {
	ID            string   `json:"id"`
	Path          string   `json:"path"`
	Steps         int      `json:"steps"`
	SetupSteps    int      `json:"setup_steps"`
	TeardownSteps int      `json:"teardown_steps"`
	Operations    []string `json:"operations"`
	// Diagnostics carries warning-severity findings only: an error would
	// already have rejected the write (Create/Update return errs.FlowInvalid
	// instead), so anything reaching here passed validation.
	Diagnostics []domain.Diagnostic `json:"diagnostics,omitempty"`
	Bytes       int                 `json:"bytes"`
}

// buildFlowSaveResult reduces a saved flow (plus the ValidationResult from
// validating its source, best-effort -- a nil or errored valResult just
// means no warnings are reported) to its lean FlowSaveResult.
func buildFlowSaveResult(f *domain.Flow, wsDir string, valResult *domain.ValidationResult) FlowSaveResult {
	out := FlowSaveResult{
		ID:            f.ID,
		Path:          displayFlowPath(wsDir, f.Path),
		Steps:         len(f.Steps),
		SetupSteps:    len(f.Setup),
		TeardownSteps: len(f.Teardown),
		Operations:    distinctOperations(f),
		Bytes:         len(f.Source),
	}
	if valResult != nil {
		for _, d := range valResult.Diagnostics {
			if d.Severity == domain.SeverityWarning {
				out.Diagnostics = append(out.Diagnostics, d)
			}
		}
	}
	return out
}

// distinctOperations lists every operation f's setup, main, and teardown
// steps call, deduplicated, in first-appearance order across all three
// (setup, then steps, then teardown).
func distinctOperations(f *domain.Flow) []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range [][]domain.Step{f.Setup, f.Steps, f.Teardown} {
		for _, st := range group {
			if st.Call == "" || seen[st.Call] {
				continue
			}
			seen[st.Call] = true
			out = append(out, st.Call)
		}
	}
	return out
}

// displayFlowPath renders a workspace-owned flow's absolute Path relative
// to wsDir, e.g. "flows/qcom-allocation.flow.yaml" -- what create_flow's and
// update_flow's `path` input documents and what their one-line text and
// FlowSaveResult.Path show. p is returned unchanged if it isn't under
// wsDir (a service-owned flow's Path is relative to the service's own
// package directory) or wsDir is unknown.
func displayFlowPath(wsDir, p string) string {
	if wsDir == "" || p == "" || !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(wsDir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}

// readWorkspaceFlowFile validates rel as a safe path inside
// <wsDir>/flows -- not empty, not absolute, no ".." segment -- and reads
// it, for update_flow's and patch_flow's file-path inputs. It does not
// require the domain.FlowFileSuffix create_flow's destination path does:
// this always names a file that already exists (already produced by
// create_flow, or hand-edited by the calling agent), never a new one that
// needs to be indexable at a fresh location.
func readWorkspaceFlowFile(wsDir, rel string) (string, error) {
	flowsDir := filepath.Join(wsDir, domain.FlowsDir)
	if strings.TrimSpace(rel) == "" {
		return "", errs.New(errs.Invalid, "path is required").
			WithHint(fmt.Sprintf("pass a path relative to %s", flowsDir))
	}
	if filepath.IsAbs(rel) {
		return "", errs.New(errs.Invalid, "path %q must be relative to the flows directory %s, not absolute", rel, flowsDir).
			WithHint(fmt.Sprintf("pass a path relative to %s", flowsDir))
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return "", errs.New(errs.Invalid, "path %q must not contain \"..\"", rel).
				WithHint(fmt.Sprintf("use a path inside %s", flowsDir))
		}
	}
	joined := filepath.Join(flowsDir, rel)
	data, err := os.ReadFile(joined)
	if err != nil {
		return "", errs.Wrap(errs.Invalid, err, "reading %s", joined)
	}
	return string(data), nil
}

// --- create_flow -----------------------------------------------------

// CreateFlowInput is create_flow's arguments.
type CreateFlowInput struct {
	FlowYAML string `json:"flow_yaml" jsonschema:"flow YAML source"`
	// Path is relative to the workspace's flows directory (<workspace>/flows),
	// not the workspace root: "sub/dir/x.flow.yaml" saves to
	// <workspace>/flows/sub/dir/x.flow.yaml. Must not be absolute or contain
	// "..", and must end in .flow.yaml so list_flows finds it. Default:
	// "<id>.flow.yaml" from the flow's own `id:`.
	Path string `json:"path,omitempty" jsonschema:"destination path relative to the workspace's flows directory (not the workspace root); default <id>.flow.yaml; must stay inside the flows directory and end in .flow.yaml"`
}

func (s *server) createFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in CreateFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteFlows); denied != nil {
		return denied, nil, nil
	}
	// Best-effort: gathers warning diagnostics to report alongside the lean
	// output. Create validates yamlSrc itself and is authoritative on
	// whether the flow is valid; this call's own result/error is otherwise
	// unused.
	valResult, _ := s.eng.Flows().Validate(ctx, in.FlowYAML)
	flow, err := s.eng.Flows().Create(ctx, in.FlowYAML, in.Path)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := buildFlowSaveResult(flow, s.workspaceDir(), valResult)
	text := fmt.Sprintf("created flow %s at %s, %d steps\n", out.ID, out.Path, out.Steps)
	return result(text, out), nil, nil
}

// --- update_flow -----------------------------------------------------

// UpdateFlowInput is update_flow's arguments: exactly one of FlowYAML or
// Path.
type UpdateFlowInput struct {
	ID string `json:"id" jsonschema:"flow id"`
	// FlowYAML is the replacement source, sent inline. Alternative to Path.
	FlowYAML string `json:"flow_yaml,omitempty" jsonschema:"replacement flow YAML source; alternative to path"`
	// Path names a file inside the workspace's flows directory that this
	// agent already edited on disk (e.g. after get_flow, or a Python/sed
	// edit -- docs/feedback/2026-09-05-41-step-flow-session.md item 3):
	// Sapien re-reads that file's current content instead of requiring it
	// resent inline. Alternative to FlowYAML; must stay inside the flows
	// directory.
	Path string `json:"path,omitempty" jsonschema:"path inside the workspace's flows directory to re-read the new YAML from (already edited on disk); alternative to flow_yaml"`
}

func (s *server) updateFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in UpdateFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteFlows); denied != nil {
		return denied, nil, nil
	}
	yamlSrc, err := resolveUpdateSource(s.workspaceDir(), in)
	if err != nil {
		return errResult(err), nil, nil
	}
	valResult, _ := s.eng.Flows().Validate(ctx, yamlSrc)
	flow, err := s.eng.Flows().Update(ctx, in.ID, yamlSrc)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := buildFlowSaveResult(flow, s.workspaceDir(), valResult)
	text := fmt.Sprintf("updated flow %s at %s, %d steps\n", out.ID, out.Path, out.Steps)
	return result(text, out), nil, nil
}

// resolveUpdateSource returns the YAML update_flow should save: in.Path
// read from disk, or in.FlowYAML verbatim; exactly one of the two must be
// set.
func resolveUpdateSource(wsDir string, in UpdateFlowInput) (string, error) {
	switch {
	case in.FlowYAML != "" && in.Path != "":
		return "", errs.New(errs.Invalid, "update_flow: pass flow_yaml or path, not both")
	case in.Path != "":
		return readWorkspaceFlowFile(wsDir, in.Path)
	case in.FlowYAML != "":
		return in.FlowYAML, nil
	default:
		return "", errs.New(errs.Invalid, "update_flow requires flow_yaml or path")
	}
}

// --- patch_flow -----------------------------------------------------

// PatchFlowInput is patch_flow's arguments: one or more flowpatch.Op,
// applied in order to the saved flow's own YAML document
// (docs/feedback/2026-09-05-41-step-flow-session.md item 3: "Editing one
// assertion in an 880-line flow means re-sending 30KB through
// update_flow... Accept a file path, or a step-level patch").
type PatchFlowInput struct {
	ID  string         `json:"id" jsonschema:"flow id"`
	Ops []flowpatch.Op `json:"ops" jsonschema:"operations to apply in order: set_step{id,step}, merge_step{id,fields}, add_step{step,phase?,after?,before?}, remove_step{id}, set_inputs{inputs}, set_meta{meta:{name?,description?,tags?}}"`
}

func (s *server) patchFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in PatchFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteFlows); denied != nil {
		return denied, nil, nil
	}
	if len(in.Ops) == 0 {
		return errResult(errs.New(errs.Invalid, "patch_flow requires at least one op")), nil, nil
	}
	existing, err := s.eng.Flows().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	patched, perr := flowpatch.Apply(existing.Source, in.Ops)
	if perr != nil {
		// errs.New, not errs.Wrap: errResult's text rendering shows only
		// Code and Message, not a wrapped Cause, and flowpatch's own error
		// (e.g. "unknown step id ...; ids present: ...") is exactly what an
		// agent needs to see to retry.
		return errResult(errs.New(errs.Invalid, "applying patch to flow %q: %v", in.ID, perr)), nil, nil
	}
	valResult, _ := s.eng.Flows().Validate(ctx, patched)
	flow, err := s.eng.Flows().Update(ctx, in.ID, patched)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := buildFlowSaveResult(flow, s.workspaceDir(), valResult)
	text := fmt.Sprintf("patched flow %s at %s, %d steps\n", out.ID, out.Path, out.Steps)
	return result(text, out), nil, nil
}

// --- run_flow -----------------------------------------------------

// RunFlowInput is run_flow's arguments.
type RunFlowInput struct {
	ID     string         `json:"id" jsonschema:"flow id"`
	Env    string         `json:"env" jsonschema:"environment name"`
	Inputs map[string]any `json:"inputs,omitempty" jsonschema:"flow input values by name"`
	// ResumeFrom, FromStep, and UntilStep let a retry skip steps a prior run
	// already proved (docs/feedback/2026-09-05-41-step-flow-session.md item
	// 1: six full 40-step runs to find five failures, one full restart per
	// failure). See engine.RunOptions for the exact semantics.
	ResumeFrom string `json:"resume_from,omitempty" jsonschema:"resume_from: id of an earlier run of this flow whose setup and pre-failure step results are reused instead of executed; from_step / until_step: first and last main step to execute"`
	FromStep   string `json:"from_step,omitempty" jsonschema:"first main step to execute (inclusive); with resume_from, overrides its default resume point (the first step that did not pass in that run)"`
	UntilStep  string `json:"until_step,omitempty" jsonschema:"last main step to execute (inclusive); later main steps are skipped; teardown still runs"`
	// Detail bounds the result size (58-step field report: every full result
	// blew the token ceiling). summary is the default.
	Detail string `json:"detail,omitempty" jsonschema:"summary (default): one line per step, failed steps with their assertion details, no bodies; failed: bodies (capped) only for failed or errored steps; full: every body, capped, large"`
}

// runFlow's permission class is execute_read or execute_mutation, whichever
// the highest-risk step operation requires (PLAN §23).
func (s *server) runFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in RunFlowInput) (*sdkmcp.CallToolResult, any, error) {
	flow, err := s.eng.Flows().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	class := classExecuteRead
	for _, st := range flow.Steps {
		op, err := s.eng.Catalog().ResolveOperation(ctx, st.Call)
		if err != nil {
			return errResult(err), nil, nil
		}
		if executeClassFor(methodOf(*op)) == classExecuteMutation {
			class = classExecuteMutation
			break
		}
	}
	_, perm, denied := s.checkPermission(req.Session, class)
	if denied != nil {
		return denied, nil, nil
	}
	if denied := s.checkEnvironment(ctx, perm, in.Env); denied != nil {
		return denied, nil, nil
	}

	run, err := s.eng.Runner().RunFlow(ctx, in.ID, engine.RunOptions{
		Environment:     in.Env,
		Inputs:          in.Inputs,
		AllowProduction: perm.AllowProduction,
		Trigger:         "mcp",
		ResumeFrom:      in.ResumeFrom,
		FromStep:        in.FromStep,
		UntilStep:       in.UntilStep,
	})
	if err != nil {
		return errResult(err), nil, nil
	}
	rv := runViewForDetail(run, in.Detail)
	hints := hintsFor(ctx, s.eng, run)
	text := appendSoftLines(renderRunText(rv), run, diagnose.SoftChanges(ctx, s.eng, run))
	text = appendHintsText(text, hints)
	if in.Detail != "full" {
		text += fmt.Sprintf("bodies omitted (detail=%s); get_run(id=%q, include_bodies=true, step=<id>) shows one step in full\n", detailOrDefault(in.Detail), run.ID)
	}
	return result(text, RunViewWithHints{RunView: rv, Hints: hints}), nil, nil
}

// runViewForDetail applies run_flow's detail mode: summary strips every
// body, failed keeps bodies only on failed or errored steps, full keeps
// them all (capped).
func runViewForDetail(run *domain.Run, detail string) RunView {
	rv := buildRunView(run, "", true, true)
	mode := detailOrDefault(detail)
	if mode == "full" {
		return rv
	}
	for i := range rv.Steps {
		st := &rv.Steps[i]
		failed := st.Status == string(domain.StepFailed) || st.Status == string(domain.StepErrored)
		if mode == "failed" && failed {
			continue
		}
		// Keep method, URL, and status so the one-line summary still says what
		// was called and what came back; drop the payloads and headers.
		if st.Request != nil {
			st.Request.Headers = nil
			st.Request.Body = nil
		}
		if st.Response != nil {
			st.Response.Headers = nil
			st.Response.Body = nil
			st.Response.Truncated = false
		}
	}
	return rv
}

func detailOrDefault(d string) string {
	switch d {
	case "full", "failed":
		return d
	}
	return "summary"
}

// appendSoftLines adds the soft-assertion report (current mismatches and
// flips since the previous run of the flow) to a run's text.
func appendSoftLines(text string, run *domain.Run, changes []diagnose.SoftChange) string {
	for _, l := range diagnose.SoftLines(run, changes) {
		text += l + "\n"
	}
	return text
}

// --- get_run -----------------------------------------------------

// GetRunInput is get_run's arguments.
type GetRunInput struct {
	ID            string `json:"id" jsonschema:"run id"`
	Step          string `json:"step,omitempty" jsonschema:"restrict to this step id"`
	IncludeBodies bool   `json:"include_bodies,omitempty" jsonschema:"include full (uncapped) request/response bodies; default false"`
}

func (s *server) getRun(ctx context.Context, req *sdkmcp.CallToolRequest, in GetRunInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadRuns); denied != nil {
		return denied, nil, nil
	}
	run, err := s.eng.Runs().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	rv := buildRunView(run, in.Step, in.IncludeBodies, false)
	hints := hintsFor(ctx, s.eng, run)
	return result(appendHintsText(renderRunText(rv), hints), RunViewWithHints{RunView: rv, Hints: hints}), nil, nil
}
