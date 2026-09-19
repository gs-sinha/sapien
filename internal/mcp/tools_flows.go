package mcp

import (
	"context"
	"fmt"
	"github.com/gs-sinha/sapien/internal/diagnose"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/flowpatch"
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
	flows, err := s.engine().Flows().List(ctx, in.Query)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := ListFlowsOutput{Flows: flows}
	var b strings.Builder
	for _, f := range flows {
		tier := flowTierLabel(f.OwnerKind, f.OwnerID)
		if shipText := shipStateText(f.Shipped); shipText != "" {
			tier += ", " + shipText
		}
		fmt.Fprintf(&b, "- %s (%d steps, %s): %s [%s]\n", f.ID, f.StepCount, tier, f.Name, strings.Join(f.Tags, ","))
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
	flow, err := s.engine().Flows().Get(ctx, in.ID)
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
			if st.When != "" {
				extras += " when:" + st.When
			}
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
	Path     string `json:"path,omitempty" jsonschema:"a flow file already on disk to validate without echoing its text: relative to <workspace>/flows, or to the workspace root when inside flows/ or local/flows/"`
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
	res, err := s.engine().Flows().Validate(ctx, src)
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
	ID   string `json:"id"`
	Path string `json:"path"`
	// Tier is the owner kind the file landed in: local, workspace, or
	// service; Service names the owning service for the service tier.
	Tier          string   `json:"tier"`
	Service       string   `json:"service,omitempty"`
	Steps         int      `json:"steps"`
	SetupSteps    int      `json:"setup_steps"`
	TeardownSteps int      `json:"teardown_steps"`
	Operations    []string `json:"operations"`
	// Diagnostics carries warning-severity findings only: an error would
	// already have rejected the write (Create/Update return errs.FlowInvalid
	// instead), so anything reaching here passed validation.
	Diagnostics []domain.Diagnostic `json:"diagnostics,omitempty"`
	Bytes       int                 `json:"bytes"`
	// Shipped is the workspace tier's ship state (FlowSummary.Shipped, PLAN
	// §7b): empty for local and service, and for a workspace not in git.
	// Filled in by the caller (shippedStateFor), not by
	// buildFlowSaveResult, since it needs a List call the *domain.Flow
	// buildFlowSaveResult is given does not carry.
	Shipped string `json:"shipped,omitempty"`
}

// buildFlowSaveResult reduces a saved flow (plus the ValidationResult from
// validating its source, best-effort -- a nil or errored valResult just
// means no warnings are reported) to its lean FlowSaveResult.
func buildFlowSaveResult(f *domain.Flow, wsDir string, valResult *domain.ValidationResult) FlowSaveResult {
	out := FlowSaveResult{
		ID:            f.ID,
		Path:          displayFlowPath(wsDir, f.Path),
		Tier:          f.OwnerKind,
		Steps:         len(f.Steps),
		SetupSteps:    len(f.Setup),
		TeardownSteps: len(f.Teardown),
		Operations:    distinctOperations(f),
		Bytes:         len(f.Source),
	}
	if f.OwnerKind == domain.FlowOwnerService {
		out.Service = f.OwnerID
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

// readWorkspaceFlowFile validates rel as a safe path -- not empty, not
// absolute, no ".." segment -- and reads it for update_flow's, patch_flow's
// and validate_flow's file-path inputs. rel is tried relative to
// <wsDir>/flows first, as the tools have always documented; when nothing
// is there and rel itself starts with flows/ or local/flows/, it is read
// relative to the workspace root instead. That second form exists because
// create_flow now reports local-tier paths as local/flows/x.flow.yaml, and
// an agent pasting that back should not have to know which prefix to
// strip. Nothing outside those two directories is readable this way. It
// does not require the domain.FlowFileSuffix create_flow's destination
// path does: this always names a file that already exists (already
// produced by create_flow, or hand-edited by the calling agent), never a
// new one that needs to be indexable at a fresh location.
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
	if err == nil {
		return string(data), nil
	}
	if os.IsNotExist(err) && isFlowsDirPath(rel) {
		alt := filepath.Join(wsDir, rel)
		if altData, altErr := os.ReadFile(alt); altErr == nil {
			return string(altData), nil
		}
	}
	return "", errs.Wrap(errs.Invalid, err, "reading %s", joined)
}

// isFlowsDirPath reports whether rel, taken relative to the workspace
// root, lies inside one of the two workspace flow tiers (flows/ or
// local/flows/) -- the only root-relative forms readWorkspaceFlowFile
// accepts.
func isFlowsDirPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, dir := range []string{domain.FlowsDir, domain.LocalDir + "/" + domain.FlowsDir} {
		if strings.HasPrefix(rel, dir+"/") && len(rel) > len(dir)+1 {
			return true
		}
	}
	return false
}

// --- create_flow -----------------------------------------------------

// CreateFlowInput is create_flow's arguments.
type CreateFlowInput struct {
	FlowYAML string `json:"flow_yaml" jsonschema:"flow YAML source"`
	// Path is relative to the chosen tier's flows directory, not the
	// workspace root: "sub/dir/x.flow.yaml" saves to
	// <workspace>/local/flows/sub/dir/x.flow.yaml for the default local
	// scope. Must not be absolute or contain "..", and must end in
	// .flow.yaml so list_flows finds it. Default: "<id>.flow.yaml" from the
	// flow's own `id:`.
	Path string `json:"path,omitempty" jsonschema:"destination path relative to the chosen tier's flows directory (not the workspace root); default <id>.flow.yaml; must stay inside that directory and end in .flow.yaml"`
	// Scope is the tier: local (default) is this machine only, workspace is
	// the team's repo, service is the owning service's api/flows and needs
	// Service plus a bound local checkout.
	Scope   string `json:"scope,omitempty" jsonschema:"tier to save into: local (default; this machine only, <workspace>/local/flows), workspace (the team's repo, <workspace>/flows), or service (<service>/api/flows; needs service and a bound local checkout)"`
	Service string `json:"service,omitempty" jsonschema:"owning service name; required for scope service"`
}

func (s *server) createFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in CreateFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteFlows); denied != nil {
		return denied, nil, nil
	}
	kind, service, err := flowScopeArgs("create_flow", in.Scope, in.Service)
	if err != nil {
		return errResult(err), nil, nil
	}
	// Best-effort: gathers warning diagnostics to report alongside the lean
	// output. CreateIn validates yamlSrc itself and is authoritative on
	// whether the flow is valid; this call's own result/error is otherwise
	// unused.
	valResult, _ := s.engine().Flows().Validate(ctx, in.FlowYAML)
	flow, err := s.engine().Flows().CreateIn(ctx, in.FlowYAML, engine.CreateFlowOptions{
		Path: in.Path, OwnerKind: kind, OwnerID: service,
	})
	if err != nil {
		return errResult(err), nil, nil
	}
	out := buildFlowSaveResult(flow, s.workspaceDir(), valResult)
	out.Shipped = shippedStateFor(ctx, s.engine().Flows(), flow.ID)
	text := fmt.Sprintf("created flow %s at %s (%s) in workspace %s, %d steps\n", out.ID, out.Path, flowTierNote(out.Tier, out.Service), s.workspaceName(), out.Steps)
	return result(text, out), nil, nil
}

// flowScopeArgs turns a tool's scope/service inputs into the owner kind
// and id the engine takes: an empty scope is the local tier, service scope
// must name its service, and anything else is refused with the ladder in
// the hint so the agent can retry without another lookup.
func flowScopeArgs(tool, scope, service string) (kind, owner string, err error) {
	scope = strings.TrimSpace(scope)
	service = strings.TrimSpace(service)
	switch scope {
	case "":
		kind = domain.FlowOwnerLocal
	case domain.FlowOwnerLocal, domain.FlowOwnerWorkspace, domain.FlowOwnerService:
		kind = scope
	default:
		return "", "", errs.New(errs.Invalid, "%s: unknown scope %q", tool, scope).
			WithHint("scope is local (this machine), workspace (the team's repo), or service (the owning service; pass service)")
	}
	if kind == domain.FlowOwnerService {
		if service == "" {
			return "", "", errs.New(errs.Invalid, "%s: service scope requires service", tool).
				WithHint("pass service=<name> (list_services shows names); the service must be bound to a local checkout here")
		}
		return kind, service, nil
	}
	// A service name with another scope is almost always a slip; ignoring
	// it would file the flow somewhere the caller did not mean.
	if service != "" {
		return "", "", errs.New(errs.Invalid, "%s: service applies to scope service only, not %s", tool, kind).
			WithHint("drop service, or pass scope=service")
	}
	return kind, "", nil
}

// flowTierLabel is the short form list_flows shows per flow: the tier,
// with the service named when the tier is service.
func flowTierLabel(kind, ownerID string) string {
	if kind == domain.FlowOwnerService && ownerID != "" {
		return kind + ":" + ownerID
	}
	if kind == "" {
		return domain.FlowOwnerWorkspace
	}
	return kind
}

// shipStateText renders a workspace-tier flow's domain.Ship* state
// (FlowSummary.Shipped, PLAN §7b) for list_flows' one-line-per-flow text:
// "not committed" (in flows/ but never added to git), "modified" (tracked,
// with uncommitted changes), "committed, not pushed", or "shipped". Empty
// -- the local and service tiers, and a workspace not in git -- renders
// nothing, so the tier label stands alone as it always has.
func shipStateText(state string) string {
	switch state {
	case domain.ShipUntracked:
		return "not committed"
	case domain.ShipModified:
		return "modified"
	case domain.ShipUnpushed:
		return "committed, not pushed"
	case domain.ShipShipped:
		return "shipped"
	default:
		return ""
	}
}

// shippedStateFor looks up flow id's current ship state
// (FlowSummary.Shipped) for FlowSaveResult, through one List call filtered
// to id. Best effort: any error, or no exact match, leaves it empty rather
// than failing a save that has already succeeded.
func shippedStateFor(ctx context.Context, flows engine.FlowAPI, id string) string {
	list, err := flows.List(ctx, id)
	if err != nil {
		return ""
	}
	for _, f := range list {
		if f.ID == id {
			return f.Shipped
		}
	}
	return ""
}

// commitShortSHA best-effort reads wsDir's current HEAD short sha, for
// rescope_flow's confirmation text after it commits a promotion. Never
// fails the call: git not being installed, wsDir not being a repository,
// or a raced concurrent commit all just mean the text omits the sha.
func commitShortSHA(ctx context.Context, wsDir string) string {
	if wsDir == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "git", "-C", wsDir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// flowTierNote is the parenthetical create_flow and rescope_flow print
// after the path: where the flow now lives in terms of who can see it, and
// for the local tier the one thing to do next.
func flowTierNote(kind, service string) string {
	switch kind {
	case domain.FlowOwnerLocal:
		return "local tier; promote with rescope_flow when it works"
	case domain.FlowOwnerService:
		return fmt.Sprintf("service tier: %s/api/flows, rides your branch", service)
	default:
		return "workspace tier: the team's repo"
	}
}

// --- rescope_flow -----------------------------------------------------

// RescopeFlowInput is rescope_flow's arguments.
type RescopeFlowInput struct {
	ID      string `json:"id" jsonschema:"flow id"`
	Scope   string `json:"scope" jsonschema:"tier to move the flow to: local (this machine only), workspace (the team's repo), or service (the owning service's api/flows; needs service and a bound local checkout)"`
	Service string `json:"service,omitempty" jsonschema:"owning service name; required for scope service"`
	// Commit and Message mirror engine.RescopeOptions (PLAN §7b): opt-in,
	// only meaningful for scope workspace, and never a push.
	Commit  bool   `json:"commit,omitempty" jsonschema:"also commit the moved file in the workspace repository; only for scope workspace; never pushes; do this only when the user asked for it"`
	Message string `json:"message,omitempty" jsonschema:"commit message; only used with commit; default: \"Promote flow <id> to the team workspace\""`
}

// RescopeFlowOutput is rescope_flow's structured output: the same lean
// summary create_flow returns, plus where the file was and where it is
// now.
type RescopeFlowOutput struct {
	FlowSaveResult
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

func (s *server) rescopeFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in RescopeFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteFlows); denied != nil {
		return denied, nil, nil
	}
	if strings.TrimSpace(in.Scope) == "" {
		return errResult(errs.New(errs.Invalid, "rescope_flow requires scope").
			WithHint("pass scope local, workspace, or service (with service=<name>)")), nil, nil
	}
	kind, service, err := flowScopeArgs("rescope_flow", in.Scope, in.Service)
	if err != nil {
		return errResult(err), nil, nil
	}
	existing, err := s.engine().Flows().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	moved, err := s.engine().Flows().RescopeWith(ctx, in.ID, kind, service, engine.RescopeOptions{Commit: in.Commit, Message: in.Message})
	if err != nil {
		return errResult(err), nil, nil
	}
	wsDir := s.workspaceDir()
	out := RescopeFlowOutput{
		FlowSaveResult: buildFlowSaveResult(moved, wsDir, nil),
		OldPath:        displayFlowPath(wsDir, existing.Path),
		NewPath:        displayFlowPath(wsDir, moved.Path),
	}
	out.Shipped = shippedStateFor(ctx, s.engine().Flows(), moved.ID)

	text := fmt.Sprintf("rescoped flow %s to %s (%s -> %s)", out.ID, flowTierNote(out.Tier, out.Service), out.OldPath, out.NewPath)
	// The commit note only makes sense for a promotion to the workspace
	// tier -- Commit is refused by the engine for any other target -- so it
	// is appended only there; a move to local or service keeps the text as
	// it always was.
	if out.Tier == domain.FlowOwnerWorkspace {
		switch {
		case !in.Commit:
			text += "; not committed"
		default:
			if sha := commitShortSHA(ctx, wsDir); sha != "" {
				text += "; committed " + sha
			} else {
				text += "; committed"
			}
		}
	}
	text += "\n"
	return result(text, out), nil, nil
}

// --- commit_flow -----------------------------------------------------
//
// commit_flow is the standalone form of rescope_flow's Commit option (PLAN
// §7b): before this, committing a flow already at the team tier meant
// rescoping it back to local and promoting again with the commit checkbox.

// CommitFlowInput is commit_flow's arguments.
type CommitFlowInput struct {
	ID string `json:"id" jsonschema:"flow id; must already be at the workspace tier"`
	// Message overrides the engine's own default: "Add flow <id> to the
	// team workspace" for a file never added to git, "Update flow <id>"
	// for one with an uncommitted edit.
	Message string `json:"message,omitempty" jsonschema:"commit message; default depends on whether the file was ever added to git"`
}

// commitFlow commits a workspace-tier flow's file in the workspace
// repository: one commit of that file, never a push. Refused by the
// engine for the local and service tiers, a workspace not in git, and a
// file with nothing to commit (already unpushed or shipped).
func (s *server) commitFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in CommitFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteFlows); denied != nil {
		return denied, nil, nil
	}
	sum, err := s.engine().Flows().Commit(ctx, in.ID, in.Message)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := buildFlowSaveResultFromSummary(sum, s.workspaceDir())

	// Commit always leaves the file unpushed (Sapien never pushes); the
	// other branches are defensive, not expected in practice.
	note := "not pushed"
	if out.Shipped != domain.ShipUnpushed {
		if txt := shipStateText(out.Shipped); txt != "" {
			note = txt
		} else {
			note = "committed"
		}
	}
	text := fmt.Sprintf("committed %s; %s\n", out.Path, note)
	return result(text, out), nil, nil
}

// buildFlowSaveResultFromSummary reduces a FlowSummary -- what Commit
// returns, since committing never touches the document itself -- to the
// same lean FlowSaveResult shape buildFlowSaveResult produces from a full
// *domain.Flow. SetupSteps, TeardownSteps, Diagnostics and Bytes stay
// zero: a FlowSummary carries none of them.
func buildFlowSaveResultFromSummary(sum *domain.FlowSummary, wsDir string) FlowSaveResult {
	out := FlowSaveResult{
		ID:         sum.ID,
		Path:       displayFlowPath(wsDir, sum.Path),
		Tier:       sum.OwnerKind,
		Steps:      sum.StepCount,
		Operations: sum.Operations,
		Shipped:    sum.Shipped,
	}
	if sum.OwnerKind == domain.FlowOwnerService {
		out.Service = sum.OwnerID
	}
	return out
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
	Path string `json:"path,omitempty" jsonschema:"file to re-read the new YAML from (already edited on disk): relative to <workspace>/flows, or to the workspace root when inside flows/ or local/flows/ (as create_flow reports it); alternative to flow_yaml"`
}

func (s *server) updateFlow(ctx context.Context, req *sdkmcp.CallToolRequest, in UpdateFlowInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteFlows); denied != nil {
		return denied, nil, nil
	}
	yamlSrc, err := resolveUpdateSource(s.workspaceDir(), in)
	if err != nil {
		return errResult(err), nil, nil
	}
	valResult, _ := s.engine().Flows().Validate(ctx, yamlSrc)
	flow, err := s.engine().Flows().Update(ctx, in.ID, yamlSrc)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := buildFlowSaveResult(flow, s.workspaceDir(), valResult)
	out.Shipped = shippedStateFor(ctx, s.engine().Flows(), flow.ID)
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
	existing, err := s.engine().Flows().Get(ctx, in.ID)
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
	valResult, _ := s.engine().Flows().Validate(ctx, patched)
	flow, err := s.engine().Flows().Update(ctx, in.ID, patched)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := buildFlowSaveResult(flow, s.workspaceDir(), valResult)
	out.Shipped = shippedStateFor(ctx, s.engine().Flows(), flow.ID)
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
	flow, err := s.engine().Flows().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	class := classExecuteRead
	for _, st := range flow.Steps {
		op, err := s.engine().Catalog().ResolveOperation(ctx, st.Call)
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

	run, err := s.engine().Runner().RunFlow(ctx, in.ID, engine.RunOptions{
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
	run, err := s.engine().Runs().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	rv := buildRunView(run, in.Step, in.IncludeBodies, false)
	hints := hintsFor(ctx, s.eng, run)
	return result(appendHintsText(renderRunText(rv), hints), RunViewWithHints{RunView: rv, Hints: hints}), nil, nil
}
