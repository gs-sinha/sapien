package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/gitsrc"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// --- add_service ------------------------------------------------------

// AddServiceInput is add_service's arguments: exactly one of Path or URL,
// mirroring `sapien service add <path|git-url>`.
//
// A local Path in a shared (team) workspace tries AddFromCheckout first
// (committing the checkout's origin as the team's git source and binding
// the checkout here) rather than a plain local Add, unless Local is set;
// see addService's doc comment for the full decision.
type AddServiceInput struct {
	Path     string `json:"path,omitempty" jsonschema:"absolute path of the service repo (or its api/ package) on this machine; use path or url, not both"`
	URL      string `json:"url,omitempty" jsonschema:"git remote (git@host:org/repo.git, ssh://, https://host/org/repo.git, file://); use path or url, not both"`
	Name     string `json:"name,omitempty" jsonschema:"service name; default: the name in service.yaml, else the contract's info.title"`
	Ref      string `json:"ref,omitempty" jsonschema:"git ref: branch, tag, or commit; for a git url, what it clones, for a local checkout in a shared workspace, the branch the new team source pins"`
	Subdir   string `json:"subdir,omitempty" jsonschema:"package directory inside the repo; default api (git url sources only)"`
	Contract string `json:"contract,omitempty" jsonschema:"explicit contract file relative to the package root, overriding discovery"`
	Local    bool   `json:"local,omitempty" jsonschema:"register path as a local-only source even in a shared workspace, skipping the team git source entirely"`
	Team     bool   `json:"team,omitempty" jsonschema:"commit path's repository as the team's git source even though it is a subdirectory of a monorepo, and surface any other error instead of silently falling back to a local-only source"`
	Force    bool   `json:"force,omitempty" jsonschema:"register the checkout even though it has no api/ package yet"`
}

// AddServiceOutput is add_service's structured output.
type AddServiceOutput struct {
	Service domain.Service `json:"service"`
}

// isSharedWorkspace reports whether ws is a shared (team) workspace: one
// where a git source committed to sapien.workspace.yaml is read by every
// machine that clones it, as opposed to a workspace with no team file to
// commit into. addService uses it to decide whether a local Path commits a
// team git source (AddFromCheckout) or registers a plain local source
// (Add) by default.
//
// This is a package-level var wrapping workspace.IsShared, not a direct
// call, so tests can swap it to exercise both branches of addService
// without needing a real git-tracked workspace file on disk.
var isSharedWorkspace = workspace.IsShared

// resolvePathOrURL applies add_service's path-vs-url rule (exactly one of
// them) and the same "a git URL passed as path is an easy mistake" rescue
// gitsrc.IsGitURL affords the CLI, returning the normalized (path, url)
// pair every caller -- addServiceSource and addService's checkout branch
// alike -- builds a domain.Source or an AddFromCheckout call from.
func resolvePathOrURL(in AddServiceInput) (path, url string, err error) {
	path = strings.TrimSpace(in.Path)
	url = strings.TrimSpace(in.URL)

	switch {
	case path == "" && url == "":
		return "", "", errs.New(errs.Invalid, "add_service needs path or url").
			WithHint("pass path (the absolute local directory of the service repo) or url (a git remote)")
	case path != "" && url != "":
		return "", "", errs.New(errs.Invalid, "add_service takes path or url, not both")
	}

	// A git URL passed as path is an easy mistake for an agent; accept it.
	if url == "" && gitsrc.IsGitURL(path) {
		url, path = path, ""
	}
	return path, url, nil
}

// addService registers a service from a local path or a git URL. A local
// path in a shared workspace (isSharedWorkspace) is not registered as a
// plain local source by default: it first tries AddFromCheckout, which
// commits the checkout's repository to sapien.workspace.yaml as the team's
// git source and binds the checkout here in one step, so every other
// clone gets a source to read while this machine keeps reading (and
// writing) the working copy. Local skips that and always registers path
// as a local-only source; Team asks AddFromCheckout to accept a checkout
// that is a subdirectory of a monorepo, and turns its refusal (a checkout
// with no origin, or an unaccepted subdirectory -- both reported as
// errs.Invalid with Details["local_add"] == true) into a surfaced error
// instead of the local-only fallback that detail otherwise signals is the
// sensible thing to do.
func (s *server) addService(ctx context.Context, req *sdkmcp.CallToolRequest, in AddServiceInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteServices); denied != nil {
		return denied, nil, nil
	}

	path, url, err := resolvePathOrURL(in)
	if err != nil {
		return errResult(err), nil, nil
	}

	if path != "" && !in.Local {
		if !filepath.IsAbs(path) && path != "~" && !strings.HasPrefix(path, "~/") {
			return errResult(pathNotAbsoluteErr(path)), nil, nil
		}
		if in.Subdir != "" {
			return errResult(errs.New(errs.Invalid, "subdir only applies to a git url; a local checkout's package directory is discovered, not named")), nil, nil
		}
		if isSharedWorkspace(s.engine().Workspace()) {
			return s.addServiceFromCheckoutOrFallback(ctx, in, path)
		}
	}

	src, err := addServiceSourceFrom(in, path, url)
	if err != nil {
		return errResult(err), nil, nil
	}
	svc, err := s.engine().Services().Add(ctx, strings.TrimSpace(in.Name), src)
	if err != nil {
		if errs.CodeOf(err) == errs.Conflict {
			return s.addServiceConflictResync(ctx, conflictServiceName(err, in.Name))
		}
		return errResult(err), nil, nil
	}
	return result(renderServiceAdded(s.engine().Workspace(), svc), AddServiceOutput{Service: *svc}), nil, nil
}

// addServiceFromCheckoutOrFallback is addService's shared-workspace,
// local-path branch: try AddFromCheckout, and on the specific refusal it
// marks with Details["local_add"] == true, fall back to a plain local Add
// unless Team insists on a team source.
func (s *server) addServiceFromCheckoutOrFallback(ctx context.Context, in AddServiceInput, path string) (*sdkmcp.CallToolResult, any, error) {
	name := strings.TrimSpace(in.Name)
	svc, err := s.engine().Services().AddFromCheckout(ctx, name, path, engine.AddFromCheckoutOptions{
		Ref: strings.TrimSpace(in.Ref), Force: in.Force, AllowSubdir: in.Team,
	})
	if err == nil {
		return result(renderServiceFromCheckoutAdded(s.engine().Workspace(), svc), AddServiceOutput{Service: *svc}), nil, nil
	}
	if errs.CodeOf(err) == errs.Conflict {
		return s.addServiceConflictResync(ctx, conflictServiceName(err, in.Name))
	}

	e := errs.As(err)
	localAdd, _ := e.Details["local_add"].(bool)
	if !localAdd || in.Team {
		return errResult(err), nil, nil
	}

	svc2, err2 := s.engine().Services().Add(ctx, name, domain.Source{
		Kind: domain.SourceLocal, Path: path, Contract: strings.TrimSpace(in.Contract),
	})
	if err2 != nil {
		if errs.CodeOf(err2) == errs.Conflict {
			return s.addServiceConflictResync(ctx, conflictServiceName(err2, in.Name))
		}
		return errResult(err2), nil, nil
	}
	return result(renderServiceAddedLocalFallback(s.engine().Workspace(), svc2, e.Message), AddServiceOutput{Service: *svc2}), nil, nil
}

// pathNotAbsoluteErr is add_service's and bind_service's shared complaint
// about a relative path: the MCP server's working directory is the
// daemon's, never the calling agent's, so a relative path could only
// resolve against the wrong place.
func pathNotAbsoluteErr(path string) *errs.Error {
	return errs.New(errs.Invalid, "path %q is not absolute", path).
		WithDetail("path", path).
		WithHint("the MCP server does not share your working directory; pass the absolute path")
}

// conflictServiceName recovers the name an E_CONFLICT error from
// Services().Add was actually reported for, preferring the error's own
// "name" detail (set by workspace.AddService, which knows the derived name
// even when the caller passed none) over the argument the tool was called
// with.
func conflictServiceName(err error, fallback string) string {
	if e := errs.As(err); e != nil {
		if v, ok := e.Details["name"].(string); ok && v != "" {
			return v
		}
	}
	return strings.TrimSpace(fallback)
}

// addServiceConflictResync handles add_service's E_CONFLICT path: rather
// than fail with an error the caller can only relay to the user (PLAN §23's
// "add_service conflicts on an already-registered service, and the resync
// path is CLI-only with no MCP equivalent" gap), it re-syncs the already
// registered service and returns that as a normal, non-error result -- the
// same thing `sapien service sync <name>` would do.
func (s *server) addServiceConflictResync(ctx context.Context, name string) (*sdkmcp.CallToolResult, any, error) {
	if name == "" {
		return errResult(errs.New(errs.Conflict, "service already exists")), nil, nil
	}
	svcs, err := s.engine().Services().Sync(ctx, name)
	if err != nil {
		return errResult(err), nil, nil
	}
	if len(svcs) == 0 {
		return errResult(errs.New(errs.ServiceNotFound, "service %q not found", name)), nil, nil
	}
	svc := svcs[0]
	text := fmt.Sprintf("already registered as %s; re-synced\n\n", svc.Name) + renderServiceSummary(s.engine().Workspace(), &svc)
	return result(text, AddServiceOutput{Service: svc}), nil, nil
}

// addServiceSourceFrom turns the tool's arguments into a domain.Source,
// given the (path, url) pair resolvePathOrURL already normalized, insisting
// on an absolute local path: the MCP server's working directory is the
// daemon's, never the calling agent's, so a relative path could only
// resolve against the wrong place. Reached only for a git url, a local path
// with Local set, or a local path in a workspace that is not shared -- the
// checkout flow (addServiceFromCheckoutOrFallback) handles every other
// local path itself and never calls this.
func addServiceSourceFrom(in AddServiceInput, path, url string) (domain.Source, error) {
	if url != "" {
		return domain.Source{
			Kind:     domain.SourceGit,
			URL:      url,
			Ref:      strings.TrimSpace(in.Ref),
			Subdir:   strings.TrimSpace(in.Subdir),
			Contract: strings.TrimSpace(in.Contract),
		}, nil
	}

	if !filepath.IsAbs(path) && path != "~" && !strings.HasPrefix(path, "~/") {
		return domain.Source{}, pathNotAbsoluteErr(path)
	}
	if in.Ref != "" || in.Subdir != "" {
		return domain.Source{}, errs.New(errs.Invalid, "ref and subdir apply to git sources only").
			WithHint("for a local path, point path at the repo (or directly at its api/ package) and use contract to override discovery")
	}
	return domain.Source{
		Kind:     domain.SourceLocal,
		Path:     path,
		Contract: strings.TrimSpace(in.Contract),
	}, nil
}

// warningAcceptanceGuidance is the sentence renderServiceSummary appends
// whenever a service still has an unaccepted warning: acceptance, not a
// rewritten contract, is how a warning that faithfully describes the wire
// gets resolved. See the "Warnings" section of reference_service.md.
const warningAcceptanceGuidance = "If a warning describes the wire faithfully (for example an endpoint really returns text/plain), accept it in api/service.yaml under accepted_warnings with a reason instead of changing the contract to silence it."

// renderServiceSummary renders one service's post-sync status: operation
// count and status, every unaccepted warning (code, message, and location
// when known), how many warnings were reviewed and accepted, and, only
// when an unaccepted warning remains, warningAcceptanceGuidance. It is
// shared by add_service (on success, and after an E_CONFLICT resync) and
// sync_service, so the same facts render the same way from either tool.
func renderServiceSummary(ws *domain.Workspace, svc *domain.Service) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d operations, status %s\n", svc.Name, svc.OperationCount, svc.Status)
	b.WriteString(renderCoverage(svc.Coverage))
	if svc.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", svc.Error)
	}
	for _, w := range svc.Warnings {
		b.WriteString(formatWarningLine(w))
	}
	fmt.Fprintf(&b, "%d warnings accepted (reviewed)\n", len(svc.AcceptedWarnings))
	if len(svc.Warnings) > 0 {
		b.WriteString(warningAcceptanceGuidance + "\n")
	}
	b.WriteString(missingEnvHint(ws, svc))
	return b.String()
}

// renderCoverage states how much of the service is understandable, not just
// indexed: how many operations the narrative docs reach, and how many of those
// that take a body show a payload. The operation count alone reads as
// completeness, which is how services kept getting onboarded with a clean
// contract and docs the next agent could not use.
func renderCoverage(cov *domain.DocCoverage) string {
	if cov == nil || cov.Operations == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "docs: %d/%d operations documented in api/docs (%d sections)\n", cov.Documented, cov.Operations, cov.DocSections)
	if cov.Deprecated > 0 {
		// The operation count above includes deprecated operations and these
		// totals do not; say so rather than leaving two numbers to disagree.
		fmt.Fprintf(&b, "  (%d deprecated operations are not counted)\n", cov.Deprecated)
	}
	if cov.NeedExample > 0 {
		fmt.Fprintf(&b, "examples: %d/%d operations that take a body have a request example\n", cov.WithExample, cov.NeedExample)
	}
	return b.String()
}

// formatWarningLine renders one LintWarning as "warning [CODE] message
// (file[:line])\n", or without the parenthesized location when the warning
// carries no source.
func formatWarningLine(w domain.LintWarning) string {
	loc := ""
	if w.Source != nil && w.Source.File != "" {
		loc = w.Source.File
		if w.Source.Line > 0 {
			loc += fmt.Sprintf(":%d", w.Source.Line)
		}
	}
	if loc != "" {
		return fmt.Sprintf("warning [%s] %s (%s)\n", w.Code, w.Message, loc)
	}
	return fmt.Sprintf("warning [%s] %s\n", w.Code, w.Message)
}

// renderServiceAdded is add_service's text block on success: the shared
// status summary, plus what to do next.
func renderServiceAdded(ws *domain.Workspace, svc *domain.Service) string {
	var b strings.Builder
	b.WriteString("registered service ")
	b.WriteString(renderServiceSummary(ws, svc))
	b.WriteString("next: get_service to review what was indexed, then search_apis with a query a caller would type\n")
	b.WriteString("\nAdd this to the repo's CLAUDE.md or AGENTS.md (create AGENTS.md if neither exists) so future code changes keep api/ current:\n\n")
	b.WriteString(AgentsFileSection(svc.Name))
	return b.String()
}

// renderCheckoutCommitLine is the first line of renderServiceFromCheckoutAdded:
// what got committed to the team's sapien.workspace.yaml (url, ref, and
// subdir when the checkout is a monorepo package Team allowed through).
func renderCheckoutCommitLine(svc *domain.Service) string {
	if svc.Binding != nil && svc.Binding.Team != nil && svc.Binding.Team.Kind == domain.SourceGit {
		t := svc.Binding.Team
		line := fmt.Sprintf("committed %s to the team workspace as a git source: %s", svc.Name, t.URL)
		if t.Ref != "" {
			line += " @ " + t.Ref
		}
		if t.Subdir != "" {
			line += " (subdir " + t.Subdir + ")"
		}
		return line + "\n"
	}
	return fmt.Sprintf("committed %s to the team workspace\n", svc.Name)
}

// renderServiceFromCheckoutAdded is add_service's text block when the
// checkout flow succeeded: what was committed to sapien.workspace.yaml for
// every other clone to read, and what this machine itself reads (the
// checkout, writable).
func renderServiceFromCheckoutAdded(ws *domain.Workspace, svc *domain.Service) string {
	var b strings.Builder
	b.WriteString(renderCheckoutCommitLine(svc))
	b.WriteString(renderServiceReads(svc))
	b.WriteString(renderServiceSummary(ws, svc))
	b.WriteString("next: get_service to review what was indexed, then search_apis with a query a caller would type\n")
	return b.String()
}

// renderServiceAddedLocalFallback is add_service's text block when the
// checkout flow refused the path (reason, the refusal's own message) and
// it fell back to a plain local-only source instead of a team git source:
// says why, and that team: true commits the repository instead of falling
// back next time.
func renderServiceAddedLocalFallback(ws *domain.Workspace, svc *domain.Service, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s, so it was registered as a local-only source at %s instead of a team git source; pass team: true to commit the repository as the team's source instead\n",
		svc.Name, reason, svc.Source.Path)
	b.WriteString(renderServiceSummary(ws, svc))
	b.WriteString("next: get_service to review what was indexed, then search_apis with a query a caller would type\n")
	return b.String()
}

// AgentsFileSection is the block an agent pastes into a service repo's
// CLAUDE.md or AGENTS.md after onboarding it, so the next agent that
// changes the code also updates api/. It mirrors the "Keeping docs
// current" section of the service reference (reference_service.md).
func AgentsFileSection(serviceName string) string {
	return fmt.Sprintf(`## Sapien

This service is indexed by Sapien as `+"`%[1]s`"+` from api/ (openapi.yaml,
service.yaml, docs/).

When you add or change an endpoint, a request or response field, a status
code, or an error code, update api/openapi.yaml and the relevant
api/docs/*.md in the same change. Keep operationIds stable.

Before editing api/, read what Sapien already knows with the sapien MCP
tools: get_service("%[1]s"), search_apis, and get_dsl_reference("service")
for the format. Sapien re-indexes on save; `+"`sapien service sync %[1]s`"+`
forces it.
`, serviceName)
}

// --- sync_service -----------------------------------------------------

// SyncServiceInput is sync_service's arguments.
type SyncServiceInput struct {
	Name string `json:"name,omitempty" jsonschema:"service name; omit to sync every registered service"`
}

// SyncServiceOutput is sync_service's structured output. Repo is set only
// when name was empty (every service) and the workspace is inside a git
// repository: syncing everything also syncs the workspace's own
// repository (PLAN §7b).
type SyncServiceOutput struct {
	Services []domain.Service   `json:"services"`
	Repo     *domain.RepoStatus `json:"repo,omitempty"`
}

func (s *server) syncService(ctx context.Context, req *sdkmcp.CallToolRequest, in SyncServiceInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteServices); denied != nil {
		return denied, nil, nil
	}
	name := strings.TrimSpace(in.Name)
	svcs, err := s.engine().Services().Sync(ctx, name)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := SyncServiceOutput{Services: svcs}
	text := renderServicesSynced(s.engine().Workspace(), svcs)
	if name == "" {
		if repo, line := syncTeamRepo(ctx, s.engine()); line != "" {
			out.Repo = repo
			text += line
		}
	}
	return result(text, out), nil, nil
}

// syncTeamRepo syncs the workspace's own repository after sync_service
// (no name) already synced every service's -- the local engine's
// Services().Sync("") does this too, so this is a second, idempotent
// pass, worth making explicitly because Repo().Status alone never carries
// a reason a pull did not happen (Skipped). Returns a nil status and ""
// when the workspace is not inside a git repository, or checking it
// failed; syncing services must never fail for the repo's sake.
func syncTeamRepo(ctx context.Context, eng engine.Engine) (*domain.RepoStatus, string) {
	st, err := eng.Repo().Status(ctx)
	if err != nil || !st.InGit {
		return nil, ""
	}
	synced, err := eng.Repo().Sync(ctx)
	if err != nil {
		return nil, fmt.Sprintf("team repo: sync failed: %v\n", err)
	}
	return synced, "team repo: " + repoPullLine(synced) + "\n"
}

// repoPullLine renders the outcome of a Repo().Sync call: "pulled N
// commits", "not pulled: <skipped>", or "already current".
func repoPullLine(st *domain.RepoStatus) string {
	switch {
	case st.Pulled:
		return fmt.Sprintf("pulled %d commits", st.PulledCount)
	case st.Skipped != "":
		return fmt.Sprintf("not pulled: %s", st.Skipped)
	default:
		return "already current"
	}
}

// renderServicesSynced joins renderServiceSummary's block for each synced
// service, in the order Sync returned them, separated by a blank line.
func renderServicesSynced(ws *domain.Workspace, svcs []domain.Service) string {
	if len(svcs) == 0 {
		return "no services registered\n"
	}
	parts := make([]string, len(svcs))
	for i := range svcs {
		parts[i] = renderServiceSummary(ws, &svcs[i])
	}
	return strings.Join(parts, "\n")
}

// missingEnvHint names the environments svc's service.yaml declares that the
// workspace has no environments/<name>.yaml for, so an agent learns before
// its first execute_api that "stage" is not runnable yet and what creates
// it (feedback: "nothing said so; I found it by failing"). Empty when ws is
// unknown or nothing is missing.
func missingEnvHint(ws *domain.Workspace, svc *domain.Service) string {
	if ws == nil || svc == nil {
		return ""
	}
	missing := env.MissingForService(ws, *svc)
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("environments declared by %s but not defined in this workspace: %s; run `sapien env scaffold --workspace %s` to create them from service.yaml, then fill in auth\n",
		svc.Name, strings.Join(missing, ", "), ws.Dir)
}

// --- binding: what a service is read from (PLAN §7b) -------------------

// bindingOf is the ServiceBinding to describe svc with. The registry fills
// Service.Binding on every sync, but a daemon that predates it, or a fake,
// leaves it nil; deriving from Source then says the same thing the
// registry would (a git source is read from the managed clone, a local
// one from its path) rather than showing nothing.
func bindingOf(svc *domain.Service) domain.ServiceBinding {
	if svc.Binding != nil {
		return *svc.Binding
	}
	if svc.Source.Kind == domain.SourceGit {
		team := svc.Source
		return domain.ServiceBinding{Mode: domain.BindingTeam, Team: &team}
	}
	return domain.ServiceBinding{
		Mode:     domain.BindingLocal,
		Local:    &domain.LocalCheckout{Path: svc.Source.Path},
		Writable: true,
	}
}

// renderServiceReads is the one line get_service and list_services print
// about where a service's knowledge comes from on this machine, e.g.
//
//	reads: local ~/code/rider-service (branch feat/x, 3 uncommitted)
//	reads: team git@github.com:org/rider-service.git @ stage (read-only; bind a checkout to contribute)
//
// It is the difference between "I can write a service memory here" and
// "the call will be refused", so it is said before the agent finds out by
// failing.
func renderServiceReads(svc *domain.Service) string {
	return renderBindingLine(bindingOf(svc))
}

// renderBindingLine is renderServiceReads' body, taking the
// domain.ServiceBinding directly so find_checkouts (which gets one from
// Services().Binding without a whole domain.Service to hang it on) can
// render the exact same line as get_service and list_services.
func renderBindingLine(b domain.ServiceBinding) string {
	if b.Mode == domain.BindingTeam {
		src := ""
		if b.Team != nil {
			src = b.Team.URL
			if b.Team.Ref != "" {
				src += " @ " + b.Team.Ref
			}
		}
		if src == "" {
			src = "git source"
		}
		return fmt.Sprintf("reads: team %s (read-only; bind a checkout to contribute)\n", src)
	}
	line := "reads: local"
	if b.Local != nil {
		if b.Local.Path != "" {
			line += " " + b.Local.Path
		}
		var notes []string
		if b.Local.Branch != "" {
			notes = append(notes, "branch "+b.Local.Branch)
		}
		if b.Local.Dirty > 0 {
			notes = append(notes, fmt.Sprintf("%d uncommitted", b.Local.Dirty))
		}
		if len(notes) > 0 {
			line += " (" + strings.Join(notes, ", ") + ")"
		}
	}
	return line + "\n"
}

// --- bind_service / unbind_service / find_checkouts (PLAN §7b) ---------

// BindServiceInput is bind_service's arguments.
type BindServiceInput struct {
	Path    string `json:"path" jsonschema:"absolute path of the local git checkout to read the service from"`
	Service string `json:"service,omitempty" jsonschema:"service name; inferred from the checkout's origin (the one registered service naming that repository) when omitted"`
	Force   bool   `json:"force,omitempty" jsonschema:"bind even though the checkout's origin does not match the service's team repository, or it has no api/ package yet"`
}

// BindServiceOutput is bind_service's structured output.
type BindServiceOutput struct {
	Service domain.Service `json:"service"`
}

// bindService binds a service to a local checkout on this machine, so it
// is read from there and service-scoped memories, examples and flows
// become writable there instead of refused (a service read from its
// team's git source is read-only). An agent reaches this from a plain
// instruction like "read rider-service from my checkout in ~/code/x": path is
// required and must be absolute (the MCP server does not share the
// calling agent's working directory); service is optional and, when
// omitted, is inferred from the checkout's origin.
func (s *server) bindService(ctx context.Context, req *sdkmcp.CallToolRequest, in BindServiceInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteServices); denied != nil {
		return denied, nil, nil
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return errResult(errs.New(errs.Invalid, "bind_service needs path").
			WithHint("pass the absolute path of the local checkout to read the service from")), nil, nil
	}
	if !filepath.IsAbs(path) && path != "~" && !strings.HasPrefix(path, "~/") {
		return errResult(pathNotAbsoluteErr(path)), nil, nil
	}
	svc, err := s.engine().Services().BindWith(ctx, strings.TrimSpace(in.Service), path, engine.BindOptions{Force: in.Force})
	if err != nil {
		return errResult(err), nil, nil
	}
	text := fmt.Sprintf("bound %s\n", svc.Name) + renderServiceReads(svc)
	return result(text, BindServiceOutput{Service: *svc}), nil, nil
}

// UnbindServiceInput is unbind_service's arguments.
type UnbindServiceInput struct {
	Service string `json:"service" jsonschema:"service name, as returned by list_services"`
}

// UnbindServiceOutput is unbind_service's structured output.
type UnbindServiceOutput struct {
	Service domain.Service `json:"service"`
}

// unbindService undoes bind_service: the service goes back to being read
// from its committed team source (or stays local if it has no team
// source), so service-scoped memories, examples and flows can no longer be
// written for it here until it is bound again.
func (s *server) unbindService(ctx context.Context, req *sdkmcp.CallToolRequest, in UnbindServiceInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteServices); denied != nil {
		return denied, nil, nil
	}
	name := strings.TrimSpace(in.Service)
	if name == "" {
		return errResult(errs.New(errs.Invalid, "unbind_service needs service")), nil, nil
	}
	svc, err := s.engine().Services().Unbind(ctx, name)
	if err != nil {
		return errResult(err), nil, nil
	}
	text := fmt.Sprintf("unbound %s\n", svc.Name) + renderServiceReads(svc)
	return result(text, UnbindServiceOutput{Service: *svc}), nil, nil
}

// FindCheckoutsInput is find_checkouts' arguments.
type FindCheckoutsInput struct {
	Service string `json:"service" jsonschema:"service name, as returned by list_services"`
}

// FindCheckoutsOutput is find_checkouts' structured output: the service's
// current binding, plus every local checkout of the same repository this
// machine already knows about, so an agent can offer the user a choice
// instead of asking them to type a path from memory.
type FindCheckoutsOutput struct {
	Service    string                 `json:"service"`
	Binding    domain.ServiceBinding  `json:"binding"`
	Candidates []domain.LocalCheckout `json:"candidates,omitempty"`
}

// findCheckouts finds local checkouts of a service's repository already
// known on this machine, and what the service currently reads from, so an
// agent can offer the user a choice of checkout to bind_service with (and
// flag one that looks stale: an old commit, or far behind the team ref).
func (s *server) findCheckouts(ctx context.Context, req *sdkmcp.CallToolRequest, in FindCheckoutsInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	name := strings.TrimSpace(in.Service)
	if name == "" {
		return errResult(errs.New(errs.Invalid, "find_checkouts needs service")), nil, nil
	}
	info, err := s.engine().Services().Binding(ctx, name)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := FindCheckoutsOutput{Service: info.Service, Binding: info.Binding, Candidates: info.Candidates}

	var b strings.Builder
	fmt.Fprintf(&b, "%s currently %s", name, renderBindingLine(info.Binding))
	if len(info.Candidates) == 0 {
		b.WriteString("no other local checkouts of this repository are known on this machine\n")
	} else {
		b.WriteString("candidates:\n")
		for _, c := range info.Candidates {
			b.WriteString(renderCheckoutCandidate(c))
		}
		b.WriteString("an old commit or one far behind the team ref is worth flagging to the user before binding it\n")
	}
	return result(b.String(), out), nil, nil
}

// renderCheckoutCandidate renders one find_checkouts candidate line: path,
// then branch, how long ago it was committed to, ahead/behind the team
// ref, uncommitted files, and worktree, whichever apply.
func renderCheckoutCandidate(c domain.LocalCheckout) string {
	var notes []string
	if c.Branch != "" {
		notes = append(notes, "branch "+c.Branch)
	}
	if !c.CommittedAt.IsZero() {
		notes = append(notes, "committed "+c.CommittedAt.Format("2006-01-02"))
	}
	if c.Ahead > 0 || c.Behind > 0 {
		notes = append(notes, fmt.Sprintf("%d ahead/%d behind", c.Ahead, c.Behind))
	}
	if c.Dirty > 0 {
		notes = append(notes, fmt.Sprintf("%d uncommitted", c.Dirty))
	}
	if c.Worktree {
		notes = append(notes, "worktree")
	}
	line := "  " + c.Path
	if len(notes) > 0 {
		line += " (" + strings.Join(notes, ", ") + ")"
	}
	return line + "\n"
}
