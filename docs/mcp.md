# MCP

Sapien ships no LLM. Instead, `sapien mcp` exposes the engine over the
Model Context Protocol so an external agent — Claude Code, Codex, Cowork,
or any other MCP host — can discover services, author flows, capture
memories, and (with permission) execute calls, all against the same
catalog the CLI and daemon use.

## What it exposes

Transport: stdio (`sapien mcp`, daemon-backed — several hosts attached at
once share one engine, one watcher, and one live-run state) or
streamable HTTP on the daemon, for hosts that prefer a URL.

| Tool | Permission class | Notes |
|---|---|---|
| `list_services` | `read_contracts` | names, descriptions, op counts |
| `get_service` | `read_contracts` | description, owners, concepts, env base URLs, doc list |
| `search_apis` | `read_contracts` | id, method, path, summary, score, matched_on |
| `get_api` | `read_contracts` | `detail=summary\|fields\|full`; `fields`/`full` add `examples` (saved example ids, verified flag, description) |
| `search_docs` | `read_contracts` | doc sections: service, path, heading, snippet, score |
| `get_doc` | `read_contracts` | full Markdown of a doc, or one section |
| `get_schema` | `read_contracts` | a named component schema as flattened fields |
| `get_dsl_reference` | none | flow/memory/expressions/service reference, ~1.5k tokens per topic |
| `get_context` | `read_*` | the context builder; recommended first call for authoring |
| `add_service` | `write_services` | registers a service from a repo path or a git url; returns the operation count, sync status, warnings, and the CLAUDE.md/AGENTS.md section to paste; an already-registered service is re-synced instead of failing |
| `sync_service` | `write_services` | re-reads one service's `api/` package (or every service) and reindexes it; same summary as `add_service` |
| `execute_api` | `execute_read` (GET/HEAD/OPTIONS) or `execute_mutation` (otherwise); env must be allowed | redacted status/headers/body + run_id; on a failed call, "Might explain it" hints from the contract, docs, and memories; an `example` id may replace `id` (or accompany it, if they agree) as the base request, with `params`/`body`/`headers` overriding it field by field |
| `list_flows`, `get_flow` | `read_flows` | `get_flow` returns the YAML source; `detail: outline` lists steps one per line |
| `validate_flow` | `read_contracts` | diagnostics with line numbers and suggestions; `flow_yaml` or `path` (a file in `flows/`, validated without echoing it) |
| `create_flow`, `update_flow` | `write_flows` | validated first; rejected with diagnostics if invalid; return a summary (id, path, step counts, operations, warnings), never the document; `path` is relative to `flows/`; `update_flow` also accepts `path` to re-read a file edited on disk |
| `patch_flow` | `write_flows` | step-level ops (`set_step`, `merge_step`, `add_step`, `remove_step`, `set_inputs`, `set_meta`) applied to the YAML preserving comments |
| `run_flow` | `execute_*` by the highest-risk method used; env allowed | summary by default (one line per step, no bodies; `detail: failed` adds bodies for failed steps, `full` everything); soft mismatches and flips since the previous run; hints on failure; `resume_from`, `from_step`, `until_step` reuse an earlier run's results and bound execution |
| `get_run` | `read_runs` | optionally one step, with bodies; hints on failure |
| `search_memories` | `read_memories` | |
| `get_relevant_memories` | `read_memories` | structural retrieval over subjects |
| `create_memory` | `write_memories` | source recorded as `agent{client}`; the description carries the scope rule, the result says where it was stored, flags similar memories, and points at promotion |
| `rescope_memory` | `write_memories` | moves a memory between scopes without losing it (service = committed in the repo, workspace = local) |
| `get_promotion_target` | `read_contracts`, `read_memories` | locates the file/line to edit; never edits it |
| `list_examples` | `read_contracts` | filter by operation, service, tag, or text; id, operation, scope, verified env, description |
| `get_example` | `read_contracts` | one saved example, as YAML text plus structured fields |
| `create_example` | `write_examples` | either `run_id` (+ `step_id?`) to save a verified example from a recorded run, or `operation` (+ `input?`/`body?`/`headers?`) for a hand-written one — never both; `verified` can only ever come from the `run_id` path, never by hand; source recorded as `agent{client}` |
| `rescope_example` | `write_examples` | moves an example between scopes without losing it (service = committed in `<service>/api/examples`, workspace = local) |
| `delete_example` | `write_examples` | |

Resources: `sapien://services/{name}`, `.../docs/{path}`,
`sapien://operations/{id}`, `sapien://schemas/{service}/{name}`,
`sapien://flows/{id}`, `sapien://memories/{id}`, plus Sapien's own
reference material at `sapien://reference/flow-dsl`,
`sapien://reference/memory`, `sapien://reference/expressions`,
`sapien://reference/service`, and `sapien://reference/flow.schema.json`
— kept apart from service documentation so an agent never confuses the
two.

Every tool caps output: no more than `limit` items, schemas render as
flattened field lines rather than raw JSON Schema, and response bodies in
tool results are capped at 16 KB with a pointer to `get_run` for the rest.

## Permissions

Permissions live in `<workspace>/.sapien/mcp.yaml`, with a global default
in `~/.sapien/config.yaml` (under an `mcp:` key there, since that file
carries other settings too). Both are optional — an absent file means
every client gets the default profile below.

```yaml
# <workspace>/.sapien/mcp.yaml
default:
  read_contracts: true
  read_memories: true
  write_memories: true
  read_flows: true
  write_flows: true
  write_services: true      # add_service; onboarding a new service
  write_examples: true      # create_example, rescope_example, delete_example
  read_runs: true
  execute_read: true        # GET/HEAD/OPTIONS calls, non-production envs
  execute_mutation: false   # POST/PUT/PATCH/DELETE calls: opt in explicitly
  environments: []          # [] or omitted = every non-production environment
  allow_production: false   # true unlocks execute_* against production envs

clients:
  # Per-client name, matched against the MCP `clientInfo.name` the host
  # reports at initialize. Only the fields you set here override the
  # default profile above -- everything else is inherited.
  claude-code:
    execute_mutation: true
    environments: [local, staging]
  codex:
    write_flows: false      # a read-only research agent
```

A denied call returns `E_PERMISSION_DENIED` naming the missing class and
the config key to change, so the agent can relay it to whoever is
driving it rather than failing silently.

Both files are re-checked on every tool call: editing `mcp.yaml` (or the
global `config.yaml`) to grant or revoke a class takes effect on the very
next call from an already-connected client, with no need to restart the
daemon or reconnect the host.

## Host setup

`sapien mcp config --client claude-code|codex|cursor|cowork|generic
[--write] [--scope user|local|project]` prints, or with `--write`
installs, the host's MCP entry for `sapien mcp --workspace <dir>`. The
command needs to find a workspace: run it inside one, or pass
`--workspace <dir>` explicitly. All hosts share one daemon per
workspace, so a flow created from Cowork is visible to Claude Code and
Codex immediately, and vice versa.

`--scope` matters for `claude-code`:

- `user`, the default, installs the entry once for every Claude Code
  session on the machine, in any repo, since the entry bakes in the
  workspace's absolute path.
- `local` ties the entry to the directory the command is run in.
- `project` writes a `.mcp.json` in that directory for the team to
  commit.

`codex` and `cursor` configs are global by nature, living at
`~/.codex/config.toml` and `~/.cursor/mcp.json`, so both are available
everywhere regardless of scope. `sapien mcp config` resolves the running
binary's own absolute path, so it works even when `sapien` is a symlink,
for example `/usr/local/bin/sapien -> <repo>/bin/sapien`. Re-running
`--write` for `claude-code` replaces an existing `sapien` entry instead
of failing, so it is safe to run again after the workspace path changes.

Rebuilding `sapien` needs no re-install: the entry records the binary's
absolute path, so the next `sapien mcp` the host launches is the new
build. If a daemon from the previous build is still running for the
workspace, `sapien mcp` stops it and starts its own; restart the agent
session, or reconnect its MCP servers, so it launches the new binary.
The one-shot CLI stops a daemon from another build and runs in-process
for that command; it never starts a daemon itself, so the next `sapien
mcp` or `sapien serve` brings one up with the new build. Exactly one
daemon may hold a workspace: `serve` takes `.sapien/daemon.lock` for its
lifetime, a second one refuses to start (or replaces the holder with
`--restart`), and a spawned daemon's log is `.sapien/daemon.log`.

That restart is only needed for a new binary: `sapien mcp`'s stdio bridge
re-finds (or respawns) the workspace's daemon on demand, so a same-build
`sapien serve --restart` or the daemon's 30-minute idle timeout (PLAN §4)
recycling it underneath an already-connected host is transparent and
needs no reconnect.

You can also add the entry by hand; these are the exact snippets it
renders (`internal/mcp/hostconfig.go`), for `sapien mcp --workspace
/path/to/workspace`:

**Claude Code:**

```sh
claude mcp add --scope user sapien -- sapien mcp --workspace /path/to/workspace
```

**Codex** (`~/.codex/config.toml`):

```toml
[mcp_servers.sapien]
command = "sapien"
args = ["mcp", "--workspace", "/path/to/workspace"]
```

**Cursor** (`~/.cursor/mcp.json`, merged into the existing file):

```json
{
  "mcpServers": {
    "sapien": {
      "command": "sapien",
      "args": ["mcp", "--workspace", "/path/to/workspace"]
    }
  }
}
```

**Cowork** (`claude_desktop_config.json`, merged by `--write`; restart Claude Desktop) **/ generic** (any host reading an `mcpServers` block):

```json
{
  "mcpServers": {
    "sapien": {
      "args": [
        "mcp",
        "--workspace",
        "/path/to/workspace"
      ],
      "command": "sapien"
    }
  }
}
```

If you don't want a `sapien` binary installed on the host machine, swap
`"command": "sapien"`, or the shell snippet above, for `npx
@gs-sinha/sapien`. See
[`packages/npm/README.md`](../packages/npm/README.md).

Verify with `claude mcp list` and, in a new Claude Code session
anywhere, ask it to "list the Sapien services".

## Onboarding a service from an agent

`add_service` is how an agent registers a service it just wrote docs
for. Most services are not documented well enough for Sapien to index
directly, so the intended path is an agent working in that service's
own repo: it reads `get_dsl_reference("service")`, also readable as the
resource `sapien://reference/service`, for the exact `api/` package
layout, writes `api/openapi.yaml`, `api/service.yaml`, and
`api/docs/*.md` from the real code, then calls:

```json
add_service { "path": "/absolute/path/to/repo" }
add_service { "url": "git@github.com:org/repo.git", "ref": "main", "subdir": "api" }
```

`path` must be absolute, since the MCP server does not share the
calling agent's working directory. `name`, `contract`, and, for git
sources, `ref` and `subdir` are optional. The tool returns the operation
count, sync status, and warnings, such as an operation missing an
`operationId`; the agent fixes those and confirms with `get_service` and
`search_apis`. The result also carries a ready-to-paste "Sapien" section
for the repo's `CLAUDE.md` or `AGENTS.md`, telling future agents to
update `api/` in the same change as the code. From a shell the equivalent is `sapien service add
"$PWD" --workspace /abs/workspace`, run inside the service repo.
`add_service` is gated by the `write_services` permission class,
default `true`, configurable the same way as the other classes above.
See [`docs/onboarding.md`](onboarding.md) for the full walkthrough.

## The authoring loop

The MCP server's `instructions` (sent at `initialize`) describe the
intended loop so hosts need no custom system prompt:

> Start with `get_context(intent)`. Discover with `search_apis`, inspect
> with `get_api`, read the service's own documentation with `search_docs`
> and `get_doc`, read `get_relevant_memories` for the operations you will
> use, check `list_flows` for existing flows, read
> `get_dsl_reference("flow")` once per session, then `validate_flow`
> until clean and `create_flow`. Never invent operation IDs or fields;
> use only IDs returned by tools. Turn memories of type `invariant` or
> `testing` attached to a selected operation into assertions or `until`
> steps. Check `list_examples`/`get_api` for a verified example before
> writing a request body; save a working one with `create_example(run_id)`
> so the next agent does not rediscover it. Do not execute flows or
> endpoints unless the user asked. To onboard a service or write
> Sapien-style docs: read `get_dsl_reference("service")`, create the
> `api/` package in the repo from its real code, then `add_service` with
> the absolute repo path, fix the warnings it returns, and paste the
> `CLAUDE.md`/`AGENTS.md` section it suggests into the repo so future code
> changes keep `api/` current.

Saved examples (PLAN §34b) close the loop `get_context` opens: an agent
that had to feel its way to a working request body should not make the
next agent do the same work. `list_examples`/`get_api` surface what
already exists (verified ones first); `execute_api(example: <id>)` or a
flow step's `example: <id>` replays one directly, with any `params`/
`body`/`headers` given alongside it overriding the example field by
field; and `create_example(run_id: <id>)` — right after a call that
worked — turns that run into a verified example the next agent (or the
CLI, or the desktop UI, once built) can start from instead of guessing. A
hand-written example (`create_example(operation: ...)`) is useful too,
but can never carry `verified`: only a save-from-run path sets it, so a
tested example is always distinguishable from a drafted one.

`validate_flow`'s diagnostics carry the repair loop: unknown operation ->
nearest IDs by fuzzy match; unknown input name -> the operation's actual
parameter names; unknown field in an expression -> nearest field paths
from that step's response; a reference to a later step -> the step
order; a CEL parse/type error -> the message rewritten in DSL terms. The
CLI and any future UI show the same diagnostics.

## Production safeguards

- `execute_api` and `run_flow` check the target environment's
  `production` flag against the caller's `allow_production` permission
  (default `false`) *and* its `environments` allowlist, independent of
  each other — both must pass.
- Denied calls fail closed with `E_PERMISSION_DENIED`, never a partial
  execution.
- This mirrors the CLI's own `--allow-production` flag on `call` and
  `flow run` (PLAN.md §20, §28): the safeguard is enforced once, in the
  engine, so MCP and the CLI can't drift apart.
- `create_memory` text is scanned for secret-like values before being
  written, with a warning — memories are meant to be shared.
- The flow validator rejects `secret.NAME` anywhere outside
  `headers`/`params.headers` (`SECRET_CONTEXT`), so a flow authored by an
  agent can't end up with a secret in its body, an assertion, or a saved
  run record.
