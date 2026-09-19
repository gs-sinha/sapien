# Sapien — Technical Implementation Plan (v0.2)

Source: `Sapien.md` (PRD). Drafted 2026-09-05; revised the same day after discussion.

How to read this: sections follow PRD §50. Each significant choice lists a recommendation, alternatives, rationale, tradeoffs, and future implications. Decision status is tracked in §37.

Decision log (2026-09-05):

- Engine language: **Go**.
- Process model: **daemon-centric hybrid**. One `Engine` interface with an in-process and an HTTP-client implementation; see §4 for the reasoning.
- Expressions: **CEL**.
- LLM: **external only**. Flows and memories are authored through MCP from Cowork, Claude Code, Codex, or any MCP host. No LLM client ships in the engine. A bring-your-own-key authoring panel in the desktop UI is deferred until MCP is proven on the success scenario.
- Focus: **engine first** (Phases 0–6). Desktop follows.
- Documentation is a first-class MCP surface, not just search input: `search_docs`, `get_doc`, `get_service`, `get_schema`, and a `docs` tier in `get_context` (§5, §14, §23).

---

## 1. Architecture summary

```
        service repos (local working tree  |  managed git clone)
        api/openapi.yaml  api/service.yaml  api/flows/  api/memories/  api/docs/
                                   │  fs watch / git fetch
                                   ▼
 ┌────────────────────────── sapien engine (library) ───────────────────────────┐
 │ registry ─► openapi ingest ─► normalized catalog ─► SQLite (catalog, FTS5,   │
 │                                                     memory index, runs)      │
 │ flow engine (CEL expressions) ─► http runtime ─► assertions ─► run store     │
 │ memory store (files + index) ─► retrieval/ranking ─► agent context builder  │
 │ environments + OS-keychain secrets                                           │
 └───────────┬──────────────────────────┬───────────────────────────┬──────────┘
             │ embedded                 │ localhost HTTP + WS       │ MCP stdio / streamable HTTP
       `sapien` CLI (one-shot)    `sapien serve` (daemon)      `sapien mcp`
             │                          │                           │
            CI                Tauri desktop (React)      Cowork / Claude Code / Codex / any MCP host
```

One binary, `sapien`, with three modes:

| Mode | Used by | Engine location |
|---|---|---|
| one-shot CLI (`sapien search …`) | humans, CI | in-process if no daemon is running for the workspace; proxied to the daemon if one is |
| `sapien serve` | desktop UI, MCP over HTTP, `--watch` clients | long-lived daemon; single SQLite writer, single file watcher, live runs |
| `sapien mcp` | agent hosts over stdio | always daemon-backed; starts the daemon if needed |

The desktop app bundles the same binary as a sidecar. Every surface (UI, CLI, MCP, CI) is a thin adapter over one `Engine` facade, which is how PRD §4.8 (parity) is enforced structurally rather than by discipline.

---

## 2. Technology choices

### 2.1 Engine language (decided: Go)

**Decision: Go.** Alternatives considered: Rust; TypeScript (Node/Bun).

| Criterion | Go | Rust | TypeScript |
|---|---|---|---|
| Cold start / idle RSS | ~5 ms / ~15 MB | ~2 ms / ~8 MB | ~80–150 ms / ~60 MB (Bun better) |
| Single static binary | yes, trivial cross-compile | yes | possible (bun compile / SEA), large |
| OpenAPI parsing | **libopenapi** (pb33f): 3.0+3.1, $ref incl. circular, keeps line numbers | no equivalent; would write own tolerant walker + resolver | good (swagger-parser, scalar) |
| Expression language | **cel-go** (reference CEL impl) | cel-interpreter (less complete) | cel-js (immature) |
| MCP SDK | official go-sdk | official rmcp | official, most mature |
| SQLite + FTS5 | modernc (pure Go) or mattn (cgo) | rusqlite bundled | better-sqlite3 |
| Desktop | Tauri sidecar (two toolchains, small shell) | Tauri native, in-process engine | Tauri/Electron |
| Local embeddings | weak (ONNX needs cgo or external process) | fastembed-rs | transformers.js |
| Iteration speed with coding agents | fast compiles, simple | slow compiles, strict | fastest |

Why Go: the two hardest V1 libraries (OpenAPI ingest with source locations, CEL) are best-in-class in Go; the engine is a daemon/CLI shape Go is built for; a single static binary satisfies the "lightweight utility" thesis; compile speed keeps agent-driven iteration tight.

Tradeoffs: desktop needs a sidecar process instead of in-process engine; semantic search will need an external embedding process or a later cgo build. Both are acceptable because PRD §13 makes semantic search optional.

Rust would be the choice if you want one toolchain and an in-process engine in Tauri, and are willing to hand-write the OpenAPI resolver. TypeScript is rejected for the engine on baseline memory and startup, but is used for the UI.

Future implications: Go engine can be embedded in CI images at ~10 MB; the local HTTP API makes the language invisible to clients.

### 2.2 Other choices

| Concern | Recommendation | Alternatives | Rationale |
|---|---|---|---|
| Storage | SQLite, WAL mode, FTS5; one DB per workspace | bbolt, Postgres, DuckDB | rebuildable index, zero infra, FTS built in, multi-process safe with WAL |
| Vectors (optional) | float32 blobs in SQLite + brute-force cosine in Go, embeddings via an external OpenAI-compatible or Ollama endpoint; hybrid via reciprocal-rank fusion | sqlite-vec (needs cgo with the pure-Go SQLite driver), in-process ONNX | keeps the binary cgo-free; brute force is fine to ~10k operations; off by default. Decided during the build (2026-09-05). |
| Expressions (decided) | CEL (`cel-go`) for `${…}` interpolation and assertions | JMESPath, custom mini-language, JS | deterministic, non-Turing-complete, typed, well specified, familiar syntax (`a.b == 1 && size(c) > 0`); PRD §47 excludes a JS runtime |
| HTTP client | net/http with explicit transport, per-env TLS options, timings via httptrace | resty | control over redirects, timeouts, cancellation |
| Local API server | net/http + chi; JSON; WebSocket for events | gRPC, Connect | trivially consumable from UI, CLI, curl, agents |
| MCP | official `modelcontextprotocol/go-sdk`; stdio + streamable HTTP | mcp-go | official, tracks spec |
| Desktop | Tauri 2 (thin Rust shell) + React + TypeScript + Vite; CodeMirror 6 for YAML; TanStack Query over engine HTTP | Wails v3, Electron | Tauri is stable with updater/bundling/sidecar; system webview keeps footprint low |
| Git | shell out to system `git` for managed clones | go-git | reuses the user's SSH agent / credential helpers; zero credential handling in V1 |
| FS watch | fsnotify + 200 ms debounce + content hashing | polling | standard |
| Secrets | OS keychain via `go-keyring` (macOS Keychain, Windows Credential Manager, libsecret) with env-var fallback for CI | encrypted file | PRD §44 |
| LLM | none in the engine; authoring agents are external MCP clients (§24) | in-engine agent loop with provider clients | keeps the engine deterministic and vendor-free (PRD §4.9, §39); the BYO-key UI panel is deferred |
| CLI | cobra + `--json` on every command | urfave | standard |
| Logging | slog, JSON file logs; optional OTLP later | zap | stdlib |

---

## 3. Repository structure

```
sapien/
  cmd/sapien/                 # main: CLI, serve, mcp
  internal/
    engine/                   # Engine facade wiring all services
    domain/                   # Service, Operation, Schema, Flow, Run, Memory, Environment types
    registry/                 # service sources (local, git), discovery, sync
    ingest/openapi/           # OpenAPI 3.x -> normalized model (libopenapi)
    catalog/                  # SQLite catalog + field index
    search/                   # FTS5 queries, ranking, optional vectors
    flow/                     # DSL parse, validate, compile
    expr/                     # CEL env, ${} templating, structured assertions -> CEL
    runtime/                  # HTTP execution, redaction, timings
    runner/                   # flow execution state machine, cancellation
    runs/                     # run persistence, retention
    memory/                   # memory files, index, subject resolution
    retrieval/                # memory ranking, context builder
    env/                      # environments, secrets, substitution
    server/                   # HTTP API + WS events + auth token
    mcp/                      # MCP tools/resources/permissions
    store/                    # sqlite open, migrations, single-writer
  spec/                       # JSON Schemas: workspace, service, flow, memory, environment
  fixtures/logistics/         # order/allocation/rider sample services + tiny mock servers
  apps/desktop/               # Phase 7: Tauri 2 shell (src-tauri/, ~200 lines Rust) + React app
  packages/engine-client/     # Phase 7: TS client generated from the engine's own OpenAPI
  docs/
```

The engine's local HTTP API is itself described by OpenAPI, so Sapien can register itself as a service (dogfooding, and agents can discover the engine API through the engine).

---

## 4. Engine architecture

**Facade.** `Engine` exposes sub-services: `Services`, `Catalog`, `Search`, `Flows`, `Runner`, `Runs`, `Memories`, `Context`, `Envs`. HTTP, CLI, and MCP call these; none contains business logic.

**Concurrency.** Goroutines per request; a single dedicated writer goroutine owns SQLite writes (channel-fed) so `SQLITE_BUSY` never surfaces; readers use a pool. Runs execute in their own goroutine tree with a `context.Context` for cancellation.

**Process model (decided: daemon-centric hybrid).** Two models were considered:

| | Daemon-only | Hybrid (chosen) |
|---|---|---|
| One-shot CLI (`sapien search`) | auto-starts a daemon, then proxies | runs the engine in-process when no daemon exists; proxies when one does |
| Cost of a one-liner | first call pays daemon start (~300 ms) and leaves a background process behind | ~20 ms, no side effects |
| CI | needs daemon lifecycle inside the job | plain process, nothing to clean up |
| Code paths | one (HTTP client) | two transports behind one interface |
| Concurrency | single writer and single watcher, always | same whenever a daemon runs; a lone CLI process is trivially single-writer |
| Tests | need a daemon per test | exercise the engine directly |

Hybrid is chosen because a developer utility must not spawn background processes as a side effect of a one-line command, and CI should be a plain process. The "two code paths" cost is contained structurally rather than by discipline: every command is written against a Go `Engine` interface; `engine.Local` implements it in-process and `engine.Remote` implements it as an HTTP client to the daemon. The CLI picks one at startup and never branches again. A round-trip test suite runs every command through both.

Rules:

- **One-shot CLI.** If `<workspace>/.sapien/daemon.json` points at a live, version-matching daemon → `Remote`. Otherwise `Local`: open the DB (WAL), run a cheap staleness check (mtime + content hash of contract, flow, and memory files), reindex what changed, answer, exit. It never starts a daemon.
- **`sapien serve`.** The daemon. Binds `127.0.0.1`, writes `daemon.json` `{pid, port, token, version, started}` with mode 0600, owns the file watcher and the single SQLite writer, idle-exits after 30 min with no clients (configurable; disabled while the desktop app is attached).
- **`sapien mcp`.** Always daemon-backed: starts the daemon if none is running, then bridges stdio to HTTP. Several hosts attached at once (Cowork, Claude Code, Codex) share one engine, one watcher, and one live-run state, and each sees the others' flows and memories immediately.
- **Desktop.** Spawns or attaches to the daemon as a sidecar.
- **One daemon per workspace.** A single multi-workspace daemon is a possible later change that does not affect clients.
- **Version mismatch** between CLI and daemon → the CLI reports it and offers `sapien serve --restart`.

**Event model.** In-process bus of typed events: `catalog.changed`, `service.sync_failed`, `run.started`, `run.step`, `run.finished`, `memory.created`, `flow.changed`. Exposed on `GET /v1/events` (WebSocket) and `sapien … --watch`.

---

## 5. Normalized API model

```go
type Service struct {
  ID, Name, Description string
  Owners, Concepts     []string
  Source               Source            // {Kind: local|git, Path|URL, Ref, Subdir}
  Environments         map[string]EnvHint // base_url per env from service.yaml
  ContractFiles        []string
  Status               SyncStatus        // ok | error(with report) | pending
}

type Operation struct {
  ID          string   // "rider-service.getRider"
  ServiceID   string
  Protocol    Protocol // Http (future: Grpc, GraphQL, Async)
  Http        *HttpBinding // {Method, Path}
  RawOpID     string   // OpenAPI operationId if present
  Summary, Description string
  Tags, Concepts       []string
  Params      []Param  // {Name, In: path|query|header|cookie, Required, Schema, Description, Example}
  RequestBody *Body    // {ContentType, Schema, Required, Examples}
  Responses   []Response // {Status "200"|"2XX"|"default", ContentType, Schema, Headers}
  Security    []SecurityRequirement
  Examples    []Example
  Deprecated  bool
  Source      SourceLoc // {File, JSONPointer, Line}
  Hash        string    // content hash for change detection
}

// Normalized JSON-Schema subset; refs resolved at ingest, component names preserved.
type Schema struct {
  Kind       SchemaKind // object|array|string|number|integer|boolean|null|oneOf|anyOf|allOf|ref
  Name       string     // component name when it came from components/schemas
  Properties map[string]*Schema; Required []string; Items *Schema; Variants []*Schema
  Enum []any; Format string; Description string; Nullable bool; Example any
}
```

**Field paths** (used by memory subjects, assertions, search): `request.body.customer.id`, `request.query.limit`, `request.path.riderId`, `response.200.body.rider.qcomSkill`, `response.*.body.error.code`. Arrays use `[]`: `response.200.body.items[].riderId`.

**Operation ID rules.** `<service-name>.<operationId>`. Missing operationId → synthesized `<method>_<path-slug>` (e.g. `post_v1_orders`) and reported as a lint warning, because synthesized IDs are less stable. An alias table maps `METHOD /path` → ID for lookup and for memory-subject fallback resolution.

**Protocol independence.** Params/body/responses are generic; protocol-specific binding lives in `Http *HttpBinding`. A gRPC ingester would populate `Grpc *GrpcBinding` and reuse everything else.

**Field index.** Ingest flattens every schema into `(operation_id, field_path, type, description)` rows. This powers "search by field name", memory attachment to fields, and the compact contract rendering for agents.

**Documentation model.** Service docs are first-class catalog objects. Sources: `api/docs/**/*.md`, plus documentation embedded in the contract (`info.description`, tag descriptions, `externalDocs`). Each file is split into sections by heading:

```go
type Doc struct {
  ID        string     // "allocation-service/docs/allocation.md"
  ServiceID string
  Path      string     // relative to the service package; "contract#info", "contract#tag:<name>" for embedded docs
  Title     string
  Source    DocSource  // file | contract_info | contract_tag
  Hash      string
  Sections  []DocSection
}

type DocSection struct {
  ID      string     // doc ID + "#" + heading slug
  Heading string; Level int
  Body    string     // Markdown
  Refs    []DocRef   // {Kind: operation|path|schema|field|concept|service, Value}
}
```

`Refs` are extracted at ingest by matching operation IDs, `METHOD /path` mentions, path fragments, component schema names, and concept tags found in the text. Docs therefore get the same structural association as memories: "sections that mention `allocation-service.allocate`" is a join, not a search.

---

## 6. Service package specification

```
<repo>/api/
  openapi.yaml            # required (yaml or json; several files via service.yaml)
  service.yaml            # optional metadata
  docs/**/*.md            # optional narrative docs; indexed by section, linked to the operations they mention (§5)
  examples/               # optional
  flows/*.flow.yaml       # optional service-owned flows
  memories/*.md           # optional service-shared memories
```

`service.yaml` (minimal schema, all optional except `version`):

```yaml
version: 1
name: allocation-service          # default: slug of openapi info.title
description: Finds eligible riders and creates allocations.
owners: [allocation-platform]
concepts: [rider allocation, dispatch, matching]
contracts: [openapi.yaml]         # default
environments:                     # non-secret only
  local:   { base_url: http://localhost:8080 }
  staging: { base_url: https://allocation.staging.internal }
```

Discovery order on `sapien service add <path>`: `api/`, then `openapi.{yaml,yml,json}` in repo root, `docs/`, `spec/`, `openapi/`. `--contract <path>` overrides. Nothing else in the repo is read.

---

## 7. Workspace specification

```
~/work/logistics/
  sapien.workspace.yaml     # committable
  environments/*.yaml       # committable, non-secret
  flows/*.flow.yaml         # workspace flows
  memories/*.md             # workspace-shared memories
  .sapien/                  # gitignored: sapien.db, repos cache link, logs, daemon.json
```

```yaml
# sapien.workspace.yaml
version: 1
name: logistics
services:
  - name: order-service
    source: { type: local, path: ../order-service }
  - name: allocation-service
    source: { type: git, url: git@github.com:company/allocation-service.git, ref: main, subdir: api }
  - name: rider-service
    source: { type: local, path: ~/code/rider-service }
default_environment: local
```

```yaml
# environments/staging.yaml
version: 1
name: staging
production: false                 # true => execution safeguards (see §28)
services:
  order-service:      { base_url: https://orders.staging.internal }
  allocation-service: { base_url: https://alloc.staging.internal }
vars:
  TEST_CUSTOMER: cust_123
auth:
  default: { type: bearer, token: "${secret.STAGING_TOKEN}" }
  rider-service: { type: header, name: X-Api-Key, value: "${secret.RIDER_KEY}" }
```

`base_url` precedence: environment file → `service.yaml` environments → OpenAPI `servers[0]`.

---

## 7b. Team workspaces: one committed composition, per-machine bindings, tiers (decided 2026-09-14)

Two propagation paths already existed and each was half of what a team needs: a local source is watched, so a change on disk is in the catalog within a second; a git source is fetched on a timer, so a merge to the pinned branch reaches every teammate within ten minutes. The `team` workspace of 2026-09-06 (every service a git source, the workspace itself a git repo a new hire clones) failed because every service was forced to be one or the other for everyone, and because the managed clone a git source is read from is a cache the daemon `reset --hard`s on every tick: a doc promotion written into it was reverted, a memory written into it was stranded where nothing pushes from, and a colleague's uncommitted knowledge was invisible by construction.

The model: the team commits the composition, each machine binds the services it is working on.

```
<workspace>/
  sapien.workspace.yaml         committed: every service, as a git source
  sapien.workspace.local.yaml   gitignored: this machine's bindings
  .gitignore                    carries sapien.workspace.local.yaml (Init writes it; bind ensures it)
  flows/  memories/  examples/  workspace tier: the team's, versioned with this repo
  local/                        this machine's tier; self-ignoring (local/.gitignore = "*")
    flows/  memories/
  .sapien/                      state; self-ignoring
```

```yaml
# sapien.workspace.local.yaml
version: 1
services:
  rider-service:
    path: ~/code/rider-service        # read rider-service from here instead of the committed git source
```

- **Binding.** `workspace.Load` applies the local file: the bound service's `Source` becomes the checkout (`ServiceRef.Team` keeps the committed source) so every path -- builder, watcher, syncer, locators -- treats it as a local source, and `workspace.Save` writes `Team` back so an override never leaks into the committed file. The git timer skips it, the file watcher covers it. `sapien service bind <name> <path>` / `unbind`, `PUT|DELETE /v1/services/{name}/binding`, and the service page's Source panel do this; `GET .../binding` also lists candidate checkouts (local sources of any registered workspace whose `origin` normalizes to the same repository).
- **Read-only clones.** A service still read from its git source is read-only for service-scoped memories, examples and flows: the locators refuse with a hint to bind a checkout or use workspace scope. As insurance, `gitsrc.Manager.Sync` refuses to reset a clone with modified tracked files and names them. Nothing is ever written into `~/.sapien/repos`.
- **"Listening to" is visible.** `domain.Service.Binding` records the mode (`local` - **Binding is validated, and the picker is daemon-side.** The same repository is often cloned in several places on one machine (a backup clone, a worktree per branch), so a bind refuses a checkout whose origin names another repository or which has no API package unless forced, and every checkout is described with its last commit time, ahead/behind the team ref as of its last fetch, and whether it is a worktree, so a stale clone is obviously stale. A web page cannot learn an absolute path from a file dialog, so the Source panel browses directories through `GET /v1/services/{name}/checkouts?path=`, which annotates each git repository it meets with whether its origin is this service's. `sapien service bind <path>` alone infers the service from the origin (the repo you are standing in), and the same binding is exposed to agents as the `bind_service` / `unbind_service` / `find_checkouts` MCP tools, so a user can say "read this service from my checkout" to their agent.
- **A new hire onboards a service from their checkout.** `sapien service add <path>` in a shared workspace (one inside a git repository with a remote) commits the checkout's origin as a git source and binds the checkout on this machine (`Services().AddFromCheckout`, `POST /v1/services/from-checkout`, `add_service` unless `local`). Their catalog indexes from the working copy at once, before anything is pushed; teammates see the service in error state until the `api/` package reaches the branch, then their next tick picks it up. Committing an absolute local path into the shared file, which is what the plain add would have done, breaks for everyone else.
| `team`), the committed source, and for a checkout its branch, commit, origin and count of uncommitted files, from read-only git queries. Sapien still never fetches, checks out or commits in a developer's repository.
- **Tiers.** Flows: `local` (`local/flows`, this machine, the default for a new flow) -> `workspace` (`flows/`, the team repo) -> `service` (`api/flows` of a bound service). `Flows().Rescope` moves the file, keeping its name, and reindexes both owners; a `scope: flow` memory follows its flow (`local/memories` for a local flow). Memories and examples climb the same ladder: a workspace-scope memory or example lives in the local tier (`local/memories`, `local/examples`, the default for a new one) until it is moved to the workspace tier (`memories/`, `examples/`), and service scope stays in the owning repo; personal memories have no file and no tier. Every workspace-tier file, flow, memory or example, carries a shipping state and can be committed on its own. Promotion is explicit: `sapien flow promote`, `sapien memory move`, `sapien example move`, the rescope tools with `tier`, and the pages.
- **Promotion is a move, and says so.** Promoting a flow to the workspace tier moves the file into `flows/`; every workspace-tier flow then carries a shipping state from a read-only look at the workspace repository (`untracked`, `modified`, `unpushed`, `shipped`), shown on the flows page, in `flow list` and in `list_flows`, so a moved file nobody committed is visible rather than silently local. Opt-in, a promotion can also commit the moved file in the workspace repository (`--commit`, the "and commit" checkbox, `rescope_flow` with `commit`): one `git add` and one `git commit` of that file, never a push, and never in a service repository, which is the developer's. A flow already at the team tier is committed on its own with `sapien flow commit`, `POST /v1/flows/{id}/commit`, `commit_flow`, or the Commit button beside its badge; demoting and re-promoting to reach the checkbox was the first cut's workaround and is not the workflow. The same Commit exists for memories and examples. And the workspace repository can be pushed from the product -- `sapien workspace push`, `POST /v1/workspace/repo/push`, the Push button beside any "not pushed" badge and in the status bar -- because a flow stuck at "committed, not pushed" that could only be pushed from a terminal defeated the point of showing the state. Push is the branch's unpushed commits to its upstream, never forced, refused when the branch is behind, and only ever the workspace repository; service repositories are the developer's and stay untouched. Sapien still never pushes on its own.
- **Contribution rides the developer's pull request.** Knowledge agents consume needs the same review gate as code; one agent's wrong "invariant" pushed straight to a shared branch would poison every agent on the team. So Sapien never commits or pushes: a promoted flow is an uncommitted file in the workspace repo, a service memory is an uncommitted file on the developer's branch, and the developer ships both the way they ship code. The workspace repository itself is fetched on the same git tick, read-only: `Repo().Status` reports branch, behind/ahead of the upstream as of that fetch, and uncommitted files; the status bar shows "N new" with a Pull button that appears only when the tree is clean, since a pull is `merge --ff-only` and the developer's uncommitted work is theirs. "Sync all" is an explicit request, so it also syncs the repository: fetch, then pull when clean and behind, otherwise say why not. Never a push.

---

## 8. Flow DSL

```yaml
version: 1
id: order-allocation                # default: filename stem
name: Order allocation
description: Create an order, allocate, verify rider online.
tags: [allocation, smoke]

inputs:
  customerId: { type: string, default: "${env.TEST_CUSTOMER}" }
  city:       { type: string, required: true }

steps:
  - id: create
    call: order-service.createOrder
    input:                          # bound by name to path/query/header params
      city: "${inputs.city}"
    body:
      customerId: "${inputs.customerId}"
      type: QCOM
    extract:
      orderId: body.orderId
    assert:
      - status == 201

  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    until: status == 200 && body.riderId != null     # optional polling
    poll: { interval: 1s, timeout: 30s }
    assert:
      - latency_ms < 2000

  - id: rider
    call: rider-service.getRider
    input: { riderId: "${steps.allocate.body.riderId}" }
    assert:
      - status == 200
      - body.online == true
      - { schema: contract }        # validate body against the 200 response schema
      - { path: body.qcomSkill, eq: true, message: "QCOM riders must have qcomSkill" }
```

Design points:

- **Expressions.** `${…}` inside strings interpolates a CEL expression; a value that is exactly one `${…}` keeps its type. `assert`, `extract`, and `until` are bare CEL. Structured assertions (`schema`, `path/eq/exists/matches/contains`, `latency_ms`) compile to CEL or the schema validator so there is one execution path.
- **Scope names.** `inputs`, `env` (vars), `steps.<id>.{request, status, headers, body, latency_ms, out}`, and inside a step's own `assert/extract/until`: `status`, `headers`, `body`, `latency_ms`, `request`. `secret.X` is allowed only in `headers`, `auth`, and environment `auth`, is substituted engine-side, and never appears in runs.
- **Input binding (proceeding as proposed).** Flat `input:` bound by name against the contract's path/query/header params, plus explicit `body:`; explicit `params: {path:, query:, headers:}` is accepted for name collisions. The flat form is what the PRD example uses and what an LLM writes most reliably.
- **Polling (proceeding as proposed).** `until` + `poll` is not in PRD §16's V1 list, but PRD §3.5 and §19 ("wait for timeline generation") make it necessary for the success scenario in practice. Recommend including it; it is ~1 day of work.
- **Validation** (`sapien flow validate`, also run before every execution): operation IDs exist; bound input names exist on the operation; required params provided; expressions parse and type-check against known shapes where schema is available; step references are to earlier steps; extract names unique; unknown keys rejected with source line.
- **Reserved for future** (rejected by validator today, keys parked): `when:` (condition), `parallel:` groups, `foreach:`, `retry:`, `use:` (subflow), `setup:/teardown:`, `datasets:`. The step list stays an ordered list so a DAG can later be expressed with `needs:`.

---

## 9. Flow execution state machine

```
Run:   queued ─► running ─► passed | failed | errored | cancelled
Step:  pending ─► resolving ─► requesting ─► (polling)* ─► asserting ─► passed | failed | errored | skipped | cancelled
```

- `failed` = an assertion or `until` timeout; `errored` = resolution, transport, or engine error. Distinguished everywhere (UI, CLI exit code 1 vs 2, MCP).
- Default policy: stop at first failed/errored step; remaining steps `skipped`. `--continue-on-failure` flag for suites.
- At start, the run persists a snapshot: flow source, resolved inputs, environment name, redacted variable set, catalog hashes of the operations used. Steps append as they complete, so a crashed run is still inspectable.
- Cancellation cancels the in-flight HTTP request via context and marks the run `cancelled`.
- Each step records request (after secret substitution then redaction), response headers/body (size-capped, truncated flag), timings (dns, connect, tls, ttfb, total), assertion results with the evaluated left-hand value for diagnostics, and any extracted outputs.

---

## 10. Memory domain model

A memory is a Markdown file with YAML front matter (agent- and diff-friendly; one file per memory so parallel additions never conflict).

```markdown
# memories/01J8Z5K3W2RQ4X7M9N.md          (ULID)
---
id: mem_01J8Z5K3W2RQ4X7M9N
type: semantic          # semantic | behavioral | testing | invariant | environment | gotcha | note (default)
scope: workspace        # personal | workspace | service | flow
subject:
  operation: rider-service.getRider
  field: response.200.body.qcomSkill
tags: [qcom, allocation]
source: { kind: user }  # user | agent{model,client} | run{run_id,step_id} | import{path} | documentation{file}
created: 2026-09-05T10:20:00Z
updated: 2026-09-05T10:20:00Z
status: active          # future: superseded | deprecated | disputed
---
qcomSkill indicates whether the rider is eligible for quick-commerce (QCOM) orders.
It does not indicate the rider is online or available.

Implications: QCOM allocation should only select riders with qcomSkill=true.
```

- Only `id`, `scope`, `created`, and the body are required. `type` defaults to `note`. An "Implications:" paragraph is a convention, not schema, in V1 (PRD §18: don't over-structure).
- Capture from the CLI is one line: `sapien memory add --op rider-service.getRider --field response.200.body.qcomSkill "…"`. Capture from the UI is select-field → Remember → type text; all subject fields are attached automatically.

## 11. Memory subject / reference model

Subjects are a small set of typed references; a memory may carry several.

| Key | Value | Resolution fallback if it stops resolving |
|---|---|---|
| `service` | service name | none |
| `operation` | operation ID | alias table (`METHOD /path` recorded at save time) → same service, same raw operationId |
| `field` | field path (needs `operation` or `schema`) | same field name elsewhere in the same operation |
| `schema` | `service.ComponentName` | none |
| `flow` / `step` | flow ID / flow ID + step ID | none |
| `run` | run ID | run may be purged; keep reference as provenance |
| `environment` | env name | none |
| `error` | `{operation, status, code}` | operation fallback |
| `concept` | free string | lexical only |

Every saved subject also stores a `resolved_at` snapshot (method, path, operation hash). Unresolvable subjects are flagged `unresolved` in UI and CLI; files are never rewritten automatically. This is how references survive reasonable OpenAPI changes without a rename-tracking system.

## 12. Memory persistence strategy (proceeding as proposed)

| Scope | Canonical store | Index |
|---|---|---|
| personal | SQLite only (`memories` table, `file_path NULL`) | same table |
| workspace | `<workspace>/memories/*.md` | SQLite index rebuilt from files |
| service | `<service>/api/memories/*.md`, written only on explicit "share with service" | SQLite index |
| flow | stored as workspace or service memory with `subject.flow` set; `scope: flow` only narrows visibility | SQLite index |

Rationale: shared knowledge must be git-reviewable and travel with the code (PRD §4.2, §27); personal notes should be zero-friction and private. The DB is always rebuildable from files plus the personal table. Writing into a service repo's working tree is not "modifying git state" (no commits, no checkout), but it is visible to the developer, so it is behind an explicit action with a confirmation.

Alternative considered: DB-only in V1 with export. Rejected because the promotion lifecycle (§28 in PRD) needs files from day one, and the file format is the cheapest part.

## 13. Memory retrieval and ranking

Two entry points on `Memories`:

- `Relevant(subjects []Subject, opts)` — structural first; used by the context builder and MCP `get_relevant_memories`.
- `Search(query string, ctx *Context, opts)` — lexical (+ optional semantic) with structural boosts; used by UI/CLI/MCP search.

Score = `w_s·structural + w_l·bm25_norm + w_v·cosine + w_p·provenance + w_r·recency`, computed over the union of candidates from each retriever.

| Structural tier | score |
|---|---|
| exact `operation`+`field` match | 1.0 |
| `operation` match | 0.8 |
| memory on a `schema` used by the operation | 0.7 |
| memory on a `flow` using the operation | 0.5 |
| `service` match | 0.4 |
| `concept`/tag overlap with operation tags or query terms | 0.3–0.5 |

Provenance weights: user/documentation 1.0, run-derived 0.9, agent 0.7. Recency is a tiebreak only. Default weights `w_s=1.0, w_l=0.6, w_v=0.4, w_p=0.2, w_r=0.05`, exposed in config for tuning; explicit structural matches outrank semantic similarity by construction (PRD §25).

## 14. Agent context builder

Input: `{intent, operations?, flow?, environment?, budget_tokens (default 8k)}`. Output: `ContextBundle` JSON.

1. If `operations` empty: `Search.Operations(intent, k=8)`; keep results above a relevance floor.
2. For each operation: compact contract rendering (params, body fields as `path: type — description`, primary response fields, security). Examples included only if budget allows.
3. Docs: sections whose `Refs` hit a selected operation, its schemas, or its service's concept tags, plus lexical hits on the intent; top 6, ordered by structural tier then BM25. Rendered as `{service, path, heading, body}` with the body budget-truncated and a resource URI for the rest.
4. `Memories.Relevant(subjects = ops ∪ their services ∪ their schemas ∪ concept terms from intent)`, top 15.
5. Flows that use ≥1 selected operation (id, name, step list only).
6. Optional: last 3 runs touching those operations, as one-line outcomes (status, failed assertion).
7. Budget enforcement in tiers: drop run evidence → drop examples → trim doc bodies to their first paragraph → truncate descriptions → prune response schemas to depth 2 → reduce operations.
8. Emit `ContextBundle { operations[], docs[], memories[], flows[], runs[], omitted: {...counts} }` so the consumer knows what was cut.

Every item carries `tier: contract | documentation | memory | run`, and memories also carry `scope` and `source`. This keeps the PRD §43 hierarchy visible to an external agent, which can weigh a canonical doc sentence above a personal note without the engine editorializing.

Exposed as `POST /v1/context` and MCP `get_context`. Vendor-neutral by construction: it returns data, not prompts.

## 15. Persistence schema (SQLite, per workspace)

```
services(id PK, name UNIQUE, source_json, status, last_indexed, error_report)
contract_files(service_id, path, hash, last_parsed)
operations(id PK, service_id, protocol, method, path, raw_op_id, summary, description, tags_json, deprecated, source_file, source_pointer, source_line, hash, doc_json)
operation_aliases(method, path, operation_id)                 -- for METHOD /path lookup and subject fallback
schemas(service_id, name, hash, doc_json, PK(service_id,name))
fields(operation_id, field_path, type, description, PK(operation_id, field_path))
operations_fts(id UNINDEXED, service, op_id, path_tokens, summary, description, tags, param_names, field_names)  -- FTS5, bm25 weights per column
docs(id PK, service_id, path, title, source, hash, updated)
doc_sections(id PK, doc_id, ord, heading, level, body)
doc_refs(section_id, kind, value)                               -- operation | path | schema | field | concept | service
docs_fts(section_id UNINDEXED, service, title, heading, body)   -- FTS5
flows(id PK, owner_kind, owner_id, path, name, tags_json, ops_json, hash, updated)   -- ops_json: operations referenced
memories(id PK, scope, type, file_path NULL, subject_json, tags_json, source_json, status, body, created, updated, hash)
memory_subjects(memory_id, kind, value, resolved_json)          -- one row per subject for structural joins
memories_fts(id UNINDEXED, body, tags, subject_text)
vectors(kind, id, model, dim, content_hash, embedding BLOB, PK(kind,id))  -- optional semantic index for operations, doc sections, memories (004_vectors.sql)
runs(id PK, flow_id NULL, flow_snapshot_json, env, inputs_json, status, started, finished, duration_ms, summary_json)
run_steps(run_id, step_id, idx, status, request_json, response_json, timings_json, assertions_json, out_json, error_json, PK(run_id, step_id))
environments(name PK, production, doc_json)                     -- mirror of files for query convenience
settings(key PK, value)
schema_migrations(version)
```

Retention: runs pruned by count (default 500 per workspace) and age (30 days), pinned runs kept. Everything except `memories WHERE scope='personal'`, `runs`, `run_steps`, `settings` is rebuildable by `sapien reindex`.

## 16. Search architecture

- **Query parsing.** `^(GET|POST|…)\s+/` → structured lookup via `operation_aliases` (prefix match on path). Bare `/v1/orders` → path search. Otherwise FTS.
- **Tokenization.** Paths tokenized into segments and camel/snake splits (`/v1/riders/{riderId}` → `v1 riders rider id riderId`), stored alongside the raw path; operation IDs split the same way. FTS5 `unicode61` tokenizer plus a trigram index on `path`/`op_id` for substring hits.
- **Ranking.** BM25 with column weights (op_id 8, path 6, summary 5, tags 3, param/field names 3, description 1) + boosts: exact operationId match, method match if specified, service name match, non-deprecated. Return `{operation, score, matched_on[]}` so UI can highlight.
- **Docs.** `Search.Docs(query, service?)` runs FTS over `docs_fts` at section granularity (heading weight 4, title 3, body 1) with a boost when `doc_refs` links the section to an operation the same query matches. Results are `{service, path, heading, snippet, refs[], score}`. The same index feeds the `docs` tier of `get_context`.
- **Semantic (optional, off by default).** Embed `summary + description + field names` per operation into sqlite-vec; hybrid = reciprocal-rank fusion of FTS and vector lists. Requires an embedding endpoint (Ollama or OpenAI-compatible) configured in settings.
- **LLM rerank.** Not in V1; slot exists in the `Search` pipeline.

Targets: p95 < 20 ms at 10k operations for FTS; index rebuild < 10 s at 10k.

## 17. Filesystem synchronization

```
fsnotify event(s) on watched dirs ─► 200 ms debounce ─► hash changed files
   ─► parse + validate (per contract file) ─► diff operations by hash
   ─► single transaction: upsert/delete operations, fields, aliases, fts
   ─► reresolve memory subjects touching changed operations
   ─► emit catalog.changed {service, added, removed, changed}
```

Watched paths: each service's contract files, `api/` subtree (docs, flows, memories), workspace `flows/`, `memories/`, `environments/`. Parse errors keep the last good catalog for that service and surface `service.sync_failed` with file/line. Branch switches are just file changes, so the catalog follows the checked-out branch automatically (PRD §41).

## 18. Git synchronization

- Managed clones in `~/.sapien/repos/<sha1(url)>/`, created with `git clone --filter=blob:none --no-checkout` then sparse-checkout of `subdir` (default `api/`), checked out at `ref` (default: remote default branch).
- `sapien service sync [name]` and a daemon timer (default 10 min, per-service override) run `git fetch` + checkout; changed files flow through the same pipeline as §17.
- Credentials: system git handles SSH agent, credential helpers, and tokens. Sapien stores none.
- Pinning: `ref` may be a branch, tag, or commit. The catalog records the resolved commit per service; runs snapshot it.
- Local repos are never fetched, pulled, or checked out by Sapien (PRD §11).
- Future: `sapien api diff <ref>` reuses the ingest pipeline on two trees and diffs normalized operations.

## 19. HTTP runtime

- Request assembly: operation binding + inputs → URL (path params encoded per style), query, headers, body (JSON by default; `content_type` override; form and multipart later).
- Auth applied from environment `auth` (default or per service); operation `security` requirements are shown in UI but not enforced.
- Per-environment transport: timeouts (connect 10 s, total 30 s default), TLS verify toggle (allowed only when `production: false`), proxy, max redirects, capture caps (1 MB body default, truncated flag).
- Timings via `httptrace`. Context cancellation propagates from UI/CLI/MCP.
- Redaction pipeline before persistence: header allow/deny list (Authorization, Cookie, Set-Cookie, X-Api-Key, configurable), any value equal to a resolved secret, and configurable JSON paths per environment. Redaction happens once, at persistence; nothing unredacted leaves the runtime except the live in-memory response for assertions.

## 20. Environments and secrets

- Environment files are non-secret and committable (§7). Secrets are referenced as `${secret.NAME}` and resolved from, in order: process env `SAPIEN_SECRET_NAME` (CI), OS keychain entry `sapien/<workspace>/<NAME>`, prompt (interactive CLI/UI) with offer to store.
- `sapien secret set NAME` / `list` / `rm`; values never printed; `list` shows names only.
- Agent safety: secrets are substituted inside the runtime immediately before send; the HTTP API and MCP have no endpoint that returns a secret value; runs and memories are scanned for secret values on write.
- Production environments (`production: true`): red banner in UI, `--allow-production` required on CLI, blocked for MCP unless the client permission grants it explicitly, mutation methods additionally require confirmation in UI.

## 21. CLI specification

```
sapien init [dir]                               create workspace
sapien service add <path|git-url> [--name] [--ref] [--subdir] [--contract]
sapien service list|remove|sync [name]
sapien search <query> [--method] [--service] [--limit]        # operations
sapien describe <operation-id> [--fields] [--examples]
sapien docs list [--service]
sapien docs search <query> [--service]
sapien docs show <service> <path> [--section <heading>]
sapien call <operation-id> [--env] [-p key=val] [--body json|@file] [-H k:v] [--save-as flow]
sapien flow list|validate|show <id>
sapien flow run <id|path> [--env] [-i key=val] [--continue-on-failure] [--report junit|json] [--watch]
sapien run list|show <run-id> [--step]
sapien memory add [--op] [--field] [--service] [--flow] [--scope] [--type] "<text>"
sapien memory search <query> [--op] [--service]
sapien memory list [--op] [--service] [--scope]
sapien memory promote <id> [--write]             # locate subject in OpenAPI; template-only doc patch (§26)
sapien env list|show|use <name>
sapien secret set|list|rm <name>
sapien context "<intent>" [--budget]            # print ContextBundle
sapien reindex
sapien serve [--port] [--idle-timeout] [--restart]
sapien mcp [--http]
sapien mcp config --client claude-code|codex|cowork|generic [--write]   # host setup (§23.3)
```

Conventions: `--json` on every command; exit codes 0 ok, 1 assertion failure, 2 error, 3 blocked (production/permission); `NO_COLOR` respected; `--env` falls back to workspace default; workspace discovered by walking up for `sapien.workspace.yaml` or via `--workspace`.

## 22. Local engine HTTP API (IPC)

`http://127.0.0.1:<port>/v1`, `Authorization: Bearer <token>` from `daemon.json`, `Host` header must be localhost (DNS-rebinding guard), CORS disabled except for the Tauri origin.

```
GET  /services                     POST /services            DELETE /services/{id}    POST /services/{id}/sync
GET  /services/{id}                 # metadata, doc list, op count
GET  /operations?q=&method=&service=   GET /operations/{id}  GET /operations/{id}/fields
GET  /docs?q=&service=              GET /docs/{service}/{path}?section=
GET  /schemas/{service}/{name}
POST /call                          {operation_id, env, params, body, headers}  → run (one-step)
GET  /flows                         GET /flows/{id}           PUT /flows/{id}          POST /flows/validate
POST /flows/{id}/run                {env, inputs}             → {run_id}   POST /runs/{id}/cancel
GET  /runs?flow=&status=            GET /runs/{id}            GET /runs/{id}/steps/{step}
GET  /memories?q=&op=&service=&scope=   POST /memories   GET/PATCH/DELETE /memories/{id}
POST /memories/relevant             {subjects[]}
POST /context                       {intent, operations?, flow?, env?, budget}
GET  /environments                  GET /environments/{name}
GET  /events                        WebSocket
GET  /health  GET /openapi.json
```

The desktop UI uses only this API (via the generated TS client), which guarantees that anything the UI can do the CLI/MCP can do.

## 23. MCP specification

The MCP server is the primary authoring surface: Cowork, Claude Code, Codex, and any other MCP host author flows and memories through it, and the engine ships no LLM. That makes the tool design and the validator's diagnostics the product's natural-language layer, so both get first-class attention.

Transport: stdio (`sapien mcp`, daemon-backed, §4) for Claude Code, Codex, and Cowork; streamable HTTP on the daemon for hosts that prefer a URL.

Server `instructions` (sent at initialize) describe the intended loop so hosts need no custom system prompt:

> Start with `get_context(intent)`. Discover with `search_apis`, inspect with `get_api`, read the service's own documentation with `search_docs` and `get_doc`, read `get_relevant_memories` for the operations you will use, check `list_flows` for existing flows, read `get_dsl_reference("flow")` once per session, then `validate_flow` until clean and `create_flow`. Never invent operation IDs or fields; use only IDs returned by tools. Turn memories of type `invariant` or `testing` attached to a selected operation into assertions or `until` steps. Do not execute flows or endpoints unless the user asked. To onboard a service or write Sapien-style docs: read `get_dsl_reference("service")`, create the `api/` package in the repo from its real code, then `add_service` with the absolute repo path, fix the warnings it returns, and paste the `CLAUDE.md`/`AGENTS.md` section it suggests into the repo so future code changes keep `api/` current.

Tools (compact outputs, progressive disclosure):

| Tool | Permission class | Notes |
|---|---|---|
| `list_services()` | read_contracts | names, descriptions, op counts |
| `get_service(name)` | read_contracts | description, owners, concepts, environment base URLs, doc list, op count |
| `search_docs(query, service?, limit=10)` | read_contracts | doc sections: service, path, heading, snippet, refs, score |
| `get_doc(service, path, section?)` | read_contracts | full Markdown of a doc or one section; contract-embedded docs are addressable as `contract#info` and `contract#tag:<name>` |
| `get_schema(service, name)` | read_contracts | a named component schema as flattened fields with descriptions, plus the operations that use it |
| `search_apis(query, service?, method?, limit=10)` | read_contracts | id, method, path, summary, score, matched_on |
| `get_api(id, detail=summary\|fields\|full)` | read_contracts | `summary` ≈ 300 tokens; `fields` adds the flattened field list with types and descriptions; `full` adds schemas and examples |
| `get_dsl_reference(topic=flow\|memory\|expressions\|service)` | none | DSL reference with worked examples, ≈ 1.5k tokens; `service` is the api/ package layout and onboarding checklist (`internal/engine/local/reference_service.md`); hosts with resource support can read `sapien://reference/*` instead |
| `add_service(path\|url, name?, ref?, subdir?, contract?)` | write_services | registers and indexes a service, exactly like `sapien service add`; `path` must be absolute (the server does not share the agent's cwd); returns op count, sync status, unaccepted warnings, accepted-warning count, and the CLAUDE.md/AGENTS.md section to paste; an already-registered service is re-synced |
| `sync_service(name?)` | write_services | re-reads and reindexes one or every service |
| `get_context(intent, operations?, budget)` | read_* | the context builder (§14); the recommended first call for any authoring task |
| `execute_api(id?, env, params?, body?, headers?, example?)` | execute_read for GET/HEAD/OPTIONS, execute_mutation otherwise; env must be allowed | redacted status/headers/body (capped) + run_id; `example` supplies input/body/headers, explicit fields override |
| `list_flows(query?)` / `get_flow(id)` | read_flows | |
| `validate_flow(flow_yaml)` | read_contracts | diagnostics with line numbers and suggestions (§23.1) |
| `create_flow(flow_yaml, path?)` / `update_flow(id, flow_yaml)` | write_flows | validated first; rejected with diagnostics if invalid |
| `run_flow(id, env, inputs?)` | execute_* by the highest-risk method used; env allowed | summary + per-step status; details via `get_run`; on failure, `hints` (internal/diagnose): the contract's description of that status, then docs and memories matching the error tokens in the body |
| `get_run(id, step?, include_bodies=false)` | read_runs | hints on failure as above |
| `search_memories(query, operation?, service?, limit)` | read_memories | |
| `get_relevant_memories(subjects[])` | read_memories | structural retrieval (§13) |
| `create_memory(text, subject?, type?, scope=workspace, tags?)` | write_memories | source recorded as `agent{client}`; the tool description states the scope rule (scope is storage and sharing, not subject; "true in a fresh environment?" → service); the result reports where it was stored, similar memories, and the promotion path |
| `rescope_memory(id, scope, service?)` | write_memories | moves a memory between scopes (file relocated) |
| `list_examples(operation?, service?, tag?, text?, limit=20)` / `get_example(id)` | read_contracts | saved, verified request examples (§34b) |
| `create_example(id, run_id?+step_id? \| operation+input/body/headers, scope=workspace, description?, tags?)` / `rescope_example(id, scope)` / `delete_example(id)` | write_examples | `run_id` saves a verified example from a recorded run; hand-written ones carry no `verified` |
| `get_promotion_target(memory_id)` | read_contracts, read_memories | file, line, JSON pointer, current description, memory text; the agent edits the OpenAPI file itself (§26) |

Resources: `sapien://services/{name}`, `sapien://services/{name}/docs/{path}`, `sapien://operations/{id}`, `sapien://schemas/{service}/{name}`, `sapien://flows/{id}`, `sapien://memories/{id}`. Sapien's own reference material lives apart at `sapien://reference/flow-dsl`, `sapien://reference/memory`, `sapien://reference/expressions`, `sapien://reference/service`, `sapien://reference/flow.schema.json`, so service documentation is never confused with tool documentation.

### 23.1 Validator diagnostics carry the repair loop

Because the model is external, `validate_flow` is where repair happens. Every diagnostic has `code`, `line`, `message`, and where possible `suggestions[]`:

- unknown operation → nearest IDs by fuzzy match and the alias table ("did you mean `rider-service.getRider`")
- unknown input name → the operation's actual parameter names and locations
- unknown field in an expression → nearest field paths from the field index for that step's response
- reference to a later step → the step order
- missing required parameter or body field → the requirement with its type
- expression parse or type error → the CEL message rewritten in DSL terms

The CLI and UI show the same diagnostics.

### 23.2 Permissions

Permissions live in `<workspace>/.sapien/mcp.yaml` with a global default in `~/.sapien/config.yaml`: per client name → classes granted, allowed environments, production allowed (default false). Default profile: read everything, execute_read on non-production environments, write_memories, write_flows, write_services, write_examples, no execute_mutation. Denied calls return `E_PERMISSION_DENIED` naming the missing class and the config key to change, so the agent can relay it to the user.

### 23.3 Host setup

`sapien mcp config --client claude-code|codex|cursor|cowork|generic [--scope user|local|project] [--write]` prints, or with `--write` installs, the host's MCP entry for `sapien mcp --workspace <dir>`. The entry bakes in the absolute workspace path and the absolute path of the running binary, so for Claude Code it defaults to `--scope user`: one install makes Sapien available to the agent in every repo on the machine, which is what lets an agent onboard a service from inside that service's own repo. `--write` is idempotent for claude-code (an existing `sapien` entry in that scope is replaced) and merges into `~/.cursor/mcp.json` for cursor. All hosts share one daemon per workspace, so a flow created from Cowork is visible to Claude Code and Codex immediately, and vice versa.

Context efficiency: no tool returns more than `limit` items; schemas render as flattened field lines, not raw JSON Schema; response bodies are capped at 16 KB in tool results with a pointer to `get_run` for more.

## 24. Natural-language flow generation and editing (decided: external agents via MCP)

Natural-language authoring is done by whichever agent the developer already uses, through the MCP tools in §23. The engine contributes four things:

1. **Grounding.** `search_apis`, `get_api`, and `get_context` return only real operations, fields, and memories.
2. **Teaching.** `get_dsl_reference` and the server `instructions` give the agent the DSL and the loop.
3. **Checking.** `validate_flow` with suggestions makes the repair loop converge in one or two rounds.
4. **Persistence.** `create_flow` and `update_flow` write ordinary files the developer reviews in their editor or in a PR.

Editing is `get_flow` → agent edits the YAML → `validate_flow` → `update_flow`. Coding agents that prefer to edit files directly may do so; the file watcher picks up the change and the same validator reports problems in the CLI and UI.

Guardrails: the validator rejects any reference not in the catalog; execution is a separate, permissioned tool call; agent-created flows and memories are attributed (`source: agent{client}`).

Deferred: an in-app authoring panel with a bring-your-own-key setting. It would run the same tool loop inside the desktop app and is scheduled (Phase 8) only after MCP has been exercised by real agents on the success scenario. No LLM code is planned in the engine binary.

## 25. Memory-aware flow generation

Falls out of §14 and §23. `get_context` returns memories grouped by the operation they attach to, with `type` and `subject.field`, and the server `instructions` tell the agent to turn `invariant` and `testing` memories into assertions or `until` steps. Because the agent is external, the observable success metric for PRD §31 and §48 is: after the `qcomSkill` memory is saved, an agent asked for "a QCOM allocation test" produces a flow with `body.qcomSkill == true` asserted on the rider step. This is the Phase 4 acceptance test, run with Claude Code and at least one other host.

## 26. Memory → documentation and memory → test promotion

Both are agent workflows over engine primitives; neither needs an LLM in the engine.

- **Promote to documentation.** `get_promotion_target(memory_id)` (MCP) and `sapien memory promote <id>` (CLI) return a target. For memories on a field or operation: the OpenAPI file, line, and JSON pointer plus the current description. For behavioral, testing, and invariant memories that read better as prose: the best-matching section under `api/docs/` (by refs and lexical overlap) or a proposed new file. The agent or developer edits the file in the service repo and opens a PR through the normal workflow. The CLI can emit a template-only patch; `--write` applies it to a *local* service working tree, never a managed clone. The developer marks the memory `status: promoted`. Sapien never commits.
- **Turn into test.** The agent calls `get_context(intent = memory text, operations = memory subjects)` and proceeds through the authoring loop in §24. No separate tool is needed.

## 27. Desktop architecture and engine/UI IPC (Phase 7)

- Tauri 2 shell (~200 lines Rust): create window, spawn `sapien serve --workspace <dir> --port 0` as sidecar, read port/token from its stdout handshake, inject them into the webview, kill sidecar on exit. No Tauri commands beyond `pick_directory`, `open_external`, `reveal_in_finder`.
- React + TypeScript + Vite, TanStack Query over the generated client, WebSocket `/events` for live updates, CodeMirror 6 for YAML (flow editor "code" tab), Radix primitives for accessible components, Tailwind for styling. No Monaco, no Electron.
- Navigation per PRD §33: Workspace → Flows (home) / Services / Environments / Runs. Knowledge appears as a panel on Endpoint and Flow views; a global search box searches operations, flows, and memories together.
- Flow editor: structured sequential editor (step cards with call picker, input form generated from the contract, assertion list) with a synchronized YAML tab; NL box ("Describe a change…") produces a diff preview.
- Response viewer: JSON tree with click-to-select path → "Remember" (pre-filled subject) and "Assert" (adds structured assertion to the step).
- Memory budget: idle target < 150 MB total including webview.

## 28. Security model

Threats and controls:

| Threat | Control |
|---|---|
| Local process or browser tab calls engine API | bind 127.0.0.1, bearer token from `daemon.json` (0600), Host check, no CORS |
| Agent executes mutations or hits production | MCP permission classes, per-client env allowlist, production default-deny, UI confirmation for mutations on production |
| Secrets persisted in files/runs/memories | keychain storage, engine-side substitution, redaction at persistence, secret-value scan on memory/flow save with warning |
| Sensitive response data in run history | header/JSON-path redaction per environment, body caps, retention, `sapien run purge` |
| Sensitive memory shared to a repo | sharing to service scope is explicit + confirmed; scan for secret-like patterns |
| Malicious OpenAPI file (huge, ref bombs) | size caps, ref depth caps, parse timeouts, per-service failure isolation |
| Managed clone credentials | none stored; system git |
| Prompt injection via memories or contracts reaching an external agent | tool outputs are data; the validator constrains references; execution and writes are separate permissioned calls; agent-authored artifacts are attributed |

## 29. Error model

Structured error `{code, message, details, source?: {file, line}, hint?}` shared by HTTP (status mapped), CLI (stderr + exit code, `--json` emits the object), and MCP (`isError` with the same object). Codes: `E_WORKSPACE_NOT_FOUND`, `E_SERVICE_SOURCE`, `E_CONTRACT_PARSE`, `E_OPERATION_NOT_FOUND`, `E_FLOW_INVALID`, `E_EXPR`, `E_INPUT_MISSING`, `E_SECRET_MISSING`, `E_HTTP_TRANSPORT`, `E_ASSERTION_FAILED`, `E_UNTIL_TIMEOUT`, `E_CANCELLED`, `E_PERMISSION_DENIED`, `E_PRODUCTION_BLOCKED`, `E_INTERNAL`.

## 30. Observability

`slog` JSON logs in `<workspace>/.sapien/logs/` with rotation; `--verbose`; `GET /debug/stats` (index sizes, timings, watcher state); per-run timings already in run records; optional OTLP exporter behind a setting (not V1). No usage telemetry by default.

## 31. Testing strategy

- Unit: ingest (golden tests on fixtures: petstore, the three logistics specs, a set of messy real-world specs from APIs.guru, 3.0 and 3.1), expression layer, flow validator, ranking, redaction.
- Integration: flow runner against an in-process `httptest` server scripted per test; runner state machine tests including cancellation and polling; SQLite migrations up/down.
- CLI: golden output tests (`--json`), exit codes.
- MCP: conformance via the SDK client; permission matrix table-driven.
- Authoring via MCP: a scripted MCP client replays the PRD §48 tool sequence deterministically; a manual live check with Claude Code and Codex at each phase gate.
- Desktop: Playwright smoke run against the built app driving the success scenario end to end using the fixture mock servers.
- Success-scenario test: `fixtures/logistics` mock servers + a script that walks PRD §48 through CLI and MCP; this is the acceptance test for every phase.

## 32. Performance benchmark plan

| Metric | Target | Method |
|---|---|---|
| CLI cold start (`sapien search`) | < 50 ms | hyperfine |
| Daemon start to ready | < 300 ms | timestamped log |
| Idle RSS: daemon / desktop total | < 40 MB / < 150 MB | ps sampling over 10 min |
| Idle CPU | < 0.5% | same |
| Full index 1k / 10k operations | < 1 s / < 10 s | generated specs (go test bench) |
| Incremental reindex of one file | < 200 ms | bench |
| Operation search p95 at 10k | < 20 ms | bench |
| Memory retrieval p95 (10k memories) | < 30 ms | bench |
| Per-step engine overhead | < 5 ms | runner bench with no-op server |

Benchmarks run in CI on every PR with regression thresholds.

## 33. Packaging and distribution

- Engine: GoReleaser → GitHub Releases (darwin/linux/windows, arm64/amd64), Homebrew tap, `curl | sh` installer, Docker image for CI, and a tiny npm wrapper `npx sapien mcp` because many MCP hosts assume npx.
- Desktop: Tauri bundles (dmg, msi, AppImage/deb), signed and notarized, Tauri updater fed from GitHub Releases. The desktop bundle pins the matching engine binary.
- Version compatibility: engine reports `api_version`; UI and CLI refuse mismatched majors.

## 34. Phased roadmap

Ordering matters more than dates; rough sizes assume one engineer working with coding agents. Phases 0–6 are engine-only and produce a releasable product for agent-driven use. The thesis is provable at the end of Phase 4.

Build note (2026-09-05): Phases 0–6 were built in one night by parallel coding agents working on disjoint packages; every exit criterion through Phase 5 was verified on the built binary and the §48 scenario runs as an automated acceptance test over MCP. Phase 6's release pipeline is written but not yet exercised against a tag. `docs/BUILD-LOG.md` records per-phase coverage, deviations, and open issues; where it disagrees with this plan, the log reflects what was actually built.

| Phase | Deliverable | Exit criterion |
|---|---|---|
| 0. Foundations (≈1 wk) | repo skeleton, JSON Schemas in `spec/`, fixture services + mock servers, CI, benchmark harness | `go test` green, fixtures serve |
| 1. Catalog (≈2 wk) | `service add` (local), OpenAPI ingest, normalized model, docs ingest by section with ref extraction, SQLite, FTS search for operations and docs, `describe`, `docs`, fs watch, `reindex` | search "allocate rider" finds the right op across 3 services; 10k-op bench passes |
| 2. Execution (≈2 wk) | environments, keychain secrets, `call`, flow DSL + CEL, validator with suggestions, sequential runner with polling, assertions, runs, redaction, JUnit report | success scenario steps 1–4 via CLI; CI-runnable |
| 3. Memory (≈2 wk) | memory files + index, subject model, `memory add/search/list`, ranking, context builder, `sapien context` | "Create a QCOM allocation test" context bundle contains the qcomSkill memory |
| 4. Daemon + MCP (≈2 wk) | `serve`, HTTP API, events, `Engine` local/remote split, `mcp` with permissions, doc and schema tools, DSL reference, server instructions, host config helper | Claude Code and Codex over MCP complete PRD §48 end to end, including the memory-aware assertion; Cowork verified |
| 5. Git sources + promotion (≈1.5 wk) | managed clones, sync timer, `get_promotion_target`, `memory promote` | remote service indexed; an agent produces a doc patch from a memory |
| 6. Engine release (≈1.5 wk) | semantic search option, GoReleaser, brew tap, npx wrapper, benchmarks in CI, docs | installable release usable from any MCP host |
| 7. Desktop (≈4 wk) | Tauri shell, Flows home, Services browser, endpoint runner, response viewer with Remember/Assert, Runs, knowledge panels, structured flow editor + YAML tab | success scenario in UI; idle memory target met |
| 8. In-app authoring (≈2 wk) | BYO-key setting, tool loop inside the app, proposal diff UI, NL edit | eval set passes; memories used are shown |

## 34b. Examples: saved, verified payloads (decided 2026-09-05)

Motivation (user feedback after the first real onboarding): manual testing through a UI and agent authoring both need a library of known-good requests, and an agent that found a working payload should be able to leave it for the next one without a memory that has to be re-derived into a request.

An **Example** is a saved request for exactly one operation, validated against the contract, optionally recording the response it produced and the run that proved it.

```yaml
# <workspace>/examples/create-qcom-order.example.yaml   (workspace scope)
# <repo>/api/examples/create-qcom-order.example.yaml    (service scope: committed, shared)
version: 1
id: create-qcom-order            # default: file stem
operation: order-service.createOrder
description: QCOM order in Bengaluru that allocates to a qcom-skilled rider
input: { }                       # path/query/header params, bound by name (flow-step shape)
body: { customerId: c1, type: QCOM, pickup: { lat: 12.97, lng: 77.59 }, drop: { lat: 12.93, lng: 77.61 } }
headers: { }
expect: { status: 201, body: { orderId: ord_0001 } }     # observed response, trimmed; documentation, not an assertion
verified: { env: stage, run: run_01J..., step: call, at: 2026-09-05T10:00:00Z, by: { kind: agent, client: claude-code } }
tags: [qcom, happy-path]
```

Rules:
- One operation per example; `input`/`body`/`headers` are checked against the contract exactly as a flow step is, with the same diagnostics.
- `verified` is written only by save-from-run paths (`sapien call --save-example`, `sapien run save-example`, MCP `create_example` with `run_id`), never by hand, so a tested example is distinguishable from a drafted one.
- Scope decides storage and sharing, not subject (as for memories): `service` lands in the repo's `api/examples/`, `workspace` in `<workspace>/examples/`.
- Bodies may contain `${inputs.x}` templates; a flow supplies the inputs, the CLI takes `-i k=v`.

Surfaces:
- Flow DSL: a step may set `example: <id>`; it fills `call`, `input`, `body`, `headers`, and explicit step fields override. The validator resolves the id with "did you mean" suggestions (`UNKNOWN_EXAMPLE`).
- CLI: `sapien example list|show|add|rm|rescope`, `sapien call --example <id>`, `sapien call ... --save-example <name> [--scope service]`, `sapien run save-example <run_id> [--step <id>] --name <name>`.
- MCP: `list_examples(operation?, service?, tag?)`, `get_example(id)`, `create_example(...)` and `rescope_example` behind a new `write_examples` class (default true); `execute_api` accepts `example`; `get_context` includes the examples of every selected operation (tier after documentation); `get_api(detail=full)` lists example ids.
- Engine: `engine.ExampleAPI` (List, Get, Create, Update, Delete, FromRun) on the facade, served by Local, Remote and `/v1/examples` on the daemon, so the desktop UI gets a request library.
- Storage: files are the source of truth; a SQLite `examples` table indexes id/operation/service/tags for lookup, rebuilt on reindex like memories.

## 34c. Phase 7a: live inspector UI (decided 2026-09-05)

Positioning: the chat is Claude Code, Codex, or Cursor; Sapien's UI is the glass beside it. It shows, live, what agents and humans do through the shared daemon, and it is where payloads get inspected, fixed, rerun, and saved. No LLM in it.

Priorities (user, 2026-09-05): 1) flows created by agents, their runs with every intermediate request/response, editing a step's payload to rerun, saving a step as an example; 2) services, endpoints, docs, and example payloads for manual testing, search by intent, run from the page.

Architecture: a single-page app (Vite 4 + React 18 + TypeScript + Tailwind; Vite 4 because the dev machine runs Node 16) built into `internal/ui/dist` and embedded with `go:embed`, served by the daemon at `/ui/`. `sapien ui` finds or starts the daemon and opens `http://127.0.0.1:<port>/ui/session?token=<token>`, which sets an HttpOnly `sapien_session` cookie and redirects to `/ui/`; the bearer middleware accepts that cookie as an alternative to the header (the Host/Origin checks still apply), so the browser's fetches and the `/v1/events` WebSocket authenticate without JavaScript ever seeing the token. `GET /v1/events/recent` returns the last few hundred events so a reload shows what just happened. The built `dist/` is committed so `go build` never needs Node; `make ui` rebuilds it.

Pages: Flows (list; detail with YAML, materialised steps, diagnostics, inputs form, env picker, run), Runs (list; detail with a step timeline, each step's request, response, timings, assertions, extracted values, and "Might explain it" hints; edit a step's input/body and rerun as an ad-hoc source run; save a step as an example), Services (status, operation count, unaccepted and accepted warnings, declared-versus-defined environments, sync, add service), Operations (search by intent through `/v1/context` and `/v1/operations?query=`; detail with fields, docs sections, examples, memories, and a Try-it form prefilled from an example that runs `/v1/call` and offers save-as-example), Examples, Memories, Events feed. Everything updates from the event stream.

Performance and memory budget (decided 2026-09-05; the product exists because Postman is heavy): one Go process (the daemon) and a browser tab, no Electron ever; Tauri later uses the OS webview. Initial route chunk under 120 KB gzipped and the whole app under 300 KB gzipped, enforced by `npm run size` in the build; every page is a lazy route; no component library, no Monaco, no CodeMirror in the shell (payload editing is a form from the contract or a textarea with a light highlighter). No polling; the WebSocket pushes. The events store keeps at most 500 small event summaries and never bodies; run details are fetched on view and dropped on leave. JsonView mounts children on expand, pages arrays over 200 items, and truncates strings over 256 KB with a raw copy; long lists are windowed by a 60-line component, not a library. List endpoints return summaries without bodies. Targets: first paint under 200 ms on localhost, a 1,000-item list scrolls without jank, a tab with a 50-step run open under 60 MB of JS heap; the daemon itself stays a single idle-exiting Go process with SQLite.

Phase 7b (next): an agent pane that spawns the user's own `claude` or `codex` in an xterm.js terminal wired to the workspace MCP, with "ask the agent about this run" hand-offs. Phase 7c: Tauri shell and an SDK-built chat with BYO key and multi-MCP.

## 34d. Cheap iteration for long flows (decided 2026-09-05 from the 41-step field report)

Source: `docs/feedback/2026-09-05-41-step-flow-session.md`. The knowledge layer is the product; the runner lost the agent at the moment it mattered because every failure restarted a 40-step flow from step 1 against a shared staging system.

1. **Resume and partial runs.** `RunOptions.ResumeFrom` reuses an earlier run's setup and pre-failure step results (request, response, extracts) so later expressions resolve as before; `FromStep` and `UntilStep` bound execution; a reused step whose definition changed is reused with a warning. Surfaces: `sapien run resume <run_id> [--from] [--until]`, `flow run --resume/--from/--until`, MCP `run_flow(resume_from, from_step, until_step)`, HTTP `resume_from`/`from_step`/`until_step`.
2. **Setup and teardown.** `setup:` runs first and is addressable as `steps.<id>`; `teardown:` always runs, even after failure or cancel, and never changes the run's outcome. Resume reuses setup results by default.
3. **Round-trippable authoring.** `expr:` accepted in the assertion object form so `get_flow` output is valid input; `get_flow` text is the YAML source; `create_flow`/`update_flow` return `{id, path, steps, ..., diagnostics}` and never the document; `path` is relative to `<ws>/flows` and must stay inside it.
4. **Step-level edits.** `internal/flowpatch` applies ops (`set_step`, `merge_step`, `add_step`, `remove_step`, `set_inputs`, `set_meta`) to YAML nodes, preserving comments and order; MCP `patch_flow(id, ops)`; `sapien flow patch`; `update_flow`/`flow update --file` accept a file already edited on disk.

Deferred to the next round (items 5-8 of the plan given to the user): validate-time documented-error hints, an `environment` reference topic with the exact YAML to add on "no base_url", a context budget that binds, memory `supersedes` and `memory audit`.

## 34e. Understandability, not just indexability (decided 2026-09-12 from consumer-agent feedback)

Source: feedback from agents consuming onboarded services, plus the user's own session using the inspector. The pattern behind four of the five observations: Sapien served structure and left meaning behind.

1. **Orientation.** `get_dsl_reference("sapien")` (resource `sapien://reference/sapien`): what Sapien is (a cross-repo index of contracts, the services' own docs, working examples, memories, flows), how to consume a service you do not own, why `execute_api` beats a throwaway script, where onboarding lives. The MCP `instructions` open with the same framing; budget 1,300 -> 1,750 characters. Agents had the tools and not the frame, so they read schemas here and called services elsewhere.
2. **A request example on every operation.** One resolver (`internal/example.Resolve`) picks a verified saved example, else a hand-written one, else the contract's `example:`, else a synthesis from the request schema, labelled with its source and how much to trust it. Served by `get_api.request_example` (every detail level) and `GET /v1/operations/{id}/example`; the UI's Try It form prefills from it, and `?fields=all` (`example.Synthesize`) fills the whole declared shape for a human who would rather delete fields than look them up. The authored home is the contract's own `example:`, written during onboarding, because it travels with the code and every OpenAPI tool shows it.
3. **Docs that carry business logic.** `reference_service.md` is rewritten around its actual reader -- an agent in another repository -- with a checklist of what a section must answer that the contract cannot (why it exists and who calls it, preconditions, invariants, side effects, idempotency and retries, every error code and what to do about it, timing, deprecations, the traps), plus `## Open questions` for what could not be established.
4. **An interview step.** One batched round *after* drafting from the code, with a question bank at service/subproject, operation, and cross-service level, a rule against asking anything the code answers, and answers written into the contract, the docs, and memories. Some of what makes a service usable is in nobody's code.
5. **Coverage as a number and as lint.** `registry.coverage` counts operations the narrative docs reach and bodies with an example, reported by `add_service`/`sync_service`/`get_service`/`service list`, with `NO_NARRATIVE_DOCS`, `UNDOCUMENTED_OPERATION`, `MISSING_REQUEST_EXAMPLE`, `NO_CONCEPTS` naming the gaps. Acceptable in `accepted_warnings` with a reason, per §6's acceptance rule: mandatory coverage would reproduce the 2026-09-06 incident where an agent chasing zero warnings made a contract lie.
6. **`soft:` in the flow reference.** It existed everywhere except the text agents read. `TestReference_DocumentsEveryDSLKey` reflects over every YAML tag the parser accepts so no future DSL key ships invisible.

Not addressed here: coverage measures whether a doc section mentions an operation, not whether it says anything useful; and nothing re-opens the interview when the code changes under a service onboarded earlier.

## 34f. Control from the UI, folders, non-linear flows (decided 2026-09-19 from consumer feedback)

Source: consumers of the inspector. The pattern: the engine could already do most of it, but only from a shell. Nine decisions; the HTTP shapes below are the contract the backend and the UI are built against in parallel.

1. **Changes page** (`/ui/changes`): one tree of the workspace repository like an editor's source-control panel. Change marks roll up folder -> repository -> nav badge. Select files or folders, write a message, Commit; Pull and Push sit beside it. Covers every file in the workspace repo, including `sapien.workspace.yaml`, `environments/` and `.gitignore`, which no per-kind Commit reaches. §7b's rules hold: a human asks, workspace repository only, never forced, no MCP tool. Bound service checkouts are listed (branch, changed files under the API package) but read-only: those repositories are the developer's. No discard action in this round.
   - `GET /v1/workspace/repo/changes` -> `{status: RepoStatus, files: [{path, state, kind, id?, title?, old_path?}], services: [{name, mode, path?, branch?, ref?, dirty, files: [{path, state}]}]}`; `state` is `untracked|modified|deleted|renamed|conflicted|unpushed`; `kind` is `flow|memory|example|environment|workspace|other`; listing uses `git status --porcelain -z -uall` so a new directory is expanded into its files.
   - `GET /v1/workspace/repo/diff?path=` -> `{path, state, diff, content?, binary, truncated}` (`content` for an untracked file).
   - `POST /v1/workspace/repo/commit {paths[], message}` -> `{commit, committed, status}`; deletions commit too; a path outside the repo or ignored is refused.
   - CLI `sapien workspace changes`, `sapien workspace commit -m <msg> [--all | paths...]`.
2. **Service ref from the UI.** `PUT /v1/services/{id}/ref {ref, scope}` with `scope: local` (default; a `ref:` beside `path:` in `sapien.workspace.local.yaml`, this machine only) or `team` (rewrites `source.ref` in `sapien.workspace.yaml`, which then shows on the Changes page); `DELETE /v1/services/{id}/ref` clears the local override; `GET /v1/services/{id}/branches` -> `{current, default, branches[], tags[]}` from `ls-remote`. Both resync through `gitsrc.Sync`. A managed clone with a local ref override gets its own cache directory keyed by URL and ref so two refs of one URL never thrash one clone. CLI `sapien service set-ref <name> <ref> [--team]`, `--clear`. A bound checkout's branch is shown, never switched.
3. **Daemon control.** `GET /v1/daemon` -> `{version, commit, started, pid, port, executable, install_method, workspaces_open, active_runs, terminals}`; `POST /v1/daemon/restart {force?}` -> 202, 409 with `active_runs` when runs are in flight and not forced; the daemon spawns a detached `serve --restart` successor. `sapien daemon restart`. The bearer token is persisted (`~/.sapien/daemon-token`, 0600) so a restart keeps browser sessions and MCP bridges valid. `*.localhost` hostnames pass the host and origin guard (RFC 6761 names cannot be served by public DNS), and `sapien ui` opens `http://sapien.localhost:<port>`.
4. **Updates.** The daemon checks the latest release at most once a day through the `releases/latest` redirect, caches it in `~/.sapien/update-check.json`, and is turned off by `updates: {check: false}` or `SAPIEN_NO_UPDATE_CHECK`. `GET /v1/update` -> `{current, latest, available, checked_at, release_url, install_method, can_self_upgrade, command, check_enabled, error?}`; `POST /v1/update/check`; `POST /v1/update/apply` -> 202 (409 when not self-upgradable); `PUT /v1/settings/updates {check}`. `sapien upgrade` downloads the release archive, verifies `checksums.txt`, replaces its own binary atomically and restarts a running daemon; Homebrew, `go install` and dev builds are told the right command instead. `scripts/install.sh` upgrades in place: same-version short-circuit, installs over the existing binary's directory, defers to `brew upgrade` for a Homebrew install, refuses to overwrite a symlinked dev build, restarts a running daemon.
5. **Semantic search from Settings, no restart.** Off by default and no longer an install step. `GET /v1/settings/semantic` -> `{enabled, kind, base_url, model, batch_size, api_key_set, source, status: {state, error?, model, dim, embedded, total}}` (`state`: `off|ready|indexing|error`); `PUT` same fields plus `api_key?` (absent keeps, empty clears) and `scope: user|workspace` (default user), swaps the embedder under a lock in every open workspace and reindexes when the model changed or it was just turned on; `POST .../test` tries a config without saving -> `{ok, dim?, latency_ms?, error?}`; `POST .../reindex` -> 202 with `semantic.index` events `{state, embedded, total}`; `GET .../ollama?base_url=` -> `{reachable, base_url, models: [{name, size}], error?}`; `POST .../ollama/pull {model, base_url?}` -> 202 with `semantic.pull` events `{model, status, completed, total, done, error?}`. Presets are suggestions; only `nomic-embed-text` is labelled default until the search-eval harness has measured another.
6. **Folders.** A flow, memory or example may live in a subfolder of its kind's directory at any tier; the folder is read from the path (no column), orthogonal to tier, and ids stay global so moving never breaks a reference. Items carry `folder` ("" at the root, `/`-separated); lists take `?folder=` (prefix); `POST /v1/{flows|memories|examples}/{id}/move {folder}`; creates take `folder`; an update keeps the file where it is; a tier move keeps the subfolder. CLI `sapien flow|memory|example mv <id> <folder>`; MCP: `folder` on the create and rescope tools and on the list tools. Folder names are indexed as lexical search text.
7. **`when:`** on any step: a CEL boolean over `inputs`, `env`, `steps`; false records the step `skipped` (reason `when`) and the run continues. A reference to a step that may be skipped wants `has()`; the validator warns (`MAYBE_SKIPPED`).
8. **Loop blocks.** A step with `steps:` and no `call` is a block: `foreach: <CEL list>` or `repeat: {until|while, max, interval?}`; `max` caps iterations (foreach default 100, repeat required; hard limit 1000); `break_when:`; `on_error: stop|continue`. Inside, `loop.item` and `loop.index`; `steps.<id>` is always the latest execution (so iteration N reads iteration N-1's cursor); after the loop `steps.<block>.count` and `steps.<block>.iterations[]`. Step ids stay unique across the whole flow, so `patch_flow` still addresses by id and `add_step` gains `into:`. No loop inside a loop, no loops in setup/teardown, resume only at a block boundary. `run_steps` is keyed `(run_id, step_id, iteration)` with -1 outside loops; results carry `iteration` and `parent`. Agent-facing run views collapse iterations ("x12, iteration 7 failed"). `parallel`, `needs`, `use`, `retry`, `datasets` stay reserved.
9. **Flow chart**: a read-only navigator on the flow and run pages (List | Chart), derived from the structured control flow -- a vertical spine, a diamond with a bypass edge for `when`, a container with a back edge and an iteration picker for a loop -- drawn as hand-rolled SVG in a lazy chunk, no graph library, inside the bundle budget. Not an editor: §35's canvas risk stands; authoring stays YAML and the agent.

Also: warnings on the service page and docs on the operation page collapse, remembered per browser; a Settings page (`/ui/settings`) holds semantic search, daemon and updates.

## 35. Engineering risks and mitigations

| Risk | Mitigation |
|---|---|
| Real-world OpenAPI files are messy (external refs, cycles, 3.0 vs 3.1, vendor extensions) | libopenapi handles most; tolerant mode keeps last-good catalog per service and reports file/line; fixture corpus of ugly specs from day one |
| Unstable/missing operationIds break memory subjects and flow `call:` refs | alias table + fallback resolution + `unresolved` state; lint warning encourages fixing upstream |
| CEL not expressive enough for some assertions | CEL covers comparisons, lists, maps, strings, regex; add custom functions (`jsonpath`, `duration`) before considering another language |
| Sidecar lifecycle bugs on desktop (orphans, port clashes) | port 0 + handshake, pid file, kill-on-exit, daemon idle exit |
| LLM produces plausible but wrong flows | tool-grounded loop, validator rejects unknown refs, proposal-only, memories_used transparency |
| Secret leakage in runs/memories | redaction default-on, secret-value scan, no API returns secrets |
| Scope creep toward graph canvas / knowledge platform | Phase gates tied to PRD §48; non-goals list in `docs/non-goals.md` |
| SQLite contention between daemon and CLI | WAL, busy_timeout, single writer in daemon, CLI proxies to daemon when present |
| Webview inconsistencies (WebKit vs WebView2) | plain React + Radix, no exotic CSS, Playwright smoke on macOS/Windows |

## 36. Concept boundaries (PRD §50 emphasis)

| Concern | Lives in | Never in |
|---|---|---|
| What operations/fields exist, formal meaning | OpenAPI (service repo) | memories, flows |
| Narrative documentation: how the domain works, why an endpoint exists, how services relate | `api/docs/**/*.md` in the service repo, plus `info` and tag descriptions in OpenAPI | memories (until promoted), flows |
| Service identity, owners, concepts, env base URLs | `service.yaml` | OpenAPI extensions |
| How operations compose, data passing, assertions | flow files | OpenAPI, memories |
| Learned, operational, environment-specific knowledge | memory files / personal DB | OpenAPI (until promoted), flows (until turned into assertions) |
| What actually happened | run records (DB) | files |
| Which services a developer composes | `sapien.workspace.yaml` | service repos |

Promotion paths are explicit and human-reviewed: memory → OpenAPI description (§26), memory → flow assertion (§26), run → memory (§10 provenance). Nothing moves between layers automatically.

## 37. Decision status

| # | Decision | Status |
|---|---|---|
| 1 | Engine language | **Decided: Go** (§2.1) |
| 2 | Process model | **Decided: daemon-centric hybrid** (§4) |
| 3 | Expressions | **Decided: CEL** (§2.2, §8) |
| 4 | Shared memories as Markdown files in workspace and service repos from V1 | **Built as proposed** (§12) |
| 5 | NL authoring | **Decided: external agents via MCP; no LLM in the engine; BYO-key UI panel only after MCP is proven** (§24) |
| 6 | Flow DSL: flat `input:` + `body:`, `until`/`poll` in V1 | **Built as proposed** (§8); `${…}` also interpolates inside structured assertion values |
| 7 | Desktop stack (Tauri + React) | Deferred to Phase 7; engine (Phases 0–6) complete 2026-09-05 |
| 8 | Product framing (internal vs general) | Deferred; packaging written for a public GitHub org placeholder, now `gs-sinha` |
| Examples as a workspace-layer primitive (§34b): one operation each, `verified` only from runs, scope = storage and sharing | Built 2026-09-05 (engine, flow `example:`, CLI, MCP, context tier, acceptance test) |
| Phase 7a live inspector UI (§34c): browser app served by the daemon, no Electron, enforced bundle budget | Built 2026-09-05; 64 KB initial / 118 KB total gzipped; Phase 7b agent pane next |
| Search: fold doc and memory text into the operations index, learn from use, and blend a docs-mediated ranker (§16) rather than ship an embedding model | Built 2026-09-05; measured on `experiments/search-eval`: weighted column alone neutral, docs-mediated blend 0.7/0.3 lifts R@1 0.72 -> 0.80 with no category worse; static embeddings alone 0.49, revisited only as an optional re-ranker |
