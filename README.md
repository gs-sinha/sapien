# Sapien

Sapien is a local-first, agent-native API workspace engine: point it at a
set of service repos (each with an OpenAPI contract, a small
`service.yaml`, flows, and memories) and it builds a normalized,
searchable catalog, runs flows (a CEL-powered DSL for chaining calls with
assertions), and gives humans, CI, MCP agents, and a future desktop UI
the same engine through one facade — a CLI for one-shot use, `sapien
serve` as a local daemon, and `sapien mcp` for agents. No LLM ships in
the engine; agents author flows and memories through MCP.

## Status

Pre-release, engine-only (PLAN.md's Phases 0–6 are built; the desktop
app is Phase 7+). Catalog, search, execution (environments, secrets,
`call`, flows, assertions, runs), memory, the `sapien serve` daemon, and
the `sapien mcp` server all work from the CLI today; see
[`docs/getting-started.md`](docs/getting-started.md). The release
pipeline, Homebrew tap, and npm package exist but have not shipped a
tagged version yet, so building from source is the reliable install for
now. See [`CHANGELOG.md`](CHANGELOG.md) for the deliverable list and
[`docs/BUILD-LOG.md`](docs/BUILD-LOG.md) for the running build record.

## Install

From source (works today; needs Go 1.25+):

```sh
git clone https://github.com/growsimplee/sapien.git && cd sapien
make build                                   # writes bin/sapien
ln -s "$PWD/bin/sapien" /usr/local/bin/sapien   # or add bin/ to PATH
sapien version
```

Once a tagged release ships, these will work too:

```sh
brew install growsimplee/tap/sapien
curl -fsSL https://raw.githubusercontent.com/growsimplee/sapien/main/scripts/install.sh | sh
npx @growsimplee/sapien version
go install github.com/growsimplee/sapien/cmd/sapien@latest
```

## Make it operational

Five steps take you from a fresh machine to an agent that can search,
call, and onboard your services.

1. Install `sapien` and put it on your `PATH`. See Install above.
2. Create and initialize a workspace:

   ```sh
   sapien init ~/sapien-workspace
   ```

   This creates the folder if it does not exist yet and writes
   `sapien.workspace.yaml`, `flows/`, `memories/`, `environments/local.yaml`,
   and a gitignored `.sapien/`. Docs and contracts do not need to exist
   before this step; they belong to services and are registered next.

3. Install the MCP entry everywhere:

   ```sh
   sapien mcp config --client claude-code --write
   ```

   Run this inside the workspace, or add `--workspace /abs/path/to/sapien-workspace`
   from anywhere. It defaults to `--scope user`, so the `sapien` MCP
   server becomes available in every Claude Code session on the machine,
   in any repo. `--scope local` ties it to the directory you run the
   command in; `--scope project` writes a `.mcp.json` there for the team.
   The same idea works for `codex` and `cursor`, whose configs are global
   by nature. Rebuilding `sapien` later needs no re-install: the entry
   records the binary's path, and `sapien mcp` replaces a daemon left
   over from the previous build on its own.

4. Onboard your first service from an agent, in that service's own repo.
   Open Claude Code, Codex, or Cursor there and say:

   > Onboard this service to Sapien

   The agent reads `get_dsl_reference("service")`, writes the `api/`
   package from the real code, and registers it with `add_service`. See
   "Onboarding services" below. Then, in the workspace, run
   `sapien env scaffold` to create the environments the services declare,
   fill in auth, and `sapien env probe stage` to check they answer.

5. Open the inspector and verify from a new agent session:

   ```sh
   sapien ui
   ```

   This opens a browser tab served by the daemon: flows the agent created,
   every run with its payloads, services, operations with intent search, a
   try-it form, examples, and memories, all updating live as agents work.
   Then, in a new Claude Code session, ask it to "list the Sapien
   services".

   Verify the MCP entry itself with:

   ```sh
   claude mcp list
   ```

   Then, in a new Claude Code session in any directory, ask it to "list
   the Sapien services" and it should see everything onboarded so far.
   When a call works, keep it: `sapien call ... --save-example <name>` (or
   the agent's `create_example` with the run id) stores the verified
   request so flows, other agents, and the UI reuse it. See
   [`docs/examples.md`](docs/examples.md).

Keep one workspace per system: every service your team calls together,
from however many repos. Search, `get_context`, memory-driven expansion,
and flows all span a workspace, and each MCP entry binds one workspace,
so splitting a system across workspaces hides cross-service knowledge
from the agent. Use a second workspace only for an unrelated system. The
workspace folder is separate from the service repos and can be its own
git repo, so the flows, memories, and environments you build up are
versioned too.

## Onboarding services

Most services are not documented well enough for Sapien to index
directly, so onboarding is agent-driven: open an agent in the service's
repo and ask it to onboard the service, or to write Sapien-style API
docs for it. Because the Sapien MCP server is installed everywhere, the
agent already has Sapien's tools available no matter which repo it is
running in.

The agent writes an `api/` package next to the service's code:

```text
api/openapi.yaml   the OpenAPI contract, required
api/service.yaml   name, description, owners, concepts, environments
api/docs/*.md      narrative documentation, one file per area
api/flows/         optional flows shipped with the service
```

It then calls the `add_service` tool with the repo's absolute path,
fixes any warnings Sapien returns (or accepts a warning that describes
the wire faithfully in `api/service.yaml` with a reason, instead of
silencing it), confirms with `get_service` and
`search_apis`, and adds the "Sapien" section the tool hands back to the
repo's `CLAUDE.md` or `AGENTS.md`, so the next agent that changes the
code updates `api/` in the same change. The full layout, the conventions docs must follow to link
prose to the contract, and the checklist an agent works through are in
[`internal/engine/local/reference_service.md`](internal/engine/local/reference_service.md),
the same text served by the MCP tool `get_dsl_reference("service")`. See
[`docs/onboarding.md`](docs/onboarding.md) for the full human-facing
walkthrough.

## Try it with the sample services

Sixty seconds against the bundled logistics fixture (three sample
services: orders, allocation, riders):

```sh
# terminal 1
make fixtures

# terminal 2
make build
./bin/sapien init demo && cd demo
../bin/sapien service add ../fixtures/logistics/order-service
../bin/sapien service add ../fixtures/logistics/allocation-service
../bin/sapien service add ../fixtures/logistics/rider-service

../bin/sapien search "allocate rider"
../bin/sapien call order-service.createOrder \
  --body '{"customerId":"c1","type":"QCOM","pickup":{"lat":12.9716,"lng":77.5946},"drop":{"lat":12.9352,"lng":77.6146}}'
# => "orderId": "ord_0001"

cat > allocate.flow.yaml <<'EOF'
version: 1
id: allocate
inputs:
  orderId: { type: string, required: true }
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${inputs.orderId}" }
    extract: { riderId: body.riderId }
    assert: [status == 201]
  - id: rider
    call: rider-service.getRider
    input: { riderId: "${steps.allocate.out.riderId}" }
    assert: [status == 200, body.online == true]
EOF
../bin/sapien flow run allocate.flow.yaml -i orderId=ord_0001
```

That last command prints a per-step table and exits 0. See
[`fixtures/logistics/README.md`](fixtures/logistics/README.md) for the
fixture's full seed data and success scenario.

## MCP

For agent hosts that assume `npx`:

```sh
npx @growsimplee/sapien mcp --workspace .
```

or, with `sapien` installed, `sapien mcp --workspace .`. `sapien mcp
config --client <name> --write` installs the entry for the client named:
`claude-code`, `codex`, `cursor`, `cowork`, or `generic`. It defaults to
`--scope user`, so the entry it installs works from any repo on the
machine, and re-running it is safe: a second `--write` for `claude-code`
replaces the existing `sapien` entry instead of failing. Tools include
`search_apis`, `get_api`, `create_flow`, `run_flow`, `create_memory`, and
`add_service`, the tool an agent calls to register a service it just
wrote docs for. See [`docs/mcp.md`](docs/mcp.md) for the full tool list,
permissions, and host setup for every client.

## Learn more

- [`docs/getting-started.md`](docs/getting-started.md) — install, workspaces, services, calls, environments
- [`docs/onboarding.md`](docs/onboarding.md) — onboarding a service to Sapien with an agent
- [`docs/flows.md`](docs/flows.md) — the flow DSL, assertions, running in CI
- [`docs/memory.md`](docs/memory.md) — capturing and retrieving operational knowledge
- [`docs/examples.md`](docs/examples.md): saving known-good requests and replaying them
- [`docs/mcp.md`](docs/mcp.md) — the MCP server, tools, permissions, host setup
- [`docs/architecture.md`](docs/architecture.md) — the four primitives, engine package map
- [`docs/ci.md`](docs/ci.md) — running flows in GitHub Actions
- [`docs/faq.md`](docs/faq.md) — why not Postman, why OpenAPI stays in the repo
- [`PLAN.md`](PLAN.md) — the full technical implementation plan

`make test` runs the suite with `-race -cover`; `make lint` mirrors CI's
`gofmt`/`go vet` checks; `make bench` runs the benchmark-style tests
(PLAN.md §32) in isolation and without `-race`, whose overhead alone can
blow past their wall-clock budgets.
