package mcp

import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools wires every tool from PLAN §23's table onto s.
func (srv *server) registerTools(s *sdkmcp.Server) {
	// Workspace switching is registered only when this server actually has
	// somewhere to switch to, so a single-workspace host never shows an
	// agent two tools that can only fail.
	if srv.switcher != nil {
		sdkmcp.AddTool(s, &sdkmcp.Tool{
			Name:        "list_workspaces",
			Description: "List the workspaces this daemon can serve and mark the current one. Each workspace is a separate catalog, with its own services, flows, memories and runs; ids from one never resolve in another.",
		}, srv.listWorkspaces)

		sdkmcp.AddTool(s, &sdkmcp.Tool{
			Name:        "switch_workspace",
			Description: "Bind this session to another workspace, by directory or by the name list_workspaces reports. Every later tool call acts on it. Switch when the user names a system this workspace does not carry; do not switch mid-task to look something up and forget to switch back.",
		}, srv.switchWorkspace)
	}

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_services",
		Description: "List registered services with descriptions, operation counts, and what each is read from on this machine (a local checkout, writable; or the team's git source, read-only).",
	}, srv.listServices)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_service",
		Description: "Get one service's description, owners, concepts, environment base URLs, doc list, and binding: what this machine reads it from. A service read from the team's git source is read-only for service-scoped memories, examples and flows until a local checkout is bound.",
	}, srv.getService)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "search_apis",
		Description: "Search operations by id, path, summary, description, tags, params, and field names.",
	}, srv.searchAPIs)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_api",
		Description: "Get one operation's contract at progressively more detail: summary, fields, or full. Every level includes a ready-to-send request_example (verified example, saved example, the contract's own example, or one synthesized from the schema), so there is no need to assemble a payload from the field list.",
	}, srv.getAPI)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "add_service",
		Description: "Register a service from an absolute local path or a git URL and index it. Read get_dsl_reference(\"service\") first for the api/ package layout Sapien expects; fix the warnings this returns.",
	}, srv.addService)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "sync_service",
		Description: "Re-read a registered service's api/ package and reindex it (every service if name is omitted); returns status and warnings. Use after editing openapi.yaml, service.yaml, or docs, or when add_service reports the service is already registered.",
	}, srv.syncService)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_dsl_reference",
		Description: "Get a Sapien reference with worked examples: sapien (what Sapien is and what you can do with it), flow (the flow DSL, including soft assertions), memory, expressions, or service (how to lay out and register a service's api/ package so Sapien can index it).",
	}, srv.getDSLReference)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_context",
		Description: "Build an agent-ready context bundle (operations, docs, memories, flows, runs) for an authoring intent. The recommended first call for any authoring task.",
	}, srv.getContext)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "execute_api",
		Description: "Execute one operation as a one-step run against an environment. Prefer this over curl or a throwaway script: the environment supplies the base URL, headers, and secrets, the call is recorded as a run, a failure comes back diagnosed, and a call that worked can be saved with create_example for the next caller.",
	}, srv.executeAPI)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_flows",
		Description: "List flows, optionally filtered by name, tag, or operation substring. Each carries its tier: local (this machine only), workspace (the team's repo), or service (the owning service's api/flows).",
	}, srv.listFlows)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_flow",
		Description: "Get one flow's definition and YAML source. The text content is the YAML; it is valid input to update_flow/create_flow as-is (an assertion round-trips as `expr:`).",
	}, srv.getFlow)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "search_docs",
		Description: "Search service documentation sections by heading and body text.",
	}, srv.searchDocs)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_doc",
		Description: "Get the full Markdown of a documentation file, or one section of it.",
	}, srv.getDoc)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_schema",
		Description: "Get a named component schema as flattened fields, plus the operations that use it.",
	}, srv.getSchema)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "validate_flow",
		Description: "Validate flow YAML against the catalog; returns diagnostics with line numbers and suggestions. Pass `flow_yaml` inline, or `path` to a file already on disk (relative to <workspace>/flows, or to the workspace root when inside flows/ or local/flows/).",
	}, srv.validateFlow)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "create_flow",
		Description: "Validate and write a new flow file into a tier: `scope` local (default: <workspace>/local/flows, this machine only, never committed), workspace (<workspace>/flows, the team's repo), or service (<service>/api/flows, needs `service` and a bound local checkout). Start local; promote with rescope_flow once it runs green. `path` (default `<id>.flow.yaml`) is relative to the chosen tier's flows directory, not the workspace root; it must stay inside that directory and end in .flow.yaml. Returns a lean summary (id, path, tier, step counts, operations, warnings, bytes), never the flow document -- use get_flow to read it back, or patch_flow for a small targeted edit instead of resending the whole document.",
	}, srv.createFlow)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "update_flow",
		Description: "Validate and overwrite an existing flow's YAML, from `flow_yaml` sent inline or a `path` this agent already edited on disk: relative to <workspace>/flows, or relative to the workspace root when it lies inside flows/ or local/flows/. Returns the same lean summary as create_flow. Prefer patch_flow for a small, targeted edit to a large flow.",
	}, srv.updateFlow)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "patch_flow",
		Description: "Apply targeted edits to one saved flow's steps, inputs, or metadata (set_step, merge_step, add_step, remove_step, set_inputs, set_meta) without resending the whole document; validates the result and returns the same lean summary as create_flow/update_flow. Cheaper than update_flow for a one-line change to a large flow.",
	}, srv.patchFlow)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "rescope_flow",
		Description: "Move a flow to another tier without losing it, keeping its file name. The ladder: local is this machine only (<workspace>/local/flows, never committed); workspace is the team's git repo (<workspace>/flows, shared with everyone who clones the workspace); service is the owning service's own repo (<service>/api/flows) and needs that service bound to a local checkout here, so the flow rides your branch and PR. Promote a flow up the ladder once it has run green and others would benefit; move it down to keep experimenting privately. Returns the lean create_flow summary plus old_path and new_path.",
	}, srv.rescopeFlow)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "report_friction",
		Description: reportFrictionDescription,
	}, srv.reportFriction)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "run_flow",
		Description: "Run a flow against an environment; returns a summary and per-step status. resume_from/from_step/until_step replay an earlier run's proven steps instead of re-executing them, so a retry after a mid-flow failure doesn't repeat side effects the flow already caused.",
	}, srv.runFlow)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_run",
		Description: "Get a run's full detail, optionally restricted to one step, with request/response bodies.",
	}, srv.getRun)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "search_memories",
		Description: "Search memories by text, optionally restricted to an operation or service.",
	}, srv.searchMemories)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_relevant_memories",
		Description: "Structural memory retrieval for a set of subjects (operations, services, schemas, flows, concepts, ...).",
	}, srv.getRelevantMemories)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "create_memory",
		Description: "Record a new memory; source is attributed to this MCP client. Choose scope deliberately: scope decides storage and sharing, not subject. service = written to <service>/api/memories, committed and reviewable, reaches everyone who clones the repo; workspace = <workspace>/memories, local to this machine unless the workspace is a git repo; personal = this machine only. Ask: would this still be true in a fresh environment with empty databases? yes -> service, no -> workspace. A memory that mixes both must be split, not forced into one scope. A workspace memory may still carry a service subject. Service scope needs the service bound to a local checkout on this machine (see get_service's binding); a service read from the team's git source is read-only and the call is refused with a hint to bind one. Memory is a staging area: once a fact stabilises, get_promotion_target says where it belongs in api/docs or the contract.",
	}, srv.createMemory)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "rescope_memory",
		Description: "Move a memory to another scope without losing it: service = committed in <service>/api/memories and shared with everyone who clones; workspace = local to this workspace; personal = this machine only. Scope decides storage and sharing, not subject.",
	}, srv.rescopeMemory)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_promotion_target",
		Description: "Find where a memory's knowledge belongs canonically (an OpenAPI file/line/pointer, or a doc section).",
	}, srv.getPromotionTarget)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_examples",
		Description: "List saved request examples, optionally filtered by operation, service, tag, or text.",
	}, srv.listExamples)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_example",
		Description: "Get one saved example as YAML plus structured fields.",
	}, srv.getExample)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "create_example",
		Description: "Save a request example for one operation: from a run (run_id, verified) or hand-written (operation + input/body/headers, never verified). Source is attributed to this MCP client.",
	}, srv.createExample)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "rescope_example",
		Description: "Move a saved example to another scope without losing it: service = committed in <service>/api/examples and shared with everyone who clones; workspace = local to this workspace.",
	}, srv.rescopeExample)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "delete_example",
		Description: "Delete a saved example.",
	}, srv.deleteExample)
}
