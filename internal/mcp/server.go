package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// instructions is sent to every client at initialize (PLAN §23). Budget
// raised from ~1,300 to ~1,700 characters for the first two additions that
// paid for themselves: one sentence saying what Sapien actually is (agents
// used its tools without ever learning it is a cross-repo discovery layer,
// so they read schemas here and then called services from their own
// scripts), and the request_example/execute_api nudges. Every session pays
// for this text, so nothing goes in that a per-tool description can carry.
const instructions = `Sapien indexes the services around this repo: their contracts, their own documentation, working request examples, memories, and runnable flows. get_dsl_reference("sapien") says what it can do.
Start with get_context(intent). Discover with search_apis, inspect with get_api (its request_example is ready to send; never assemble a body from the field list), read its own documentation with search_docs/get_doc, read get_relevant_memories for the operations you will use, check list_flows, read get_dsl_reference("flow") once per session, then validate_flow until clean and create_flow; patch_flow for edits. Never invent operation IDs or fields; use only IDs tools returned. Turn memories of type invariant or testing on a selected operation into assertions or until steps. Save a working request with create_example(run_id) for the next agent. Call operations with execute_api, not curl or a script: it resolves the environment's base URL, headers and secrets, records the run, and diagnoses failures. Do not execute flows or endpoints unless the user asked.
To onboard a service or write Sapien-style docs: read get_dsl_reference("service"). Its reader is an agent in another repo: the docs must carry the business rules, not restate the contract; put an example: on every request body, ask the user what the code cannot answer, then add_service with the absolute repo path, close the coverage gap, fix the warnings, and paste the CLAUDE.md/AGENTS.md section it returns so future changes keep api/ current.
Resources: sapien://services/{name}(/docs/{path}), operations/{id}, schemas/{service}/{name}, flows/{id}, memories/{id}, reference/{sapien|flow-dsl|memory|expressions|service|flow.schema.json}.`

// Options configures a Sapien MCP server.
type Options struct {
	Engine engine.Engine
	// Workspaces, when set, registers list_workspaces and switch_workspace
	// so an agent can move between workspaces without the host restarting
	// this server. Nil binds the server to Engine for its lifetime.
	Workspaces WorkspaceSwitcher
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
	// mu guards eng, which switch_workspace replaces. Every tool reads it
	// through engine(), so a switch takes effect on the next call without
	// any tool holding a stale binding.
	mu  sync.RWMutex
	eng engine.Engine

	// switcher, when set, is what list_workspaces and switch_workspace act
	// on. Nil leaves this server bound to one workspace for its lifetime
	// and both tools unregistered, which is what a test server (and any
	// embedding that never wanted switching) gets.
	switcher WorkspaceSwitcher

	cfg       *configSource
	reference func(topic string) (string, error)
	logger    *slog.Logger
	// version is the Sapien build serving this session, recorded on
	// friction reports so a human can tell whether the friction is still
	// current.
	version string
}

// engine returns the currently bound engine.
func (s *server) engine() engine.Engine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eng
}

// bind points this server at another workspace's engine.
func (s *server) bind(eng engine.Engine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eng = eng
}

func (s *server) workspaceDir() string {
	if ws := s.engine().Workspace(); ws != nil {
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
	env, err := s.engine().Envs().Get(ctx, envName)
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
	srv := &server{
		eng:      opts.Engine,
		switcher: opts.Workspaces,
		cfg:      newConfigSource(opts.Config, opts.ConfigPaths),
		logger:   logger,
	}
	srv.reference = opts.Reference
	if srv.reference == nil {
		// Resolved through srv.engine() rather than opts.Engine so the
		// reference text follows a switch_workspace.
		srv.reference = func(topic string) (string, error) {
			return srv.engine().Flows().Reference(context.Background(), topic)
		}
	}

	version := opts.Version
	if version == "" {
		version = "dev"
	}
	srv.version = version

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
