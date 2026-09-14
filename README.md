# Sapien

Website: https://gs-sinha.github.io/sapien/

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
git clone https://github.com/gs-sinha/sapien.git && cd sapien
make build                                   # writes bin/sapien
ln -s "$PWD/bin/sapien" /usr/local/bin/sapien   # or add bin/ to PATH
sapien version
```

Once a tagged release ships, these will work too:

```sh
brew install gs-sinha/tap/sapien
curl -fsSL https://raw.githubusercontent.com/gs-sinha/sapien/main/scripts/install.sh | sh
npx @gs-sinha/sapien version
go install github.com/gs-sinha/sapien/cmd/sapien@latest
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
   sapien mcp config --client claude-code --write --allow-mutations
   ```

   `--allow-mutations` lets agents make POST, PUT, PATCH and DELETE calls on
   non-production environments, which running a flow that creates data
   needs; without it they can only read. It sets `execute_mutation` in the
   workspace's gitignored `.sapien/mcp.yaml`, so it is per machine, and
   production stays blocked. `sapien mcp config --allow-mutations` on its own
   grants it later without touching the host entry.

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

   This opens a tab in your default browser, served by the daemon: flows
   the agent created, every run with its payloads, services, operations
   with intent search, a try-it form, examples, and memories, all updating
   live as agents work. Then, in a new Claude Code session, ask it to
   "list the Sapien services".

   `sapien ui --install-app` installs `~/Applications/Sapien.app` so you can
   open the inspector from Spotlight or the Dock instead of a terminal.

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

## Team workspaces

Commit the workspace folder as its own git repo and list every service as
a git source. Anyone who clones it gets the whole system: services,
environments, the team's flows, memories and examples, refreshed from
GitHub every ten minutes.

```yaml
# sapien.workspace.yaml (committed)
version: 1
name: logistics
services:
  - name: rider-service
    source: { type: git, url: git@github.com:company/rider-service.git, ref: stage }
```

A git source is read from a managed clone that Sapien resets on every
sync, so it is read-only. When you work on a service, bind it to your
checkout, from inside it or by path:

```sh
cd ~/code/rider-service && sapien service bind .      # the service is inferred from origin
sapien service bind rider-service ~/code/rider-service
```

A bind checks that the checkout's origin is the service's repository and
that it carries an `api/` package (`--force` for a fork or a package you
have not written yet). The service page has a Browse button that walks
your disk from the daemon's side and marks the checkouts that match, with
their branch, last commit and how far they are from the team's ref, so
the clone you actually work in stands out from an old copy. Your agent
can do the same from an instruction through the `bind_service` tool.

That writes `sapien.workspace.local.yaml` beside the committed file
(gitignored, one per machine). From then on this machine reads rider-service
from your checkout, on the branch you have out, re-indexed on every
save, and service-scoped memories, examples and flows are written into
its `api/` directory, so they ship in your pull request like any other
change. `sapien service list` and the service page say what each service
is listening to: `local feat/x` or `team stage`. `sapien service unbind
rider-service` goes back to the team's source.

Flows have tiers. A new flow lands in `local/flows/`, this machine only,
ignored by git. Run it until it is green, then promote it:

```sh
sapien flow promote order-cancel            # local -> flows/ (the team repo)
sapien flow promote order-cancel --commit   # same, and commit the moved file
sapien flow commit order-cancel             # commit a flow already in flows/
sapien flow promote order-cancel --to service --service rider-service
```

Memories and examples climb the same ladder: a new one lands in `local/`
(this machine only) and `sapien memory move <id> --to team` (or `example
move`) puts it in the team repo; scope (`memory rescope`) is a separate
axis, saying who a memory is about. Everything at the team tier shows
whether it is not committed, committed but not pushed, or shipped, with
Commit and Push beside the badge; `sapien workspace push` is the same
push from the terminal. Sapien never pushes on its own; what reaches the
team is what you push.

The workspace repository is fetched on the same ten-minute tick as the
services, read-only. The status bar says when teammates' commits are
waiting and offers Pull when your tree is clean (fast-forward only); Sync
all does the same. `sapien workspace status` prints it in the terminal.

Onboarding a new service from a shared workspace is one command from the
checkout: `sapien service add .` commits the checkout's origin as the
team's git source and binds the checkout on your machine, so your catalog
indexes it at once and teammates get it as soon as the `api/` package
reaches the branch. `--local` keeps a path-only source for a personal
workspace.

## Optional semantic search

Sapien uses its local SQLite task, operation, and documentation indexes by
default. This needs no model and is the appropriate mode for smaller machines.
Semantic search is an optional addition to the same `search_apis` results; it
helps match paraphrases and reports `semantic` in `matched_on` when it
contributes.

To enable it with a local Ollama model:

```sh
brew install ollama                  # skip if already installed
brew services start ollama
ollama pull nomic-embed-text
```

Add this to `<workspace>/.sapien/config.yaml`:

```yaml
semantic:
  enabled: true
  kind: ollama
  base_url: http://127.0.0.1:11434
  model: nomic-embed-text
  batch_size: 8
```

Restart the workspace daemon so it loads the setting:

```sh
sapien --workspace /absolute/path/to/workspace daemon stop
sapien --workspace /absolute/path/to/workspace ui
```

The first start builds vectors in one background worker; later starts reuse
them. Authored `tasks:` phrases enrich their target operation's existing
vector rather than creating extra task vectors. Put the same configuration in
`~/.sapien/config.yaml` to make it the user default for every workspace. A
workspace can opt out with `semantic: {enabled: false}`. Removing the block or
setting `enabled: false` leaves all SQLite search behavior available and starts
no embedding worker.

## Onboarding services

Most services are not documented well enough for Sapien to index
directly, so onboarding is agent-driven: open an agent in the service's
repo and ask it to onboard the service, or to write Sapien-style API
docs for it. Because the Sapien MCP server is installed everywhere, the
agent already has Sapien's tools available no matter which repo it is
running in.

The agent writes an `api/` package next to the service's code:

```text
api/openapi.yaml   the OpenAPI contract, with an example: on every request body
api/service.yaml   name, description, owners, concepts, environments
api/docs/*.md      overview.md plus one file per domain area
api/examples/      optional saved requests, verified from real calls
api/flows/         optional flows shipped with the service
```

The docs are the point, and their reader is an agent in a *different*
repository that cannot see this code: they have to carry the business
logic -- when to call an operation, what must exist first, what it
changes, whether a retry is safe, which errors are normal -- rather than
restate the contract. Some of that is in nobody's code, so the agent asks
you one batched round of questions after drafting from the repo, and
records what nobody could answer under "Open questions" instead of
guessing.

It then calls the `add_service` tool with the repo's absolute path, which
reports how many operations the docs actually reach and how many bodies
have an example. It closes that gap, fixes any warnings Sapien returns
(or accepts a warning that describes the wire faithfully in
`api/service.yaml` with a reason, instead of silencing it), confirms with
`get_service` and `search_apis`, and adds the "Sapien" section the tool
hands back to the repo's `CLAUDE.md` or `AGENTS.md`, so the next agent
that changes the code updates `api/` in the same change. The full layout, the conventions docs must follow to link
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
npx @gs-sinha/sapien mcp --workspace .
```

or, with `sapien` installed, `sapien mcp --workspace .`. `sapien mcp
config --client <name> --write` installs the entry for the client named:
`claude-code`, `codex`, `cursor`, `cowork`, or `generic`. It defaults to
`--scope user`, so the entry it installs works from any repo on the
machine, and re-running it is safe: a second `--write` for `claude-code`
replaces the existing `sapien` entry instead of failing. Tools include
`search_apis`, `get_api` (which returns a ready-to-send request example,
not just a schema), `create_flow`, `run_flow`, `create_memory`, and
`add_service`, the tool an agent calls to register a service it just
wrote docs for. An agent new to Sapien starts with
`get_dsl_reference("sapien")`: one page on what Sapien holds and how to
use it. See [`docs/mcp.md`](docs/mcp.md) for the full tool list,
permissions, and host setup for every client.

## Telling us what got in the way

Agents hit friction that never reaches a human: a tool that returned the
wrong shape, a capability that was missing, a doc that misled them. The
`report_friction` MCP tool lets an agent file it. Nothing leaves the
machine: the report is queued under `~/.sapien/friction`, you review it
with `sapien friction list` and `show`, and `sapien friction send` posts
it as a GitHub Discussion on this repo through the `gh` CLI (which holds
your GitHub auth; Sapien stores none). Reports that look like they carry
a secret are refused when filed, because they end up public. `sapien
friction add` files one by hand; `friction.repo` and `friction.category`
in `~/.sapien/config.yaml` point sends at another repository.

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
