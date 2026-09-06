package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/gitsrc"
)

// --- add_service ------------------------------------------------------

// AddServiceInput is add_service's arguments: exactly one of Path or URL,
// mirroring `sapien service add <path|git-url>`.
type AddServiceInput struct {
	Path     string `json:"path,omitempty" jsonschema:"absolute path of the service repo (or its api/ package) on this machine; use path or url, not both"`
	URL      string `json:"url,omitempty" jsonschema:"git remote (git@host:org/repo.git, ssh://, https://host/org/repo.git, file://); use path or url, not both"`
	Name     string `json:"name,omitempty" jsonschema:"service name; default: the name in service.yaml, else the contract's info.title"`
	Ref      string `json:"ref,omitempty" jsonschema:"git ref: branch, tag, or commit (git sources only)"`
	Subdir   string `json:"subdir,omitempty" jsonschema:"package directory inside the repo; default api (git sources only)"`
	Contract string `json:"contract,omitempty" jsonschema:"explicit contract file relative to the package root, overriding discovery"`
}

// AddServiceOutput is add_service's structured output.
type AddServiceOutput struct {
	Service domain.Service `json:"service"`
}

func (s *server) addService(ctx context.Context, req *sdkmcp.CallToolRequest, in AddServiceInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteServices); denied != nil {
		return denied, nil, nil
	}
	src, err := addServiceSource(in)
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

// addServiceSource turns the tool's arguments into a domain.Source, applying
// the same URL-vs-path rule as the CLI (gitsrc.IsGitURL) and insisting on an
// absolute local path: the MCP server's working directory is the daemon's,
// never the calling agent's, so a relative path could only resolve against
// the wrong place.
func addServiceSource(in AddServiceInput) (domain.Source, error) {
	path := strings.TrimSpace(in.Path)
	url := strings.TrimSpace(in.URL)

	switch {
	case path == "" && url == "":
		return domain.Source{}, errs.New(errs.Invalid, "add_service needs path or url").
			WithHint("pass path (the absolute local directory of the service repo) or url (a git remote)")
	case path != "" && url != "":
		return domain.Source{}, errs.New(errs.Invalid, "add_service takes path or url, not both")
	}

	// A git URL passed as path is an easy mistake for an agent; accept it.
	if url == "" && gitsrc.IsGitURL(path) {
		url, path = path, ""
	}

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
		return domain.Source{}, errs.New(errs.Invalid, "path %q is not absolute", path).
			WithDetail("path", path).
			WithHint("the MCP server does not share your working directory; pass the absolute path of the service repo")
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

// SyncServiceOutput is sync_service's structured output.
type SyncServiceOutput struct {
	Services []domain.Service `json:"services"`
}

func (s *server) syncService(ctx context.Context, req *sdkmcp.CallToolRequest, in SyncServiceInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteServices); denied != nil {
		return denied, nil, nil
	}
	svcs, err := s.engine().Services().Sync(ctx, strings.TrimSpace(in.Name))
	if err != nil {
		return errResult(err), nil, nil
	}
	return result(renderServicesSynced(s.engine().Workspace(), svcs), SyncServiceOutput{Services: svcs}), nil, nil
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
