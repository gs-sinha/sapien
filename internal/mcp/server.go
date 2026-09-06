package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// instructions is sent to every client at initialize (PLAN §23). It is kept
// under about 1,300 characters.
const instructions = `Start with get_context(intent). Discover with search_apis, inspect with get_api, read the service's own documentation with search_docs and get_doc, read get_relevant_memories for the operations you will use, check list_flows for existing flows, read get_dsl_reference("flow") once per session, then validate_flow until clean and create_flow; patch_flow for edits. Never invent operation IDs or fields; use only IDs returned by tools. Turn memories of type invariant or testing attached to a selected operation into assertions or until steps. Check list_examples/get_api for a verified example before writing a request body; save a working one with create_example(run_id) so the next agent does not rediscover it. Do not execute flows or endpoints unless the user asked.
To onboard a service or write Sapien-style docs: read get_dsl_reference("service"), create the api/ package in the repo from its real code, then add_service with the absolute repo path, fix the warnings it returns, and paste the CLAUDE.md/AGENTS.md section it suggests into the repo so future code changes keep api/ current.
Resources: sapien://services/{name}(/docs/{path}), operations/{id}, schemas/{service}/{name}, flows/{id}, memories/{id}, reference/{flow-dsl|memory|expressions|service|flow.schema.json}.`

// Options configures a Sapien MCP server.
type Options struct {
	Engine engine.Engine
	// Config is the static permission configuration, used as-is when
	// ConfigPaths is empty. Existing callers (mainly tests) that never set
	// ConfigPaths keep this exact behaviour.
	Config Config
	// ConfigPaths, when non-empty, makes permissionsFor hot-reload the
	// permission configuration from these files (PLAN §23.2): each tool
	// call rereads them via LoadConfig if any has changed (or appeared or
	// disappeared) since the last load, otherwise it reuses the cached
	// Config. This is what lets an edit to <workspace>/.sapien/mcp.yaml
	// (e.g. granting execute_mutation) take effect on the very next call,
	// with no daemon or client restart. Config is still consulted as a
	// fallback if the very first load fails.
	ConfigPaths []string
	// Version is reported as the server implementation version.
	Version string
	// Reference returns the DSL reference text for a topic (flow, memory,
	// expressions). If nil, defaults to Engine.Flows().Reference.
	Reference func(topic string) (string, error)
	Logger    *slog.Logger
}

// server holds the dependencies every tool and resource handler needs.
type server struct {
	eng       engine.Engine
	cfg       *configSource
	reference func(topic string) (string, error)
	logger    *slog.Logger
}

func (s *server) workspaceDir() string {
	if ws := s.eng.Workspace(); ws != nil {
		return ws.Dir
	}
	return "<workspace>"
}

// clientName resolves the calling client's name from its initialize
// request, per PLAN §23.2 ("by client name (from initialize request
// clientInfo.name)").
func clientName(session *sdkmcp.ServerSession) string {
	if session == nil {
		return ""
	}
	ip := session.InitializeParams()
	if ip == nil || ip.ClientInfo == nil {
		return ""
	}
	return ip.ClientInfo.Name
}

// permissionsFor resolves the permission set for the session's client,
// reloading the underlying config from disk first if Options.ConfigPaths
// was set and any of those files changed since the last load (see
// configSource.Load).
func (s *server) permissionsFor(session *sdkmcp.ServerSession) (string, Permissions) {
	name := clientName(session)
	return name, s.cfg.Load().For(name)
}

// deniedResult builds the E_PERMISSION_DENIED tool result described in the
// task: IsError text plus the structured errs.Error.
func (s *server) deniedResult(client string, class permClass) *sdkmcp.CallToolResult {
	msg := fmt.Sprintf("%s: %s not granted to client %q; grant it in %s/.sapien/mcp.yaml under clients.%s.%s: true",
		errs.PermissionDenied, class, client, s.workspaceDir(), client, class)
	e := errs.New(errs.PermissionDenied, "%s not granted to client %q", class, client).
		WithHint(fmt.Sprintf("grant it in %s/.sapien/mcp.yaml under clients.%s.%s: true", s.workspaceDir(), client, class))
	return &sdkmcp.CallToolResult{
		IsError:           true,
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: msg}},
		StructuredContent: e,
	}
}

// checkPermission returns a denial result if the client lacks class, else nil.
func (s *server) checkPermission(session *sdkmcp.ServerSession, class permClass) (client string, perm Permissions, denied *sdkmcp.CallToolResult) {
	client, perm = s.permissionsFor(session)
	if !perm.allows(class) {
		return client, perm, s.deniedResult(client, class)
	}
	return client, perm, nil
}

// errResult builds an IsError tool result carrying the structured error, per
// PLAN §29: "MCP (isError with the same object)".
func errResult(err error) *sdkmcp.CallToolResult {
	e := errs.As(err)
	return &sdkmcp.CallToolResult{
		IsError:           true,
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: fmt.Sprintf("%s: %s", e.Code, e.Message)}},
		StructuredContent: e,
	}
}

// checkEnvironment enforces the client's environment allowlist and the
// production gate (PLAN §23.2, §28).
func (s *server) checkEnvironment(ctx context.Context, perm Permissions, envName string) *sdkmcp.CallToolResult {
	if len(perm.Environments) > 0 {
		allowed := false
		for _, e := range perm.Environments {
			if e == envName {
				allowed = true
				break
			}
		}
		if !allowed {
			e := errs.New(errs.PermissionDenied, "environment %q is not in the allowed environments for this client", envName).
				WithHint(fmt.Sprintf("grant it in %s/.sapien/mcp.yaml under clients.<name>.environments", s.workspaceDir()))
			return errResult(e)
		}
	}
	env, err := s.eng.Envs().Get(ctx, envName)
	if err != nil {
		return errResult(err)
	}
	if env.Production && !perm.AllowProduction {
		e := errs.New(errs.ProductionBlocked, "environment %q is production; production execution is not allowed for this client", envName).
			WithHint(fmt.Sprintf("grant it in %s/.sapien/mcp.yaml under clients.<name>.allow_production: true", s.workspaceDir()))
		return errResult(e)
	}
	return nil
}

// executeClassFor picks the permission class for a method: execute_read for
// GET/HEAD/OPTIONS, execute_mutation otherwise.
func executeClassFor(method string) permClass {
	switch method {
	case "GET", "HEAD", "OPTIONS":
		return classExecuteRead
	default:
		return classExecuteMutation
	}
}

// NewServer builds a Sapien MCP server: instructions, tools, and resources
// wired against opts.Engine, gated by opts.Config.
func NewServer(opts Options) *sdkmcp.Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	reference := opts.Reference
	if reference == nil {
		reference = func(topic string) (string, error) {
			return opts.Engine.Flows().Reference(context.Background(), topic)
		}
	}
	srv := &server{eng: opts.Engine, cfg: newConfigSource(opts.Config, opts.ConfigPaths), reference: reference, logger: logger}

	version := opts.Version
	if version == "" {
		version = "dev"
	}

	s := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "sapien", Version: version}, &sdkmcp.ServerOptions{
		Instructions: instructions,
		Logger:       logger,
	})

	srv.registerTools(s)
	srv.registerResources(s)

	return s
}

// ServeStdio runs a Sapien MCP server over stdio until ctx is cancelled or
// the client disconnects (`sapien mcp`, PLAN §23).
func ServeStdio(ctx context.Context, opts Options) error {
	s := NewServer(opts)
	return s.Run(ctx, &sdkmcp.StdioTransport{})
}

// HTTPHandler returns a streamable-HTTP handler for the daemon to mount at
// /mcp (PLAN §23: "streamable HTTP on the daemon for hosts that prefer a URL").
func HTTPHandler(opts Options) http.Handler {
	s := NewServer(opts)
	return sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return s }, nil)
}
