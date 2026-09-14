# Build log

Running record of the autonomous build (Phases 0–4 of PLAN.md). Newest phase last. Each phase records: what was built, test/coverage results, deviations from PLAN.md, and open issues.

Conventions fixed before Phase 0:
- Module path `github.com/gs-sinha/sapien` (placeholder; change with a single sed when the real repo exists).
- Go 1.25.7 in go.mod (libopenapi requires ≥1.25.7); toolchain auto-downloads go1.26.8. Dev machine has go1.23 installed; `go` switches toolchains automatically.
- cel-go is imported as `cel.dev/cel-go` (the module moved off github.com/google/cel-go).
- Contracts every package builds on: `internal/domain` (types), `internal/errs` (error model, exit codes), `internal/engine` (Engine facade interface with Local/Remote implementations to come).
- Agents never edit go.mod/go.sum; all dependencies were pre-fetched.
- Checkpoint commits are made at phase boundaries (local only, never pushed).

## Phase 0 — Foundations (in progress)
Notes while Phase 0 agents ran:
- `go mod tidy` drops not-yet-imported deps; `internal/deps/keep.go` (build tag `deps`) now pins them. go.sum was missing testify's `go.yaml.in/yaml/v3` entry; fixed by tidy. The Makefile's temporary `testify_yaml_fail` tag workaround was removed.
- `go tool covdata` builds lazily on first use with the downloaded toolchain; a parallel first run can report "no such tool covdata". Re-run; it is not a code problem.
- CLI seam: commands register via `cli.Register(func(app *App) *cobra.Command)` from `init()`; `cli.NewEngine` is the hook the orchestrator sets to `engine.Local` once it exists.
- workspace 77.9% / cli 81.8% coverage.

### Phase 0 result
Landed and verified: store (80.4%), events (97.4%), spec (86.4%), workspace (77.9%), cli (81.8%), fixtures/logistics mock (93.5%), textutil (94.1%). All gofmt/vet clean. Fixture operation IDs: order-service.{createOrder,listOrders,getOrder,cancelOrder,getOrderTimeline}; allocation-service.{allocate,listAllocations,get_v1_allocations_stats (synthesized),getAllocation,releaseAllocation,allocateV1 (deprecated)}; rider-service.{getRider,searchRiders,updateRiderStatus,listRiders}. Seed riders R123 (online, qcomSkill), R124 (online, no qcom), R125 (offline, qcom), R126 (Mumbai online qcom), R127, R128.

## Phase 1 — Catalog (in progress)
Landed: ingest/openapi (89.7%; libopenapi; warnings SYNTHESIZED_OPERATION_ID, DUPLICATE_OPERATION_ID, MISSING_SUMMARY, NO_SUCCESS_RESPONSE, UNRESOLVED_REF, CIRCULAR_REF, UNSUPPORTED_MEDIA_TYPE), ingest/docs (100%). Running: catalog (owns 002_catalog.sql), search, registry (defines registry.Snapshot mirroring catalog.Snapshot; wiring converts with a struct conversion).
Then: wiring agent builds engine.Local (services/catalog/search/events) + CLI service/search/describe/docs/reindex + fixture integration tests.

## Phase 2 — Execution (in progress)
Landed: expr (90.5%, CEL with CrossTypeNumericComparisons; ints normalized to int64), env (89.9%), runtime (91.9%), runs (88.9%). Running: flow (parser/validator against a Catalog interface), runner (against Operations interface; includes ValidateSchema for `schema: contract`).
Then: wiring for flows/runner/runs/envs + CLI call/flow/run/secret.

## Phase 3 — Memory (in progress)
Running: memory (files+index+retrieval against a Resolver interface; may add 003_memory.sql). Then: retrieval/context builder + wiring + CLI memory/context.

## Phase 4 — Daemon + MCP (in progress)
Running: server (+ engine/remote, engine/enginetest fake, daemon info), mcp (tools, permissions, resources, host config). Then: wiring of serve/mcp commands, daemon lifecycle, Remote selection in CLI, and the §48 acceptance test over MCP.

## Phase 1 wiring — engine.Local + CLI

Landed `internal/engine/local` (`Open(ws *domain.Workspace, opts Options) (*Local, error)`; `Options{Watch, SkipStaleCheck, Logger}`) composing store/catalog/search/registry/events exactly as PLAN §4 describes for the one-shot CLI path: `registry.Snapshot` converts to `catalog.Snapshot` with a plain struct conversion (the two mirror each other field-for-field, ContractFiles included) via a small `catalogIndexer` adapter. The staleness check fingerprints each local service's package (`registry.PackageFingerprint`) against a `settings` row keyed `fingerprint:<name>` (read/written directly through `store.DB.SQL()`, per spec, not the serialized `DB.Write` path) and resyncs only what changed or is missing from the catalog; a resync failure never blocks `Open` (`registry.Syncer` already records it as a service error). `Watch: true` starts `registry.Watcher` over every locally-discoverable service package and resyncs on change. `Flows/Runner/Runs/Memories/Context/Envs` are stub implementations (`stubs.go`) that all return `E_NOT_IMPLEMENTED` naming the arriving phase. Coverage: 89.9%.

CLI: `service.go` (add/list/remove/sync), `search.go`, `describe.go`, `docs.go` (list/search/show, with a local heading-slug matcher mirroring `ingest/docs.Slug` rather than importing that package), `reindex.go`, `engine_wiring.go` (`NewEngine = func(ws) { return local.Open(ws, local.Options{}) }`). Every command calls `app.Engine()` once and `defer eng.Close()`; `App` has no engine-caching or close hook to extend (`app.go` untouched, per the hard rule), and `App.Engine()` already builds a fresh engine per call, so per-command defer is both sufficient and least-invasive. `internal/cli/phase1_test.go` runs the full add/list/search/describe/docs/reindex sequence through `cli.Execute`, plus unknown-operation and no-workspace exit-code cases.

Deviations / gaps found in the composed packages:
- **Engine facade gaps** (both worked around structurally, not by editing `internal/engine/engine.go`, which is fixed): `CatalogAPI` has no `Stats`, and `ServiceAPI.Reindex` returns only `error` (no per-service results). `reindex` therefore calls `Reindex` for the rebuild, then `Services().List` for the per-service table, and type-asserts the returned `engine.Engine` against a local `statsProvider{ Stats(ctx) (catalog.Stats, error) }` interface — satisfied by the `Stats` method added to `*local.Local` — falling back to zero stats if a future engine implementation doesn't provide it.
- **Breaks one existing test**: wiring `cli.NewEngine` in `engine_wiring.go`'s `init()` makes `internal/cli/cli_test.go`'s `TestEngine_NotWiredYet` fail (it asserts `E_NOT_IMPLEMENTED` from `app.Engine()` with no engine wired, which is no longer true once any phase wires one). Per the hard rule to touch only new files, `cli_test.go` was left as-is; that test needs updating or removing by whoever owns it next.

### Phase 1 exit criterion (verified on built binary, 2026-09-05)
`sapien init` → `service add` ×3 (warnings printed: SYNTHESIZED_OPERATION_ID, CIRCULAR_REF) → `search "allocate rider"` ranks allocation-service.allocate first in ~15 ms → `describe "POST /v1/orders"` → `docs search QCOM`. engine/local 89.9%.
Ranking notes for later tuning: (1) scores are capped at 1.00 after boosts, so several results show 1.00 and agents cannot see the ordering margin — normalize to top=1.0 instead of clamping; (2) PRD query "find riders around a pickup" returns listRiders above searchRiders (lexical only; searchRiders' text lacks "pickup"). Semantic retrieval (Phase 6 option) or a synonyms list would fix it.
Session hit the API rate limit at ~02:00; three agents (server/remote/daemon, mcp, retrieval) were terminated mid-work and are being finished by fresh agents. engine.CatalogAPI lacks Stats; local.Local exposes Stats via a side interface the CLI type-asserts.

### Phase 2/3 CLI landed (fake-engine tested)
call, flow (list/show/validate/create/delete/reference/run with --watch and --report junit|json), run (list/show/pin/purge), secret, memory (add/list/search/show/rm/promote/reindex), context. 42 tests against enginetest.Fake; package coverage 64.8% (renderers under-tested) — a CLI coverage pass is scheduled after Phase 4 commands land. Production guard is enforced client-side via Envs().Get before Runner calls; --watch uses RunOptions.Observer (Remote ignores Observer, so `--watch` over a daemon must fall back to Events() — Phase 4 wiring item).

### Phase 3 retrieval landed (89.7%)
Two bugs fixed by the finishing agent (concept tier now uses service concepts as well as tags; doc-body budget trim now actually shrinks single-paragraph bodies). Follow-ups: (1) the literal PRD intent "Create a QCOM allocation test" does not lexically surface rider-service.getRider; the qcomSkill memory attached to getRider does surface — retrieval should expand operations from retrieved memories' subjects (memory-aware operation discovery). Scheduled for the acceptance-test agent. (2) Doc URIs render as sapien://services/<svc>/docs/docs/<file> because Doc.Path already contains "docs/"; cosmetic, consistent across mcp/enginetest/retrieval; change the template to sapien://services/{name}/{path} later.

## Phase 2/3 wiring — engine/local Flows/Runner/Runs/Envs/Memories/Context

Replaced every remaining stub in `internal/engine/local` (`stubs.go` deleted; nothing left to keep) with real implementations, one file per area: `flows.go`, `runner.go`, `runs.go`, `envs.go`, `memories.go`, `context.go`. `local.go`/`staleness.go`/`watch.go`/`services.go` were extended (not rewritten) to compose the new pieces. Coverage: 83.7% (`go test ./internal/engine/local/... -coverprofile`), gofmt/vet clean, `-race` clean, full `go test ./...` for the repo still green.

**Open() additions.** `runs.Store`, `memory.Store` (Locator: `WorkspaceDir = ws.Dir`, `ServiceDirs` a map built after the staleness check from `catalog.ListServices` — kept live afterward: `Services().Add`/`Remove` update the same map instance by reference, since `memory.Locator` embeds the map header, not a copy; `FlowOwner` closure calls `cat.GetFlowSummary`), `retrieval.CatalogResolver{Cat: cat}` as the memory `Resolver`, `retrieval.Builder`, the secret chain (`opts.Secrets` if set, else `env.DefaultChain(ws)`), and `runner.New` over a small `operationsAdapter{cat}` (`catalog.Catalog.GetOperation` already has the exact signature `runner.Operations.Operation` wants, just under a different method name). Workspace flows/memories are indexed at Open the same way services are: `staleCheck` now also fingerprints (name+size+mtime, hashed) `<ws>/flows` and `<ws>/memories` against two more `settings` keys (`fingerprint:workspace-flows`, `fingerprint:workspace-memories`) and reindexes only on change. In `Watch` mode, `onWatchChange` reindexes on `Change.Workspace` "flows" (`reindexWorkspaceFlows` + emit `flow.changed`) and "memories" (`memStore.Reindex`); "environments" and "workspace" are no-ops (read straight from disk on every use), matching the task's wiring note.

**Extra exported surface beyond `engine.Engine`** (documented per the task, since `engine.Engine` itself is fixed and was not touched):
- `func local.RunError(run *domain.Run) error` — delegates to `runner.ErrorFor`. `engine.RunnerAPI` has no such method (by design: PLAN §9 error mapping is the runner package's concern), so callers that need CLI-exit-code semantics for a `*domain.Run` (as `internal/runner`'s own callers do via `runner.ErrorFor`) call this package-level helper on `local` instead of reaching into `internal/runner` themselves.
- `local.Options.Secrets env.SecretStore` — overrides the default `env.DefaultChain(ws)` (which reaches the OS keychain). Tests inject `env.NewMemoryStore()` so no test ever touches the OS keychain, per the task's requirement.

**Flow ownership / path quirk found and worked around.** `registry.scanFlows` (already-built code, not touched) stores a *service*-owned `FlowSummary.Path` relative to the service's API package directory, while this wiring's own `reindexOwnerFlows` stores *workspace*-owned paths absolute. `Flows().Get/Update/Delete` resolve through a new `(*Local).resolveFlowPath` that joins a relative service-owned path against `catalog.GetService(...).PackageDir` and passes an already-absolute workspace path through unchanged, rather than assuming one convention repo-wide.

**PromotionTarget (PLAN §26).** Implemented the three branches (`Subject.Field`+`Operation` → OpenAPI field; `Operation` only → OpenAPI operation; otherwise → doc, resolving a service from `Subject.Service`, else a schema's service prefix, else a service-owned flow's owner) with two intentionally-simple best-effort pieces flagged in code comments: (1) the field JSON-pointer suffix only handles the `request.body.…`/`response.<status>.body.…` shapes explicitly, returning the operation's own pointer unchanged for anything else ("leave the pointer if unsure", as PLAN §26 allows); (2) the "no matching doc section" fallback proposes `<service>/api/docs/<slug of the memory's first 5 words>.md` with a memory-type heading, but does not check whether that file already exists with unrelated content (a real `--write` promotion CLI, when it lands, should check). A memory with neither `Service`, an `Operation`, nor a resolvable `Schema`/`Flow` subject returns `errs.Invalid` rather than guessing.

**Gaps / deviations to flag:**
- `domain.Memory` has no field to carry a write-time secret-scan warning back to the caller (PLAN §20/§28: "runs and memories are scanned for secret values on write"); `Create`/`Update` call `memory.ScanSecrets` and log at `slog.Warn` on a hit, never blocking the write — there is nowhere else in the current `engine.MemoryAPI` shape to surface it.
- `Runner().Cancel`'s "found and cancelled" branch (as opposed to "unknown/already-finished run") is exercised by `internal/runner`'s own tests but not re-tested here, since reproducing a genuinely in-flight run deterministically from this package would need its own polling fixture; the error branch is covered.
- Removed `local_test.go`'s `TestStubs_NotImplemented` (and its now-orphaned `assertNotImplemented` helper) since none of `Flows()/Runner()/Runs()/Memories()/Context()/Envs()` are stubs anymore; nothing else in the package referenced that helper.
- Test flow used throughout (`successFlowYAML` in `testutil_test.go`) is the PRD §48 primary scenario per `fixtures/logistics/README.md`: create a QCOM order → allocate (picks R123, the first online+qcomSkill rider) → getRider asserting `online == true` and `qcomSkill == true`; run against the real fixture HTTP mocks via `mock.StartAll`, not a fake transport.

### Phase 2/3 exit criteria (verified on built binary against cmd/sapien-fixtures, 2026-09-05)
`call order-service.createOrder` → 201; `flow validate` clean; `flow run qcom-allocation` → passed 3/3 with bare CEL, structured `{path, eq}`, and `{schema: contract}` assertions; `run list`; `memory add --op rider-service.getRider --field response.200.body.qcomSkill --type invariant`; `memory search qcom` → 0.85; `context "Create a QCOM allocation test"` → 8 operations, 6 doc sections, the new memory, the flow; `--report junit` valid XML. engine/local 83.7%, server 53.9% standalone (87% when measured through remote's round-trip tests), remote 89.2%, daemon 80.9%.
Follow-ups: (1) context operations print in near-arbitrary order because search clamps scores at 1.00 (ties) — change search to normalize to top=1.0 and have context sort by score; do after the semantic hook lands in internal/search. (2) getRider not in the bundle until memory-driven expansion lands (agent running). (3) CLI prints ANSI dim codes when stdout is not a TTY — add isatty detection in output.go in the polish pass.

## Phase 4 wiring + acceptance

**Engine selection (`internal/cli/engine_wiring.go`).** `NewEngine` now implements PLAN §4's "one-shot CLI" rule instead of always opening `engine.Local`: `SAPIEN_NO_DAEMON=1` forces `local.Open` unconditionally (tests/CI); otherwise `daemon.Find(ctx, ws, Version)` decides — no daemon (`nil, nil`) → `local.Open`; a live, version-matching daemon → `remote.New("http://127.0.0.1:<port>", token)`; a version mismatch (`errs.Conflict`) → the same error with `WithHint("run \`sapien serve --restart\`")` attached in place (daemon.Find already builds the `*errs.Error`; the hint is added, not a new error). Each branch logs the choice at `slog.Debug`. It never starts a daemon itself — that is `sapien serve` and `sapien mcp`'s job, never a one-shot command's.

**`sapien serve` (`internal/cli/serve.go`).** Opens `local.Open(ws, Options{Watch: true})`, builds `server.New` for its `Handler()` only (not `ListenAndServe`, which binds internally and can't be handed a second route), composes a `http.ServeMux` — `/mcp` → `mcp.HTTPHandler(...)` wrapped in a local `bearerGuard` (a same-shape reimplementation of `internal/server`'s unexported `authMiddleware`, since that package exposes no way to add a route beside its own); everything else → `server.Handler()` — wraps the whole mux in a local `idleGuard` (same shape as `internal/server`'s unexported `idleTracker`: counts in-flight requests, including an open `/mcp` streamable-HTTP connection, as busy) and serves it with its own `http.Server`/`net.Listener` so the idle timer covers both routes uniformly. Writes `daemon.json`, prints the handshake (`sapien daemon listening on 127.0.0.1:<port> (pid N)` under human output; the full `daemon.Info` as one JSON document under `--json` — read literally, "and, with --json, the Info JSON" was interpreted as *replacing* the human line, following every other command's `if IsJSON {…} else {…}` convention rather than emitting both), and shuts down gracefully (5s deadline) on SIGINT/SIGTERM or on idle, removing `daemon.json` either way. Refuses to start when `daemon.Read`+`daemon.Alive` finds a live daemon for the workspace (`errs.Conflict` + the same restart hint) unless `--restart`, which signals the old PID (`stopDaemon`: SIGTERM, poll up to 5s) and removes the stale file first. `--foreground` is accepted but currently a no-op: `sapien serve` never daemonizes itself (Setsid/session detachment happens one level up, in `spawnDaemon`, only when `sapien mcp` starts it), so there was nothing for the flag to toggle; kept for forward compatibility (e.g. a future desktop sidecar) rather than dropped, per the CLI spec listing it.

**`sapien mcp` / `sapien mcp config` (`internal/cli/mcp.go`) and daemon helpers (`internal/cli/daemonctl.go`).** `sapien mcp` calls `findOrStartDaemon` (daemonctl.go): `daemon.Find`, and if none is running, `spawnDaemon` — `exec.Command(<self>, "serve", "--json", "--workspace", <dir>)` with `SysProcAttr{Setsid: true}`, reading the handshake JSON off its stdout pipe with a 10s timeout, then reaping the child in a background goroutine without blocking on it (it is meant to outlive this invocation). The resulting `engine.Remote` is handed to `mcp.ServeStdio`. `SAPIEN_NO_DAEMON=1` bridges stdio directly over `local.Open` instead (tests, and hosts that want isolation) — `--http` is rejected in that mode since there is no daemon to point at. `--http` otherwise prints the daemon's `/mcp` URL and its bearer token (interpreted "token hint" as the literal `Authorization: Bearer <token>` line the user needs, not a redacted pointer — the token already lives in a mode-0600 file the user's own process can already read, so withholding it from a command whose whole job is connecting a *different* tool to that same daemon would only cost the user a `cat .sapien/daemon.json`). `mcp config --client <x> [--write]` calls `mcp.HostConfig(client, <os.Executable(), symlink-resolved>, nil, ws.Dir)` and either prints the result or installs it: claude-code runs `claude mcp add` when `claude` is on `PATH` (else prints); codex appends to `~/.codex/config.toml` when that file exists (else prints); cowork/generic always print (there is no local host config file to install into for either).

**`internal/acceptance/acceptance_test.go`** (new package, test-only, no non-test files — `go test`'s coverage tool reports `[no statements]` for it, which is expected). `TestAcceptance_PRDSuccessScenario` drives PRD §48 end to end over MCP, in-process, against the real fixtures (`mock.StartAll` + the three logistics service packages copied into a fresh workspace, `local.Open`'d and `Services().Add`'d, with `environments/local.yaml` pointed at the mocks): search → get_api → search_docs → execute_api proving the permission gate (denied under default permissions, a second server/session with `ExecuteMutation: true` for client "acceptance" succeeds with 201) → create_memory (the qcomSkill invariant) → get_context (asserts `allocation-service.allocate` and the new memory are present unconditionally; `rider-service.getRider`'s presence — PLAN §14/§25's memory-driven operation expansion — is asserted in its own `t.Run` subtest with a `t.Skip` fallback per the task, but **did not skip**: `internal/retrieval`'s `expandFromMemories` (builder.go) has already landed, so getRider is expanded in from the memory's subject and the subtest asserts normally) → get_dsl_reference → validate_flow on a broken flow (unknown op `rider-service.getRiders`; one correction from the task's literal wording below) → validate_flow/create_flow/run_flow/get_run on the real §48 flow (3 steps, all assertions pass, no `Authorization` header in the redacted request records since the workspace has no auth configured) → search_memories/get_relevant_memories (with a structural reason, e.g. "operation match"). Two CLI-level subtests close it out: `cli.Execute(["flow","run",id,"--json"], …)` under `SAPIEN_NO_DAEMON=1` (exit 0, `status: "passed"`), and a real daemon round trip — `server.New` wrapped in a request-counting `http.Handler`, `daemon.Write`'d for the same workspace, then `cli.Execute(["search","allocate rider","--json"], …)` with no env override, proving it went through `engine.Remote` by asserting the counter is `> 0`.

*Deviation from the task's literal wording:* `validate_flow` on the broken flow does **not** come back as `IsError` — `internal/mcp`'s `validateFlow` handler (confirmed against its own `tools_flows_test.go`, which asserts exactly `require.False(t, res.IsError)` with the comment "validate_flow itself never errors; it reports diagnostics") always returns a normal (non-error) tool result whose `Content` text and `StructuredContent.Diagnostics` carry the "did you mean `rider-service.getRider`" repair-loop suggestion. The acceptance test asserts on that text directly rather than on `IsError`, which the task's wording assumed.

**Coverage / verification.** `internal/cli` 86.5% (`go test ./internal/cli/... -coverprofile=... -covermode=atomic`); every new file in this task is exercised: `serve.go`'s happy path (`TestPhase4_Serve_HappyPath`: binds, prints the handshake, `GET /v1/health` unauthenticated, `/mcp` rejects no/wrong bearer token and accepts the right one, shuts down cleanly on a short idle timeout with `daemon.json` removed — all without ever signaling the test process) and its refuse-when-alive path; `daemonctl.go`'s `daemon status`/`daemon stop` (against a real, killable child process — see pitfall below); `mcp.go`'s `mcp config` for all four clients, both printed and `--write`-installed (with `$HOME`/`$PATH` redirected so the codex/claude-code branches are exercised hermetically, never touching the real machine's actual config); `mcp --http`; and `engine_wiring.go`'s four `NewEngine` branches. `spawnDaemon` and the `claude mcp add`/`~/.codex/config.toml`-exists branches of `writeMCPHostConfig` are the one meaningful gap (0% — spawning the real built binary or shelling out to a real `claude`/pre-seeded config file is integration territory better covered by the manual reproduction below than by a unit test). `internal/acceptance` has no coverage percentage (test-only package) but its one test exercises the full MCP tool surface end to end; `go test ./... -race` is green except for one unrelated, non-reproducing flake in `internal/engine/local/git_test.go` (`TestGitService_Watch_PeriodicSyncPicksUpPush`, the concurrent git-sync agent's package, not touched by this task — passed immediately on a solo re-run).

**Pitfall found and fixed while writing the daemon-lifecycle tests:** a `daemon.Info.PID` used to test `daemon stop`/`serve --restart` must never be `os.Getpid()` — `stopDaemon` sends a real `SIGTERM` to that PID, which killed the test binary itself the first time (`TestPhase4_DaemonStatus_And_Stop_LiveDaemon` took the process down with it, logged simply as "signal: terminated"). Fixed by spawning a disposable `sleep 300` child for that one test and using *its* PID; every other daemon-fixture test (which never signals anything) safely uses `os.Getpid()`, matching `internal/daemon`'s own existing test convention. A second, related pitfall: the disposable child must be reaped (`go func() { _ = cmd.Wait() }()`) as soon as it's started, or its exit leaves a zombie whose PID still answers a signal-0 liveness probe — `stopDaemon`'s "did it actually exit" poll silently burned its full 5s deadline every time until this was added.

**Manual reproduction with Claude Code** (in a workspace with the three logistics services already added):
```sh
sapien mcp config --client claude-code --write
```
This runs `claude mcp add sapien -- <sapien> mcp --workspace <dir>` (or prints that command if `claude` isn't on `PATH`). Then, in Claude Code, prompt:
```
Search for the API that allocates a rider, then create a flow that creates a
QCOM order, allocates a rider, fetches that rider, and asserts it is online
and qcomSkill is true. Validate it, save it, and run it against the local
environment. Remember that QCOM allocations should only select riders with
qcomSkill=true. Then start a new conversation and ask me to "create a QCOM
allocation test" — you should recall the memory and reuse the allocate and
getRider operations without searching for them again.
```
The first `sapien mcp` invocation starts the workspace's daemon in the background (`sapien daemon status` shows it running afterward); a second Claude Code window, or `sapien flow run <id> --json`, or `codex`/Cowork configured the same way, all see the same flow and memory immediately, per PLAN §4/§23's "one daemon per workspace" model.

**What the git-sync/semantic follow-up wiring should know:** this task deliberately did not wire git sync or semantic search into `NewEngine`, `serve.go`, or the MCP config path — `local.Options{}` is passed with every field but `Watch`/`Secrets` left at its default, so whatever fields that agent adds there (and whatever it adds to `~/.sapien/config.yaml`'s non-`mcp:` keys, which `mcp.LoadConfig` already ignores gracefully) should compose without touching this task's files. The one thing worth double-checking once that lands: `internal/engine/local`'s own test suite has an intermittent timing-sensitive failure under `-race` (`TestGitService_Watch_PeriodicSyncPicksUpPush`, passes in isolation) that should be tightened up before it's relied on by a daemon that runs for 30 minutes at a time.

### Daemon path smoke (built binary, 2026-09-05 03:53 IST)
`sapien serve --port 0 --idle-timeout 2m --json` → handshake JSON (pid/port/token/version/workspace); `daemon status` reports it; one-shot `search` routes through the daemon (Remote); `mcp config --client claude-code|codex` prints the exact host snippets; `mcp --http` prints the /mcp URL and bearer header; `daemon stop` terminates and removes daemon.json. Acceptance test `internal/acceptance.TestAcceptance_PRDSuccessScenario` passes with the memory-driven getRider expansion active; cli 86.5% after Phase 4 commands.

## Phase 5/6 wiring — git sources + semantic search into engine/local

New package `internal/config` plus git (PLAN §18) and semantic (PLAN §16) wiring into `internal/engine/local`. Both were, until now, built but never composed into the engine: `internal/gitsrc`/`internal/registry`'s `WithGit` hooks and `internal/semantic`/`internal/search`'s `WithSemantic` hook existed with nothing on the other end inside `local.Open`.

**`internal/config`** (93.8% coverage). `Load(ws *domain.Workspace) (Config, error)` merges `~/.sapien/config.yaml` (or `$SAPIEN_CONFIG`) with `<ws>/.sapien/config.yaml` layered over it, field-by-field (a workspace file only overrides the keys it actually sets, mirroring `internal/mcp/permissions.go`'s `rawPermissions` pointer-field pattern rather than a plain struct overwrite). Three top-level keys: `semantic:` (`enabled`, `kind` openai|ollama, `base_url`, `model`, `api_key` — literal or `${env.X}`/`$X`, `batch_size`), `git:` (`cache_dir`, `sync_interval` default `10m`, `timeout` default 0 → defers to `gitsrc`'s own 60s default), `daemon:` (`idle_timeout` default `30m`, `memory_limit` default `2GiB` — a GOMEMLIMIT-style byte size, `0`/`off` for no limit, applied by `sapien serve` only and never over a `GOMEMLIMIT` already set in the environment). Unknown top-level keys — `mcp:` chief among them — are silently ignored: `rawConfig` simply has no field for them and neither loader ever calls yaml.v3's `KnownFields(true)`, so `internal/mcp.LoadConfig` and `internal/config.Load` read the same file for their own keys without either needing to know the other's schema (verified by a round-trip test that puts both `mcp:` and `semantic:` in one file). A bad duration string anywhere returns `errs.Invalid` naming the exact key (`git.sync_interval`, `git.timeout`, `daemon.idle_timeout`, or `daemon.memory_limit`) both in the message and as a `WithDetail("key", …)`.

**Git sources (`internal/engine/local`).** `Open` now always builds a `gitsrc.Manager` (`CacheDir` from `Options.GitCacheDir` else `config.Git.CacheDir` else gitsrc's own `~/.sapien/repos` default; `Timeout` from `config.Git.TimeoutDuration()`) and calls `syncer.WithGit(gitMgr)`, so a `Source{Kind: git}` service works with zero config beyond `type: git` in the workspace file — this was the gap `TestAdd_GitSourceNotImplemented` (now `TestAdd_GitSource_UnreachableKeepsRegistration`, since git sources are no longer unimplemented) exercised. Three call sites needed a git-aware fix beyond just wiring the `Syncer`:
- `services.go`'s `Add`, when deriving a name for an unnamed source (`ref.Name == ""`), built its own one-off `registry.Builder` that never got `WithGit` called — an unnamed git source's peek-ingest would have failed with `errs.NotImplemented` even though the `Syncer`'s own internal builder was fully wired. Fixed by calling `.WithGit(l.gitMgr)` on that builder too.
- `staleness.go`'s `computeFingerprint` assumed local sources only (returned `errNotFingerprintable` for anything else) and took no `context.Context`. It now branches on `ref.Source.Kind`: git resolves the checkout via `gitMgr.Ensure` (never `Sync`/fetch — see below) and folds the resolved commit into `registry.PackageFingerprint(pkg, checkout.Commit)`, per that function's own doc comment calling out exactly this case (two commits producing byte-identical files must still register as changed).
- `watch.go`'s `startWatch` skipped any non-local source when building the watcher's package map; a git service is now watched at its checkout's package directory (resolved the same `gitMgr.Ensure` way), so a `sapien service sync` run from another process — which rewrites the checkout's working-tree files — is picked up by the same fsnotify-driven path a local edit would be.

**Git staleness policy (PLAN §18's "local repos are never fetched" extended to managed clones on every CLI call).** `gitsrc.Manager.Ensure` only clones on first use and never calls `git fetch`; on an already-existing clone it does nothing but local, network-free commands (`rev-parse HEAD`, reading `sapien.json`). `computeFingerprint`, `staleCheck`, and `startWatch`'s package resolution all use `Ensure`, never `Sync` — so opening the engine (one-shot CLI *or* daemon) never shells out to `git fetch`, regardless of how stale the local clone might be relative to its remote. A git service is only ever actually re-fetched by three call sites, all of which now enqueue a semantic reindex on success too: `Services().Sync(name|"")`, `Services().Reindex()` (both go through `registry.Syncer.syncRef`, which calls `gitMgr.Sync` before `Builder.Build` for a git source when a Manager is configured — this was already true of the pre-existing `Syncer` code, just never reachable before `WithGit` was wired), and the new daemon-only background timer below. Verified with a dedicated test that renames the bare origin repo out of existence between two `Open` calls against the same cache dir: the second `Open` still succeeds, serves the identical cached catalog, and its `LastIndexed` is unchanged (proof no resync — let alone a fetch attempt — happened).

**Watch mode / daemon timer.** `startWatch` now also launches `registry.Syncer.SyncGitPeriodically(ctx, interval)` in a goroutine sharing the watcher's own lifetime `ctx` (so `Close`'s existing `watchCancel()` stops both), with `interval` from the new `Options.GitSyncInterval` override or `config.Git.SyncIntervalDuration()` (default 10m). This is the daemon's git-fetch timer from PLAN §18; the one-shot CLI path (`opts.Watch == false`) never starts it, consistent with the hybrid process model never giving a one-shot command a background goroutine.

**Semantic search (`internal/engine/local/semantic.go`, new file).** `Open` builds an `Embedder` from `Options.Embedder` (bypasses config entirely — tests use this) or, when unset, from `config.Semantic` via `semantic.NewHTTPEmbedder` iff `Enabled`; either way it wraps `*semantic.Index` in a `semanticAdapter` (kind string ↔ `semantic.Kind`, `semantic.Hit` ↔ `search.SemanticHit`) and calls `l.srch.WithSemantic(adapter)`. The adapter's `Query` is the one place PLAN §16's "search must still work lexically" failure mode is enforced: `internal/search.lexicalLookup`/`Docs` propagate whatever error their `Semantic` backend returns straight out of `Operations()`/`Docs()` (confirmed against `internal/search`'s own `TestOperations_SemanticQueryErrorPropagates`, which is exactly the behavior the adapter must never let reach a caller) — so `semanticAdapter.Query` catches an embed/query failure, logs it at `slog.Warn`, and returns `(nil, nil)` instead, meaning an unreachable embedding endpoint degrades to lexical-only results with no visible error anywhere above this package.

**Indexing hook.** A single background worker goroutine (`semanticWorker`, started alongside `semIdx` iff semantic search is enabled) drains a bounded channel (`semQueue`, capacity 64); every call site that resyncs a service successfully — `Services().Add/Sync/Reindex`, the one-shot staleness check, and `onWatchChange`'s per-service resync — enqueues a non-blocking `enqueueSemanticIndex(service)` job (full queue → dropped, logged at warn, never blocks the sync it's piggybacking on); every call site that writes memories — `Memories().Create/Update/Reindex`, plus the workspace-memories reindex path in `staleCheck`/`onWatchChange` — enqueues `enqueueSemanticMemoryIndex`. The worker fetches operations+`Fields` and docs+sections itself (`ListOperations`/`Fields`/`ListDocs`/`GetDoc`) and calls `semantic.Index.IndexOperations`/`IndexDocs`/`IndexMemories`; every failure is logged at warn and swallowed, never surfaced as a sync/write error. `Close` cancels the worker's context *and waits for it to actually exit* (a `semDone` channel) before closing the DB — added after an early version left a benign but noisy "sql: database is closed" race in a job that outlived `Close`.

**New exported surface (documented per the task, `engine.Engine` itself untouched):** `Local.SemanticStats(ctx) (semantic.Stats, error)` and `Local.SemanticReindex(ctx) error` (synchronous full rebuild: `Clear` all three kinds, then reindex every service + every memory) — for a future CLI debug command and for tests that need indexing to have provably finished rather than polling. Both are no-ops (zero `Stats`, nil error) when semantic search is disabled. New `local.Options` fields: `GitCacheDir string`, `GitSyncInterval time.Duration`, `Embedder semantic.Embedder`.

**Deviations / things worth flagging:**
- `TestAdd_GitSourceNotImplemented` and `TestWatch_SkipsUnresolvableSources` (pre-existing tests in `local_test.go`, which this task owns) asserted the *absence* of git support and used a `git@example.com:...` URL that would now attempt a real (if doomed) network connection under the newly-wired `gitMgr` — the first attempt at this actually left a real clone directory under the developer's real `~/.sapien/repos` (cleaned up). Rewrote both against a `file://` URL pointing at a nonexistent path, which `git clone` rejects from a local stat with no network involved, and renamed the first to `TestAdd_GitSource_UnreachableKeepsRegistration` since "not implemented" is no longer the behavior being tested.
- Every test in this package now isolates `$SAPIEN_CONFIG` to a per-test temp path (`setupWorkspace`, and the git/semantic tests' own workspace helpers) so `config.Load`'s user-level file never reads (or is affected by the absence of) whatever actually exists at the real `~/.sapien/config.yaml` on the machine running the tests.
- Memory *deletion* does not remove its semantic vector row (only `Create`/`Update`/`Reindex` are hooked); a deleted memory's stale embedding lingers until the next full `SemanticReindex`. Not required by the task and `semantic.Index` has no single-row delete, only `Clear(kind)`.
- `Services().Remove` does not clear the removed service's operation/doc vector rows either, same reasoning; `SemanticReindex` is the cleanup path for both.
- `SemanticStats`/`SemanticReindex` are not folded into `(*Local).Stats` (which returns `catalog.Stats`, a type this task's file allow-list can't touch) — exposed as separate methods instead, matching how `Stats` itself was already a type-assertable side method rather than part of `engine.Engine`.

**Coverage.** `go build ./...`, `go vet ./...` clean repo-wide; `gofmt -l .` empty. `internal/config`: 93.8%. `internal/engine/local`: 84.2% (`go test ./internal/engine/local/... -coverprofile=... -covermode=atomic`), `-race` clean. Full `go test ./...` green, including `internal/cli`/`internal/acceptance`/`internal/search` (all owned by other concurrent agents, untouched by this task).

### Search tuning verified on binary (2026-09-05)
Scores are now proportional (`allocate rider` → 1.00 / 0.81 / 0.80 / 0.78) and `context` orders operations by score with allocate first. Remaining lexical limits: for "Create a QCOM allocation test" the allocation-service siblings (getAllocation, listAllocations) sit within rounding of allocate because they share service-wide tags/concepts; "find riders around a pickup" ties listRiders with searchRiders because the fixture's searchRiders text lacks "pickup". Both are what the optional semantic layer (config `semantic.enabled`) is for; a curated synonyms list per service (`service.yaml: synonyms`) is a cheaper follow-up.

## Closing summary (2026-09-05, ~04:10 IST)

Phases 0–6 of PLAN.md are built, tested, and verified on the real binary. `go build ./...`, `go vet ./...`, `gofmt -l .` clean; `go test -race -cover ./...` green.

| Package | Coverage | Package | Coverage |
|---|---|---|---|
| fixtures/logistics/mock | 93.5% | internal/memory | 85.3% |
| internal/catalog | 83.8% | internal/mcp | 85.2% |
| internal/cli | 85.3% | internal/registry | 87.8% |
| internal/config | 93.8% | internal/retrieval | 89.9% |
| internal/daemon | 80.9% | internal/runner | 93.8% |
| internal/engine/local | 84.5% | internal/runs | 88.9% |
| internal/engine/remote | 89.2% | internal/runtime | 91.9% |
| internal/engine/enginetest (fake) | 37.3% | internal/search | 87.3% |
| internal/env | 89.9% | internal/semantic | 89.0% |
| internal/events | 97.4% | internal/server | 53.9% (87% via remote round-trips) |
| internal/expr | 90.5% | internal/spec | 86.4% |
| internal/flow | 92.0% | internal/store | 81.9% |
| internal/gitsrc | 89.4% | internal/textutil | 94.1% |
| internal/ingest/docs | 100% | internal/workspace | 77.9% |
| internal/ingest/openapi | 89.7% | internal/acceptance | end-to-end test only |

Verified on the built binary: Phase 1 (catalog/search/describe/docs), Phase 2/3 (call, flow validate/run with CEL + structured + contract-schema assertions, runs, memory capture/search, context bundle, JUnit), Phase 4 (serve → daemon.json handshake, one-shot CLI over Remote, daemon status/stop, mcp config for Claude Code/Codex, mcp --http), Phase 5 (git-backed service add from a bare repo, push → sync → catalog updated, commit recorded), and the PRD §48 scenario over MCP in `internal/acceptance` (permission gate, memory capture, memory-driven getRider expansion, validate → create → run → get_run).

Known gaps and follow-ups (none block agent use):
- Lexical ranking ties for intents whose words the contract text lacks ("pickup"); enable `semantic.enabled` or add per-service synonyms (proposed `service.yaml: synonyms`).
- Memory deletion / service removal do not prune semantic vector rows; `SemanticReindex` does.
- Doc URIs render `.../docs/docs/<file>`; change the resource template to `sapien://services/{name}/{path}`.
- `engine.CatalogAPI` lacks Stats and `engine.RunnerAPI` lacks an error-mapping method; both exposed as side helpers on `local.Local` (`Stats`, `RunError`, `SemanticStats`, `SemanticReindex`).
- `Remote` cannot transport `RunOptions.Observer` (use Events) or `MemoryQuery.Subjects` on List/Search (use Relevant).
- `sapien secret set` has no hidden interactive prompt (no x/term); use `--stdin` or `--value`.
- Release pipeline (`.goreleaser.yaml`, brew tap, npm wrapper, install script, Docker) is written but has not been run against a real tag; `goreleaser check` not executed locally.
- Phase 7 (desktop) and Phase 8 (in-app BYO-key authoring) not started, per plan.
- Repo is local-only; module path `github.com/gs-sinha/sapien` and the `gs-sinha` org in packaging are placeholders.

## Onboarding journey (2026-09-05, after the closing summary)

Trigger: first real use. The user created `~/Desktop/workspace`, ran `sapien init`, and asked (a) whether docs must exist before init, (b) whether a workspace that is a git repo tracks "both local and git", and (c) where `sapien mcp config --client claude-code --write` should be run. Answers from the code: no, no (source kind is per `service add` argument; nothing detects `.git`), and "anywhere, but Claude Code's default scope is `local`", which was the wrong default for Sapien. Then: "none of my services are well documented enough", so the plan is to have Claude Code write a Sapien-style `api/` package in each repo and register it, which needed the format to be discoverable over MCP and a registration tool.

Changes (all tested; `go test -race ./...` green, `go vet`, `gofmt` clean):
- **`service` reference topic.** `internal/engine/local/reference_service.md` (embedded; served by `Reference("service")`, `get_dsl_reference("service")`, `sapien://reference/service`, and `sapien flow reference service`): layout, what OpenAPI must contain, `service.yaml`, how docs must reference endpoints/operations/fields so `ingest/docs` links them, how to register, and an onboarding checklist. Kept as Markdown so README can link the same text humans read.
- **`add_service` MCP tool** (`internal/mcp/tools_services.go`) behind a new `write_services` permission class (default true; YAML key `write_services`). Requires an absolute local path (the daemon's cwd is not the agent's), accepts a git URL in either `path` or `url`, rejects `ref`/`subdir` for local sources, renders the same warnings the CLI prints plus next steps. Server `instructions` gained one onboarding sentence so hosts need no prompt. Fake engine in `internal/mcp` grew a real `Services().Add`.
- **`sapien mcp config`**: `--scope user|local|project` (default `user`), `cursor` client (`~/.cursor/mcp.json` merged via `mcp.MergeMCPServersFile`), and idempotent claude-code `--write` (on "already exists", `claude mcp remove --scope <s> sapien` then add again). `mcp.HostConfig` takes a scope; `mcp.ServerArgs`/`NormalizeClaudeScope` exported for the CLI.
- **Relative paths.** `sapien service add ./x` used to store `./x`, which `ResolveSourcePath` resolves against the workspace dir, so running it from a service repo pointed at the wrong place. The CLI now absolutizes against cwd (`absolutizeAgainstCwd`), and `Local.Services().Add` stores a path inside the workspace relative to it again (`workspace.NormalizeLocalPath`) so committed workspace files stay portable.
- **Docs** (Sonnet subagent, reviewed and corrected): README "Make it operational" and "Onboarding services", `docs/onboarding.md` (new), getting-started (from-source install, MCP-everywhere section, cwd-relative paths, correct git-URL rule and `~/.sapien/repos` cache location), `docs/mcp.md` (tool row, permission, scope, cursor, onboarding section). README's Status paragraph was stale ("serve/mcp wiring landing next") and is fixed.

Verified on the built binary: `mcp config --client claude-code` prints `claude mcp add --scope user sapien -- /Users/.../bin/sapien mcp --workspace /Users/.../workspace`; `--scope local` and `--client cursor` render as documented; `flow reference service` prints the embedded text.

Coverage after: mcp 85.8%, cli 86.7%, workspace 78.8%, engine/local 84.6%.

Follow-up the same morning, from the user's next questions:
- **Keeping docs current.** `add_service` now returns a ready-to-paste "## Sapien" section for the repo's `CLAUDE.md`/`AGENTS.md` (`mcp.AgentsFileSection`), the reference has a matching "Keeping docs current" section and checklist step, and the server instructions tell hosts to paste it. Rationale: onboarding is only useful if the next agent that changes a handler also updates `api/`; the repo's own agent instructions file is the one place every future agent reads.
- **Rebuilds.** `sapien mcp` (`findOrStartDaemon`) now replaces a live daemon from another build (`replaceStaleDaemon`: SIGTERM, wait, remove daemon.json, then spawn) instead of failing with the one-shot CLI's "run `sapien serve --restart`" hint, which nobody sees when an agent host launched the process. The one-shot CLI keeps PLAN §4's report-and-hint behaviour. Exported for tests via `internal/cli/export_test.go`.
- **Workspaces.** Guidance recorded in README/onboarding: one workspace per system (all the services one team calls together), because `get_context`, memory-driven expansion, and flows all span a workspace and the MCP entry binds one workspace; separate workspaces only for unrelated systems.

## Feedback round 1 (2026-09-05, after the first real onboarding session)

Source: a session that onboarded four services from scratch (four services, 169 operations; ~25 doc files, 12 memories) with Claude Code plus four Sonnet subagents, then booked a real shipment on stage. The user asked to act on "the ones that sound right". Built by five parallel Sonnet subagents on disjoint files plus the orchestrator; every item below is tested; `go test -race ./...` green.

1. **Warning acceptance with rationale** (the session's #1 ask: "status ok creates pressure to lie", one agent silenced 20 UNSUPPORTED_MEDIA_TYPE warnings by misdescribing text/plain responses as JSON). `service.yaml: accepted_warnings: [{code, match?, reason}]` (reason required by schema); the registry partitions lint warnings into `Warnings` (unaccepted) and `AcceptedWarnings` (with reason); a rule matching nothing yields `STALE_ACCEPTANCE`. CLI shows "K warnings accepted" (+`--show-accepted`, an ACCEPTED column); MCP `add_service`/`sync_service`/`get_service` render both and, while unaccepted warnings remain, say to accept faithful warnings in service.yaml rather than change the contract. Reference and onboarding docs carry the rule.
2. **Memory placement and promotion.** `create_memory`'s description and `memory add --help` now state the rule: scope is storage and sharing, not subject (service = committed in the repo and shared; workspace = local unless the workspace is a git repo); the discriminating question ("still true in a fresh environment with empty databases?"); mixed memories are split, not forced. Write-time hints: where it was stored, "not a git repo" note, a rescope nudge when a workspace memory carries a service subject, up to 3 similar existing memories, and the promotion path. New verb `memory rescope <id> --scope` / MCP `rescope_memory`. Memory reference gained "Choosing a scope" and "Promotion".
3. **Environments.** `sapien env scaffold [--force]` creates `environments/<name>.yaml` from registered services' service.yaml hints (production inferred from the name, never overwrites base URLs); `sapien env probe [env]` pings every base URL and exits 1 on connection failures; env-not-found and missing-base_url errors name the services declaring the env and the scaffold command; `service add`/`sync` and MCP `add_service`/`sync_service` print the declared-but-undefined environments (`env.MissingForService`).
4. **Daemon and permission lifecycle.** `mcp.Options.ConfigPaths`: permissions reload when `.sapien/mcp.yaml` or `~/.sapien/config.yaml` changes (size+mtime stamp), for the stdio bridge and the daemon's `/mcp`. `remote.WithEndpointResolver`: the bridge re-resolves the daemon (via `findOrStartDaemon`) on connection errors or 401 and retries once, so `serve --restart` and idle recycling no longer orphan a live agent session.
5. **`add_service` on an already-registered service re-syncs** instead of failing; `sync_service` is the MCP equivalent of `service sync`. **Workspace discovery**: `--workspace` → `$SAPIEN_WORKSPACE` → walk up → `default_workspace` in the user config, which `sapien mcp config --write` records the first time, so `sapien` commands typed inside a service repo hit the same workspace the agent host does. `sapien init` prints the `git init` tip.
6. **Run-failure hints** (`internal/diagnose`): for a failed/errored step with an HTTP status ≥ 400, extract error tokens from the JSON body (code, error, error_code, reason, message, ...), report the contract's own description of that status first, then docs and memories matching the tokens (≤ 5 per step, ≤ 12 searches per run). Shown as "Might explain it:" by `sapien call`, `flow run`, `run show`, and MCP `execute_api`/`run_flow`/`get_run` (`hints` in structured output; CLI JSON embeds the run and adds `hints`).

Not built from the feedback: suggesting promotion "after N retrievals" (needs retrieval counters; the similar-memory check covers the other trigger). Decided and started next: saved, verified **examples** as a workspace-layer primitive (PLAN §34b).

## Examples wave (2026-09-05, PLAN §34b)

Decided with the user after feedback round 1: saved, verified payloads as a workspace-layer primitive, reusable by flows, the CLI, MCP hosts, and the future UI. Built by five Sonnet subagents on disjoint files (store; engine local + server + spec; flow DSL; CLI; MCP + context) after the orchestrator landed the shared scaffold (`domain.SavedExample`, `engine.ExampleAPI`, `errs.ExampleNotFound`, Remote client, in-memory fakes) so the wave could run in parallel. `go test -race ./...` green; the PRD acceptance test now also saves a verified example from the run, replays it via `execute_api`, sees it in `get_api` and `get_context`, runs a flow whose first step is `example: qcom-order` (then releases the allocation so the fixture's QCOM rider is free again), and checks `UNKNOWN_EXAMPLE` suggestions.

What landed:
- `internal/example`: files are truth (`<ws>/examples/<id>.example.yaml`, `<repo>/api/examples/`), SQLite index (`005_examples.sql`), List/Get/Create/Update (scope move)/Delete/ForOperations/Reindex/IndexOne/RemovePath; 86% coverage.
- `internal/engine/local/examples.go`: Create/Update validate against the catalog (UNKNOWN_OPERATION with suggestions, UNKNOWN_INPUT_NAME, MISSING_BODY, UNEXPECTED_BODY warning, SCHEMA via `spec.Example`); `Verified` only from `FromRun`, which recovers path/query inputs from the recorded URL, drops auth and redacted headers, and stores the observed response as `expect` (16 KB cap). Server routes `/v1/examples...`; Remote round-trip tests; `spec/example.schema.json`.
- Flow DSL: `example: <id>` on a step; `flow.Materialize` fills call/input/body/headers (explicit fields win; body replaces; input/headers merge per key; templates preserved); diagnostics `UNKNOWN_EXAMPLE` (with suggestions) and `EXAMPLE_OPERATION_MISMATCH`; schema requires `call` or `example`; `Get`/`Validate`/`Create`/`Update`/`RunFlowSource` all materialise. The parser initially dropped the field silently (caught by the agent's tests).
- CLI: `sapien example list|show|add|rm|rescope`, `sapien call --example <id> -i k=v` (runs through RunFlowSource so templates work), `--save-example <name> [--scope service]`, `sapien run save-example <run_id>`; `docs/examples.md`.
- MCP: `list_examples`, `get_example`, `create_example` (run_id → verified, or operation+input/body for a draft), `rescope_example`, `delete_example` behind `write_examples` (default true); `execute_api(example=)`; `get_api` lists example ids (the contract's own examples moved to `contract_examples`); `get_context` gained an examples tier (2 per operation, verified first, cut before doc bodies); instructions tell agents to start from a verified example and save working requests.
- Fix found along the way: memory reindex-on-Open ran inside the stale check before `l.serviceDirs` was populated, so a service-scoped memory committed in a repo was invisible to a one-shot CLI Open on a fresh machine until an explicit `memory reindex`. Open now fills the service dirs from the catalog before and after the stale check and `reindexKnowledgeAreas` fingerprints the workspace and every service's `memories/` and `examples/` dirs, reindexing each store once when any changed (`TestOpen_FreshIndex_FindsServiceScopedMemoriesAndExamples`).

Open follow-ups: an `examples` event type (Create/Update/Delete emit no engine events yet); the daemon watcher does not yet call `exStore.IndexOne` on file changes (the stale-check path covers the CLI; the daemon picks up new example files on its next Open or `sapien example reindex`); desktop UI request library is Phase 7 work on top of `/v1/examples`.

### Example replay fixes (2026-09-05, from the user's second real session)
Report: a verified example of an operation with a required header param (`orgId`) could not replay standalone; the saved `headers:` carried the canonicalised `Orgid` plus transport noise (`User-Agent: sapien/dev`, `Content-Type`), `-H` satisfied the param on a direct call but not on `--example`, and the human error said only "flow validation failed: 1 problem(s)". Fixes: (1) the flow validator now treats a `headers:` entry that matches a declared header param case-insensitively as providing it, which is what the wire does, so `-H`, `-p`, and saved headers are interchangeable; (2) `FromRun` lifts recorded headers that match declared header params into `input` under the declared name and drops secrets plus a documented list of transport and tracing headers, so a saved example reads like the contract and carries no noise; (3) human-mode errors print each diagnostic line (code, line, step, message, suggestions), the same detail `--json` always had; (4) `example.Marshal` writes integral floats as integers (a JSON epoch-ms timestamp was being saved as `1.788589814724e+12`). Examples saved before this keep their old headers until re-saved; they now replay anyway because of (1).

## Phase 7a: live inspector UI (2026-09-05)

Scope set by the user: flows created by agents, their runs with every intermediate payload, editing a payload to rerun and saving it as an example; then services, endpoints, docs, and examples for manual testing, search by intent, run from the page. Constraint set by the user: fast and light, never a Postman. Built in two waves of Sonnet subagents (plumbing + shell, then three page agents) on disjoint trees; the orchestrator integrated the seams.

What landed:
- **Delivery**: a Vite 4 + React 18 + TypeScript + Tailwind app (Vite 4 because the dev machine runs Node 16) built into `internal/ui/dist`, embedded with `go:embed`, served by the daemon at `/ui/` with history fallback. `sapien ui [--no-open]` finds or starts the daemon and opens `/ui/session?token=`, which sets an HttpOnly `sapien_session` cookie the bearer middleware accepts (cookie-authenticated writes require an allowed Origin). `GET /v1/events/recent` keeps the last 500 events with run payloads slimmed to summaries; `GET /v1/runs/{id}/hints` exposes internal/diagnose. `domain.Flow.Source` now serialises as `source` so Remote, the MCP bridge, and the UI see flow YAML (that was a latent bug for daemon-backed `get_flow`). The built `dist/` is committed so `go build` never needs Node; `make ui` rebuilds it.
- **Budget, enforced**: `npm run build` fails above 120 KB gzipped for the initial chunk or 300 KB for the whole app. Measured: 64.4 KB initial, 117.7 KB total, 39 vitest tests. Runtime deps: react, react-dom, react-router-dom, zustand, and `marked` lazy-loaded only when a doc opens. No polling; the event store keeps 500 summaries and never bodies; run details are fetched on view and dropped on leave; JsonView mounts on expand, pages arrays over 200 and truncates strings over 256 KB; long lists use a 55-line windowed list.
- **Pages**: Flows (list; detail with YAML source, materialised steps, validate, env + inputs run panel, recent runs), Runs (windowed list; detail with summary bar, pin, step timeline with the failed step expanded, Request/Response/Assertions/Extracted/Error tabs, timings, "Might explain it" hints, live refetch on run events, per-step save-as-example and rerun-step-alone, edit-and-rerun in a textarea YAML editor with save-as-flow), Services (list with warning counts, sync all, add service; detail with declared-versus-defined environments and the scaffold hint, warnings and accepted warnings with reasons, docs drawer with Markdown and operation-mention links, operations table, remove with typed confirmation), Operations (one box: intents go to `/v1/context` and render tiers, keywords to `/v1/operations?q=`; URL is the source of truth; latency shown), Operation detail (params, request and response fields, docs sections, examples with verified badges, memories, Try it), Try it (`/ui/try/:op?example=&env=`: params form, body editor with validation and schema skeleton, headers, env select with production gating, example picker, run, result with hints, save as example, per-operation form state in sessionStorage), Examples (list with filters; detail with YAML, try it, rescope, delete), Memories (search with filters; detail with promotion target, rescope, status changes), Events (windowed live feed with type chips, pause, id links).
- Verified against the user's real workspace over the cookie session: SPA and deep links, four services, the agent-created flow with source and three steps, runs with responses, hints, two examples, memories, both environments, intent search, and the live event buffer. The browser extension was not connected from the orchestrator's session, so visual checks are the user's.

Follow-ups: a docs-referencing-operation route (the operation page derives it through `/v1/context` today); event types for examples; `estimated_tokens` in a context bundle can exceed the requested budget (8967 for 4000), worth a look at the budget enforcement; screenshots and a first-run walkthrough once the user has opened it.

## Search: knowledge columns, feedback loop, and the re-measurement (2026-09-05)

Decided with the user from `experiments/search-eval/REPORT.md` (104 ground-truth intents on the real workspace): fold doc and memory text into the operations index and learn from use, rather than ship a static embedding model (embeddings alone 0.49 R@1 versus lexical 0.72; the simulated doc-enriched BM25 reached 0.77).

Built: migration 006 recreates `operations_fts` with `doc_text` and `memory_text`; `catalog.RefreshOperationKnowledge` keeps them current on docs apply and memory changes; `search_feedback(term, op_id, count)` is fed by a ring of recent searches whenever `GetOperation`, a flow create/update, or a call uses an operation those searches returned, and boosts later searches (weight 0.35, cap 0.3 of the top score, new candidates at count ≥ 3); `matched_on` gains `docs`, `memories`, `feedback`. Fixture acceptance: a memory saying "QCOM eligibility" lifts `allocate` from 5th to 1st; two `GetOperation`s after "find riders around a pickup" put `searchRiders` first, persisted across engines.

Re-measured on the real workspace with the harness (`run_lexical.py`, `metrics.py`, `compare_runs.py`, `sweep_weights.sh`): the doc column as a weighted bm25 column did not reproduce the simulation. With doc/memory weights 2/2: R@1 0.712, 8 queries improved and 8 regressed, two fell out of the top ten (doc cross-talk: a section that mentions several operations makes them look alike). The sweep over weights 0..8 shows every doc weight above zero regressing as many queries as it fixes; memory text at a higher weight is neutral to positive. Defaults set to doc 1 / memory 4, which scores exactly the baseline (R@1 0.721, R@3 0.837, MRR 0.791) with the memory and feedback wins on top. `SAPIEN_SEARCH_KNOWLEDGE_WEIGHTS="d,m"` overrides the weights for experiments without a rebuild. The feedback loop cannot be measured offline; it needs usage.

Open: a bounded experiment is testing a docs-mediated second ranker fused by reciprocal rank (`SAPIEN_SEARCH_DOC_FUSION`), which is closer to what the simulation actually scored; it becomes the default only if it clears +0.03 R@1 with at most five regressions.

### Docs-mediated fusion adopted (2026-09-05, later the same day)
The bounded experiment paid off. A second ranker searches the docs index for the query, maps the top sections to the operations they reference, and blends with the operation ranking; reciprocal rank fusion was bad (R@1 0.587) but a weighted blend at 0.7 lexical / 0.3 docs scores R@1 0.798, R@3 0.885, MRR 0.846 on the 104-query harness against 0.721 / 0.837 / 0.791 before: 14 queries improved and 6 slipped against the previous default, one of them losing first place, and every category held or gained (error intents 0.36 -> 0.50, paraphrase 0.52 -> 0.62, business 0.80 -> 0.90). 0.5 and 0.6 gained less with more regressions; 0.75 to 0.85 gained less. The heading-scoped variant was worse. Shipped as the default (`SAPIEN_SEARCH_DOC_FUSION` unset = weighted:0.7; `off` disables; `rrf` and `weighted:<w>` remain for experiments). Search p95 at 2,008 operations stayed under 0.5 ms. This is the gain the offline simulation predicted for doc enrichment; the weighted-column approach could not deliver it, the second-ranker approach did. The agent that built it was cut off by a rate limit before measuring; the orchestrator ran the sweep, set the default, and adjusted two tests whose preconditions assumed fusion off.

## Phase 7b agent pane and UI fixes from first use (2026-09-05, evening)

User feedback after opening the inspector: flow payloads must be editable, not just flow inputs; "doc contract not found" errors on operation pages; the null-render error in more places; and start the terminal pane.

- **Editable step payloads** (FlowDetailPage): every step's input, body, and headers are editable in place (input seeded from the operation's declared params with required ones marked; body as JSON with validation, `${...}` templates kept as strings), with a modified badge, per-step reset and save-as-example, edits persisted per flow in sessionStorage; "Run with edits" patches the flow's YAML with js-yaml (lazy chunk, 13 KB gz) and runs it as an ad-hoc source run; "Save to flow" PUTs the patched YAML after a one-time warning that comments are not preserved.
- **Doc fetch**: contract-embedded docs have paths like `contract#tag:Awb`; the browser cut the URL at `#`. The client percent-encodes each doc path segment. Pages fall back to the bundle snippet if a doc fetch fails.
- **Null safety**: `normalizeNulls` in the API client turns `null` into `[]` for every array-typed key the domain declares (never map-typed keys); two real bugs found by the audit (environments list returning top-level null, a doc list type error); `nullSafety.test.tsx` renders every page with null arrays. The context bundle also always emits arrays server-side.
- **Phase 7b**: `internal/terminal` PTY sessions (creack/pty, 30 min idle kill, cap 4); `GET /v1/terminal` WebSocket (binary PTY bytes, resize and exit text frames) with commands allowlisted to `claude`, `codex`, and `$SHELL`, and the directory restricted to the workspace, a registered service repo, or home; `GET /v1/terminal/targets` for the picker. UI `/ui/agent`: picker, xterm.js lazy chunk (72 KB gz), split view with a run panel, `?prompt=` hand-off that pastes without pressing Enter, and a module-level terminal that survives in-app navigation.
- Measured: initial chunk 64.9 KB gz, whole app 208 KB gz (budget 120 / 300); 72 UI tests; Go race suite green.

Two agents were cut off by the account rate limit mid-task; fresh agents finished from their files. The terminal's survive-navigation behaviour is unit-tested with mocked xterm but not yet confirmed in a real browser.

### Search by URL (2026-09-05, evening)
User: "search should find operations by url easily". Path-shaped queries were already routed to an alias lookup, but only for `METHOD /path` or `/path`, and a miss returned nothing. Now `textutil.ParseURLQuery` accepts full URLs (scheme, host, port, query string, fragment stripped), paths without a leading slash, and `METHOD <url>`; `search.urlLookup` scores every alias template in Go: exact 1.0, `{param}` match 0.95, the pasted path carrying a base prefix the contract lacks 0.85 (suffix match), a parent path 0.7 minus 0.03 per missing segment, template-as-prefix 0.6; a given method that differs multiplies by 0.6, a matching one is labelled `method`; `matched_on` starts with `url`; fewer than three URL hits append lexical hits at half score, and no hit at all falls back to lexical over the path tokens. `describe` accepts full URLs too. The UI treats any path-shaped text as a lookup regardless of word count, including text arriving as `?intent=`. Verified on the real workspace: a full staging URL with a query string resolves the booking operation first; the same path behind a base prefix scores it 0.85.

## Resume, setup/teardown, round-trip authoring (2026-09-06, from the 41-step field report)

Source: `docs/feedback/2026-09-05-41-step-flow-session.md`; decisions in PLAN §34d. Two Sonnet subagents (runner; authoring) were both cut off by the account rate limit near the end; the orchestrator finished the wiring from their files.

- **Setup and teardown**: `setup:` runs first and is addressable as `steps.<id>`; `teardown:` always runs, in order, past its own failures, on a context detached from every cancellation and bounded by a two-minute timeout, and never changes the run's status. Step results carry `phase`.
- **Resume and partial runs**: `RunOptions.ResumeFrom/FromStep/UntilStep` → `runner.Resume`; the earlier run must belong to the same flow; setup and pre-resume-point steps are copied with `reused: true` and the expression context is seeded from them; a changed definition is reused with a warning; inputs default to the earlier run's. Surfaces: `sapien run resume <run-id> [--from] [--until]`, `flow run --resume/--from/--until`, HTTP `resume_from`/`from_step`/`until_step`, MCP `run_flow(...)`; run tables mark `(reused)` and `setup:`/`teardown:` phases; MCP views carry `phase`, `reused`, `warnings`, `resumed_from`.
- **Round-trippable authoring**: `expr:` accepted in the assertion object form; `get_flow` text is the YAML; `create_flow`/`update_flow`/`patch_flow` return `FlowSaveResult{id, path, steps, setup_steps, teardown_steps, operations, diagnostics, bytes}`; `path` relative to `<ws>/flows` and fenced; `update_flow(path)` re-reads a file edited on disk.
- **`internal/flowpatch`**: `set_step`, `merge_step`, `add_step`, `remove_step`, `set_inputs`, `set_meta` on YAML nodes preserving comments and order; MCP `patch_flow`; CLI `flow patch` is still to do (the authoring agent was cut off before the CLI part; the MCP tool and engine paths are complete).
- Acceptance test now: create a setup/teardown flow, run (fails at a wrong assertion, teardown still passes), `patch_flow` one assertion, `run_flow` with `resume_from` (setup and allocate reused with the earlier extracts, rider executed and passed, `resumed_from` set).

Left for the next round: `sapien flow patch` / `flow update --file` CLI verbs, validate-time documented-error hints, the `environment` reference topic and exact-YAML hint on "no base_url", a context budget that binds, memory `supersedes` and `memory audit`.

## Second field report: soft assertions, lean run output (2026-09-06)

Source: `docs/feedback/2026-09-06-58-step-intercity-flow.md`. Resume paid for itself at step 37 of 58; the flow file proved to be a living runbook. Fixes from its friction list, done by the orchestrator directly:
- **Soft assertions**: `soft: true` on any assertion form; a mismatch is recorded on the step and counted as `assertions_warned` without failing the step or run; `diagnose.SoftChanges` compares with the most recent earlier run of the same flow and reports flips ("now passing since run X"), printed by `flow run`, `run resume`, and `run_flow`. Tests in runner and engine/local.
- **`run_flow` output**: `detail` = summary (default; no bodies), failed (bodies only on failed/errored steps), full; the text ends with the `get_run(..., step=)` pointer. Previously every 58-step result carried every body and overflowed the host's token cap.
- **`validate_flow(path)`** validates a file inside `flows/` without echoing 47 KB back; **`get_flow(detail: outline)`** lists steps one per line for skimming.
- **UI**: the agent page is terminal-only; the run panel duplicated the runs section (user's note).

## Incident: a flow vanished from the index after the upgrade (2026-09-06)

Report from the agent session: after the daemon restarted on build 3794bef at 02:18, `fm-mm-v2-rto-intercity` (58 steps, four soft assertions) disappeared from `list_flows` and the `flows` table; `create_flow` and `sapien flow create` reported success, wrote the file, and no row landed; `validate_flow(path)` worked while every id-addressed call returned `E_FLOW_NOT_FOUND`. The file was intact throughout.

Cause: two daemons. `lsof` on the workspace database showed pid 8598 (the daemon named in `daemon.json`, build 3794bef) and pid 91162, a `sapien serve` from 00:46 running the previous binary image, parent launchd. The 02:18 replacement stopped whichever pid `daemon.json` named at the time and started the new daemon; the older process was never named there, so it survived, kept its file watcher on the same directory, and on every pass reindexed with its older grammar, which rejects `soft:`; `reindexOwnerFlows` silently skipped the unparseable file and its `UpsertFlows` (delete owner rows, reinsert) removed the row the new daemon had just written. Reproduced live: one second after a create, the table lost exactly that flow and kept a stub without soft assertions. Copies of the workspace under a fresh daemon, including one at a differently cased path, never reproduced it, which pointed at process state rather than data.

Fix on the machine: stop 91162, touch the flow file, the live daemon reindexed it; a probe stub created for the diagnosis was deleted. Fixes in code:
- **Workspace lock** (`internal/daemon/lock.go`): `sapien serve` holds `<ws>/.sapien/daemon.lock` (pid inside) for its lifetime; a live holder blocks a plain `serve` with `E_CONFLICT` naming the pid, `serve --restart` stops it; `findOrStartDaemon` stops an orphan holder before spawning; `replaceStaleDaemon` stops the lock holder as well as the `daemon.json` pid.
- **Reindex never drops a flow it failed to parse**: the previous row is kept (matched by path, then id), a warning is logged with the file and error; `get_flow` on a broken file then reports the parse error instead of "not found". A deleted file still drops its row.
- **Daemon log**: a spawned daemon's stderr goes to `<ws>/.sapien/daemon.log`, so warnings like a failed reindex are findable.

## Repository transfer and module rename (2026-09-06)

v1.0.0 was pushed to `growsimplee/sapien` and the GitHub release was assembled by hand (darwin/linux archives plus `checksums.txt`) because the org's Actions were locked for billing; the repository was then transferred to `gs-sinha/sapien`, where the old URL redirects and the same CI run passed on rerun. Every reference followed the move in one commit: the Go module path (`github.com/gs-sinha/sapien`, 364 files), the Makefile and goreleaser ldflags, installer and README URLs, `ghcr.io/gs-sinha/sapien`, the `gs-sinha/tap` formula, and the npm scope. Consequence to remember: `go install github.com/gs-sinha/sapien/cmd/sapien@latest` needs a tag cut after the rename, since v1.0.0's `go.mod` still declares the old path; the release archives and `scripts/install.sh` do not care.

## Flow page reordered around running a flow (2026-09-06)

User feedback from the inspector: the flow page showed a very large YAML block and a long agent-written description, so running the flow meant scrolling past the YAML, reading the steps meant scrolling past the description, and there was no way to watch a run from the page it was started on.

- **Collapsed by default**: the description clamps to two lines with a Show more/less toggle (a character/newline test, not a measured height, so the toggle never flickers); the YAML sits behind a disclosure whose header names its line count and keeps Copy working while closed.
- **Reordered**: one full-width column — header, Run, live run, steps, recent runs, YAML, validate — instead of two columns whose right-hand side buried the Run button under the source. The two-column split existed to hold the YAML; collapsing it removed the reason.
- **Run in place**: `POST /v1/flows/{id}/run` answers only when the run is over, so `RunPanel` no longer navigates on submit. It hands the lifecycle to the page, which learns the run id from `run.started` and each step's status from `run.step` while the request is still in flight (`StoredEvent` now carries `status` as its own field). The panel shows status, `done/total` steps with the step currently executing, a progress bar, elapsed time, assertion counts, the run's error, and an "Open run details →" link that works as soon as the id is known; the final `Run` from the response is authoritative for step statuses. Each step's card shows its own status pill while the run is going. When the event socket is not open the panel says so instead of looking stalled.
- Bundle unchanged in practice: initial chunk 65.0 KB gz, whole app 208.7 KB gz (budget 120 / 300); 75 UI tests green.

Not yet checked in a real browser against a live daemon; the behaviour is covered by tests that drive the event store directly.

## v1.0.1 through the release pipeline (2026-09-06)

First release cut by the tag-driven workflow rather than by hand: windows dropped from the goreleaser targets (Unix-only daemon syscalls), tag `v1.0.1` pushed, goreleaser produced the four archives, `checksums.txt`, and the multi-arch `ghcr.io/gs-sinha/sapien` images. The Homebrew tap (`gs-sinha/homebrew-tap`, created the same day with a hand-written v1.0.0 formula) was bumped by hand and verified with `brew install` in the `homebrew/brew` image; `docs/releasing.md` is the runbook, including the tap wiring that is still manual and why the formula keeps an explicit `version` line.

## Multiple workspaces, one daemon (2026-09-06)

Asked for before team workspaces are built as a construct: the ability to hold several workspaces and switch between them from the UI, the CLI, and MCP. The CLI could already switch per invocation (`--workspace`, `$SAPIEN_WORKSPACE`, the upward search, `default_workspace`); the daemon, the UI, and MCP each bound one workspace for their lifetime.

Two facts decided the shape. `engine.Local` was already fully self-contained per workspace — its own database handle, catalog, search, bus, syncer, watcher, git manager and semantic worker, with no package-level state — so N engines in one process was already safe. And cookies are not isolated by port (RFC 6265 §8.5), so the alternative (a daemon per workspace, the UI hopping between ports) would have had two daemons on `127.0.0.1` overwriting each other's `sapien_session`: switch, and the tab you left starts 401ing. One daemon holding many workspaces also makes the 2026-09-06 two-daemon reindex failure structural rather than defended against, since one process holds every open workspace's lock.

- **`internal/workspaces`**: a `Manager` mapping workspace directory to engine, opened lazily on first use, acquiring that workspace's `daemon.lock` as it opens and releasing it on Close. The primary (the one `serve` opened) is adopted, never reopened, and its lock stays with `serve`. `List` merges open workspaces with the registered ones, and reports a registered workspace that no longer loads *with its error* rather than dropping it from the picker.
- **`internal/config`**: `workspaces:` in the user config, beside `default_workspace`, written with the same preserve-every-other-key merge. Opening a workspace registers it, so a directory reached once by `--workspace` is offerable later without a separate add.
- **`internal/server`**: `workspaceMiddleware` resolves each request's target to an engine and puts it in the request context; the 65 `s.engine.` handler call sites became `engineFrom(r.Context()).`, so no handler carries an error path for "which workspace?" — the middleware rejects an unresolvable one first. A single-engine server (every test, any other embedding) still works, and refuses a header naming a workspace it does not have rather than silently serving the wrong one. New: `GET`/`POST /v1/workspaces`.
- **`engine.Remote`**: `WithWorkspace(dir)` sends the header on every request and on the events WebSocket handshake.
- **MCP**: `list_workspaces` and `switch_workspace`, registered only when a `WorkspaceSwitcher` is present. The bound engine moved behind `s.engine()` under a mutex, so a switch takes effect on the next call with no other tool schema touched. The daemon passes the `Manager`; a stdio `sapien mcp` passes a switcher that opens a second `Remote` client against the same daemon with a different workspace header; `SAPIEN_NO_DAEMON=1` gets a local `Manager`, so switching does not silently vanish on that path.
- **UI**: a picker at the top of the nav. The selection lives in a zustand store plus a module variable that `api/client.ts` reads on every request (a header builder cannot subscribe to a store), persisted in `localStorage`, and deliberately not in the URL — a workspace is the frame every route is read in, not part of any path. Switching flushes the event buffer, reconnects the socket against the new workspace, and reloads: ids from one workspace never resolve in another, so anything held from before the switch is wrong. The primary is stored as `""` so a tab follows the daemon rather than pinning a path.
- **CLI**: `sapien workspace list | current | use | add | forget`; `use` accepts a name from the listing as well as a path. `list` includes the workspace you are standing in whether or not it was registered.

Verified against a real daemon, not only in tests: one process (pid 81999) held both workspaces' locks, each got its own database, a memory written through the header landed in that workspace's own `memories/` and was invisible to the other, `?workspace=` (the form the browser's WebSocket must use) selected the same way, an unknown workspace returned 404, and both locks were released on shutdown. 92 Go test files pass, 81 UI tests, initial chunk 65.9 KB gz / whole app 209.6 KB gz against the 120/300 budget.

Not done here, deliberately: no idle-close of an opened workspace (a daemon holding ten workspaces holds ten watchers until it exits), and no per-machine override of a committed workspace file's service sources — the thing that would let a team workspace use git sources while a developer shadows one service with a local checkout. That override is the next piece before team workspaces are worth building.

## A stale managed clone made `service add` fail forever (2026-09-06)

Reported from real use as `no API package found under ~/.sapien/repos/<hash>-<repo>/api`. It read like a misconfigured `subdir`, and was not: it was a 34-second race.

Sapien cloned the service's repo at 14:16:21, when its branch tip was four days old and carried no `api/`. The commit that adds the package was pushed at 14:16:55 — thirty-four seconds after the clone. The error was correct at the instant it was printed, and then became permanently wrong: `service add <url>` without `--name` first builds the service once to derive its name from the contract's title, and that pass resolves a git source with `gitsrc.Ensure`, which clones if missing but deliberately never fetches. So every retry re-read the same stale tree and failed identically, with nothing in the message pointing at the clone. Passing `--name` happened to work, because that skips the pre-flight and goes straight to the syncer, which does fetch — a difference in behaviour between two spellings of the same command that nobody could be expected to guess.

- **`Builder.WithGitFetch`**: resolve a git source with `Sync` instead of `Ensure`. `Ensure`'s no-network promise is right for the staleness check and the watcher (opening the engine must never touch the network) and wrong for a build a person is waiting on. `serviceAPI.Add`'s name-derivation pass opts in; `staleCheck` and the watcher are untouched.
- **`Builder.WithGitSynced`**: the syncer fetches itself and then builds, so it must not fetch twice — but a failure there is not a staleness problem and must not be explained as one.
- **The error now describes the clone**: url, ref, commit, and `current`. A clone that has not been fetched reports when it last saw the remote and points at `sapien service sync` or deleting the clone; one that was just updated says the commit really does not carry the package, and suggests `--subdir`/`--contract`. `gitsrc.LastFetch` reads `FETCH_HEAD`'s mtime (git records no fetch timestamp of its own), falling back to the clone's own creation time, since cloning is itself a fetch of everything.

Two bugs in the first cut of this, both caught by tests written against the real scenario rather than the intended one: `Sync` on a *missing* clone just clones and returns, so a fresh, perfectly current clone was being reported as "never fetched"; and the syncer's own build was blaming staleness immediately after fetching. The freshness signal is now supplied by the caller, which is the only thing that knows.

Verified on a copy of the actual stale clone from the report: the exact command that failed now fetches forward and ingests the service's operations, status ok. Hermetic tests in `internal/registry` build the scenario from a local bare repo — clone before the push, then push the package — and pin both the old failure (without a fetch it still cannot see `api/`) and the fix.

### Path case made one workspace look like two (2026-09-06, same day)

Reported as "i am seeing three workspaces" right after the workspace move, with only two registered. The daemon had been started with a `--workspace` path spelling `Desktop` in lowercase -- the same directory as the registry's copy, on a case-insensitive filesystem. `GET /v1/workspaces` returned three entries, two of them the same workspace with `open: true`, so the daemon had opened one directory twice: two `engine.Local`s, two SQLite handles, two watchers. That is the failure the workspace lock was built to prevent, arriving by a route the lock cannot see -- both openers share a pid, so each takes the lock from the other as if reclaiming its own.

The manager and the registry now decide identity with `os.SameFile` (device and inode) rather than string equality: right on a case-insensitive filesystem, and without merging genuinely distinct paths on a case-sensitive one. `Manager.Engine` reuses an open engine reached by another path, `List` collapses duplicates and resolves `primary`/`open` the same way, and `AddWorkspace`/`RemoveWorkspace`/`KnownWorkspaces` dedupe on the same basis. A path that does not resolve falls back to string comparison, so a deleted workspace stays listed with its own error instead of quietly merging into another row.

The first tests written for this were vacuous -- they used a trailing separator and a `.` segment, which `filepath.Clean` collapses before `os.SameFile` is ever reached, so they passed with the fix stubbed out. They now use a symlink, which survives cleaning as a different string, and were checked by disabling `sameDir` and confirming they fail.

The first fix caught two of the three places that compared workspace paths as strings, and the user found the third: `sapien workspace list` merges the registry with the workspace the cwd resolves to, and tested membership with `==`. Run from a lowercase-`desktop` cwd it listed that workspace twice and marked the wrong row current -- so the UI showed two and the CLI showed three, from the same machine at the same moment. The comparison now exists once, as `workspace.SameDir`, used by the manager, the registry, and the CLI; the two local copies were deleted rather than left to drift. Each of the three tests was checked by reverting its call site to `==` and confirming the test fails.

## Discovery framing, coverage, and payloads (2026-09-12)

Five observations from real use, four of them about the same thing: Sapien
was serving structure and leaving the meaning behind.

- **Agents had the tools without the framing.** Nothing told an agent that
  Sapien is a cross-repo discovery layer, so it read schemas here and then
  called services from its own scripts. New topic
  `get_dsl_reference("sapien")` (and `sapien://reference/sapien`): what
  Sapien holds, how to consume a service you do not own, why `execute_api`
  beats curl (environment-resolved base URL/headers/secrets, a recorded
  run, a diagnosed failure, one step to save the payload for the next
  agent), where onboarding lives, where memories fit, what Sapien does not
  do. The MCP `instructions` open with the same sentence; its budget went
  from 1,300 to 1,750 characters, which is the only place in this change
  where every session pays.
- **`soft: true` was invisible to the agents that needed it.** It shipped
  in the runner, `spec/flow.schema.json` and `docs/flows.md` -- but not in
  `internal/flow.Reference()`, the text agents actually read -- so they
  rediscovered it from failed runs. Documented now, with the `expr:` object
  form and the range keys; `TestReference_DocumentsEveryDSLKey` reflects
  over every YAML tag in `domain.Flow`/`Step`/`Assertion`/`Poll`/`Range`
  and fails if the reference does not mention it (verified by stubbing
  `soft` out and watching it fail). The line budget went 240 -> 280.
- **Payloads existed everywhere except where they were needed.** `get_api`
  returned example *ids*; the UI had a manual "From schema" button that
  emitted required-fields-only placeholders in TypeScript and ignored the
  contract's own `example:`. One resolver now answers "what does a call
  look like?" for everyone: `internal/example.Resolve` picks a verified
  saved example, else a hand-written one, else the contract's `example:`,
  else a payload synthesized from the schema (required fields plus any
  field the contract gives a value for; `readOnly` excluded; `?fields=all`
  for the whole shape), labelled with its source and a note on how much to
  trust it. Served by `get_api.request_example` at every detail level and
  by `GET /v1/operations/{id}/example`. The UI's Try It form opens
  prefilled with a source banner, and `ui/src/pages/try/skeleton.ts` was
  deleted rather than left to drift from the Go implementation.
- **"The docs were lacking" was the common feedback, and it was a framing
  problem.** `reference_service.md` described a format; agents wrote for a
  reviewer of the repo. It now opens with who reads the result (an agent in
  another repository that cannot see the code), what the three layers are
  for, and a documentation checklist of what a section must answer that the
  contract cannot: why it exists and who calls it, preconditions, business
  rules, what it changes, idempotency and retries, every error code and
  what to do about it, timing, deprecations, and the traps.
- **Some of it is in nobody's code**, so onboarding asks. One batched round
  *after* drafting (a cold "tell me about your service" gets an unusable
  paragraph; "`allocate` has no idempotency key and no unique constraint on
  `order_id` -- is calling it twice safe?" gets an answer worth writing
  down), with a question bank at service/subproject, operation, and
  cross-service level, a rule against asking anything the code answers,
  and `## Open questions` for what comes back unknown -- a gap named is
  worth more than a plausible guess the reader cannot distinguish from a
  fact.
- **Coverage is now a number, because a clean contract read as
  completeness.** `registry.coverage` counts operations the narrative docs
  reach (by operation id or by an endpoint mention; contract-derived tag and
  `info` docs deliberately do not count, or every service would be fully
  documented by construction) and bodies with an example, and emits
  `NO_NARRATIVE_DOCS` (once, not per operation), `UNDOCUMENTED_OPERATION`,
  `MISSING_REQUEST_EXAMPLE`, `NO_CONCEPTS`, capped at 20 per code with an
  aggregate line. They join the contract's warnings *before* acceptance, so
  an endpoint nobody outside the team should call can be accepted with a
  reason -- the 2026-09-06 "agent rewrote 20 text/plain responses to chase
  zero warnings" incident is the reason this had to be acceptable rather
  than mandatory. `domain.Service.Coverage` rides in the existing
  `services.doc_json` blob, so no migration.

Three existing registry tests asserted exact warning counts on fixtures
with no docs and no concepts; they now filter by the code under test, which
is what they meant. Go suite and `go vet` clean, 97 UI tests green.

Not done: coverage says nothing about doc *quality* (a section that
mentions an operation and says nothing useful counts as documented), and
nothing re-asks the interview questions when the code changes under a
service that was onboarded before this.

## Opening the UI without a terminal, and keeping tabs alive (2026-09-12)

Asked as "how complicated would a desktop client be", and narrowed by the
user to the two things actually wanted: multitasking across tabs, and a way
to launch that is not `sapien ui` in a terminal. Neither needed Tauri, and
one argued against it -- a Tauri shell is a single window, so tabs would
have had to be rebuilt inside it, and it would have added a second artifact
that drifts from the engine binary. Phase 7c stays deferred; PLAN §27 and
the `tauri://localhost` origin allowlist are untouched and still cost
nothing.

What the investigation turned up, which decided the shape of all of it: the
daemon bound a random port and `serve` mints a fresh bearer token on every
start, so nothing static can point at it. A bookmark, a Dock URL or a Chrome
PWA install is stale as soon as the daemon idle-exits -- after thirty
minutes with nothing connected, i.e. exactly when you would next reach for
the launcher.

- **`sapien ui --install-app`** (`internal/cli/ui_installapp.go`) writes
  `~/Applications/Sapien.app`: an `Info.plist` and a `/bin/sh` launcher that
  runs `sapien ui`, which re-resolves the port and mints a fresh session
  every time. It never needs updating, because the UI is embedded in the
  binary (`internal/ui/embed.go`) and `brew upgrade` replaces that binary at
  the same path. `os.Executable()` is deliberately not passed through
  `filepath.EvalSymlinks`, and this was checked rather than assumed: launched
  through `/usr/local/bin/sapien` it records the symlink, not the Cellar
  path a `brew upgrade` would delete. A Finder launch inherits none of the
  login shell's `PATH` (so the binary is named absolutely, with a thin
  `command -v` fallback) and has no terminal (so failures go to
  `~/Library/Logs/Sapien/launch.log` and one `osascript` alert).
  `removeExistingBundle` refuses to recursively delete anything at that path
  that is not carrying our launcher.
- **The daemon binds 7717 by default**, falling back to an ephemeral port
  when it is taken and honouring an explicit `--port` exactly. The stable
  origin is what makes a set of tabs survive an upgrade: one relaunch mints a
  cookie every tab at that origin shares, so each comes back on reload.
  Verified live, including the fallback: a second workspace's daemon logged
  the reason and took 61443.
- **The workspace picker is per tab** (`sessionStorage`, with `localStorage`
  as the seed for a brand-new tab). It was a module variable loaded from
  `localStorage`, so tab A's switch moved tab B on its next reload and every
  id in tab B's URL then resolved in the wrong workspace. `""` is stored as
  a real choice rather than by removing the key -- removing it would fall
  through to `localStorage` and reintroduce exactly the bug.
- **A missing hashed asset is a 404** (`internal/ui/ui.go`). The SPA history
  fallback answered every unknown path under `/ui/` with `index.html`,
  including the content-hashed chunks an upgraded daemon no longer has, so a
  dynamic `import()` got `text/html` and failed as a MIME type error.
- **A banner when the tab has outlived its daemon** (`state/daemon.ts`,
  `components/DaemonBanner.tsx`). `/v1/health` is the one route outside
  `authMiddleware`, so it answers with a stale cookie and without a workspace
  header; it is probed on load to record which build served the tab, and
  again only once the event socket has failed to reconnect twice -- the "no
  polling" rule holds, since it fires only while reconnection is already
  failing. A different version means an upgrade replaced the daemon (reload);
  no answer means it idle-exited (relaunch). Previously both were silent:
  every request failed with a network error while the socket retried forever
  against a port nothing was listening on.

The banner work turned up a third state while it was being verified in a
real browser, and it is the one that was hardest to recognise from the
outside: the daemon reachable *and* the right build, while the tab's cookie
belongs to a previous one, because `serve` mints a new token on every start.
`/v1/health` is unauthenticated, so the status bar showed a green daemon dot
beside an app whose every request 401ed. `api/client` now reports a 401 to
the same store and clears it on the next success; the banner says to run
`sapien ui`, whose own `/ui/session` tab re-cookies the whole origin. The
status bar's dot follows the probe too -- it was a one-shot check on mount,
so it stayed green next to a banner saying the daemon was gone.

The icon is 🗿, asked for mid-build: `ui/public/favicon.png` for the browser
tab (a PNG, not an SVG data URI -- Safari does not render SVG favicons) and
an `.icns` embedded in the binary and written into the bundle's Resources,
both rendered from the same glyph so the Dock tile and the tab match.

Verified against a real daemon rather than only in tests: 7717 bound, the
fallback taken and logged, `/v1/health` answering unauthenticated, a missing
hashed asset 404ing while `/ui/runs/run_123` still gets the shell, and the
installed bundle launched from Finder starting the daemon and opening the
default browser. All three banner states were driven end to end in Chrome
against a live daemon -- replaced (rebuilt at v9.9.9 on the same port), gone
(daemon killed), and stale session (restarted, new token) -- along with the
recovery the stable port exists for: one `sapien ui` and a reload brought the
already-open tab back to a live event stream. 111 UI tests, the Go suite and
`-race` on the four changed packages green; initial chunk 66.9 KB gz, whole
app 211.2 KB gz against the 120/300 budget.

Not verified, and the one thing left open: a cookie-authenticated mutation
from Safari. `middleware.go`'s CSRF check requires `Origin` to be *present*
on any non-GET that authenticated by cookie, and while modern WebKit sends
it on same-origin requests, older WebKit did not -- if any browser omits it,
every save, run and delete 403s while reads work fine. Safari was confirmed
to load the app and hold six connections open, which proves the cookie
exchange, the shell and the sockets; the mutation path needs either a click
or a `Sec-Fetch-Site: same-origin` fallback in the middleware, which was not
added here because it changes a security-sensitive path and was outside what
was agreed.

## Team workspaces: one committed composition, per-machine bindings, tiers (2026-09-14)

Reported from real use of the `team` workspace built on 2026-09-06 (every
service a git source, the workspace itself a repo a new hire clones): "git
linked repos get lost with every sync tick and untracked don't get synced",
and the new hire "can't write to it". Traced before designing anything: a
git source is read from a managed sparse clone under `~/.sapien/repos`
that the daemon `reset --hard`s every ten minutes, so a doc promotion
written into it was reverted, a memory written into it was stranded where
nothing pushes from, and a colleague's uncommitted knowledge was invisible
by construction. Meanwhile the user's own knowledge was split across two
workspaces listing the same seven services, one as git sources and one as
local paths -- the shadow pattern, done by hand.

The user set the product frame: a developer and their agents discover
organizational context through APIs, docs and flows, and contribute to it
with local changes reaching their own catalog instantly and teammates'
changes arriving as soon as the code hits GitHub. Both propagation paths
already existed (the watcher, the git timer); the failure was forcing each
service to be one or the other for everyone. PLAN §7b records the model:
the team commits the composition, each machine binds the services it is
working on, and contributions ride the developer's own pull request --
knowledge agents consume needs the same review gate as code, so Sapien
never commits or pushes.

Built with the shared contracts laid first (domain types, engine
interfaces, fakes, `workspace.Save` writing the committed source) so four
agents could work in parallel on disjoint files:

- **`sapien.workspace.local.yaml`** (`internal/workspace/local.go`),
  gitignored, binds a service name to a local checkout; `Load` applies it
  so every path sees a local source, `ServiceRef.Team` keeps the committed
  one, and `Save` writes `Team` back -- the invariant the tests pin is that
  a committed file never acquires an override path. `sapien service bind |
  unbind`, `PUT|DELETE /v1/services/{name}/binding`, and the service page's
  Source panel drive it; `GET .../binding` lists candidate checkouts found
  by matching `origin` (normalized) against the team URL across every
  registered workspace, which is how the old hand-built split becomes one
  click. Binding restarts the watcher so the checkout is watched at once.
- **Read-only clones.** `memory.Locator` and `example.Locator` refuse
  service scope for a service read from its git source, with a hint to bind
  or use workspace scope; the flow tier does the same. As insurance,
  `gitsrc.Manager.Sync` now refuses to reset a clone with modified tracked
  files and names them -- the registry test that asserted the old
  "fetch discards edits" behaviour was rewritten to assert the refusal.
- **"Listening to" is visible.** `Service.Binding` records mode, the
  committed source, and for a checkout its branch, commit, origin and
  uncommitted count from read-only git queries (`gitsrc.Manager.Describe`);
  `service list` gained a READS column, MCP `get_service` a `reads:` line,
  the services page a "Reads from" pill.
- **Flow tiers.** New flows land in `local/flows` (self-ignoring
  `local/.gitignore`, this machine only) by default; `Flows().Rescope`
  moves a flow to `flows/` (the team repo) or `api/flows` of a bound
  service, keeping the name and reindexing both owners; `scope: flow`
  memories follow their flow into `local/memories`. `sapien flow create
  --scope`, `flow promote`, `rescope_flow`, tier column and chips on the
  flows page, promote controls on the flow page. Memories keep their
  existing ladder (personal -> workspace -> service), which was already the
  same idea; the UI just names the rungs. The acceptance scenario now
  creates local and promotes to workspace before running.
- **`report_friction`**, asked for mid-build: an agent files feedback about
  Sapien itself; it is queued under `~/.sapien/friction` (secrets refused,
  since it goes public), reviewed with `sapien friction list | show`, and
  posted as a GitHub Discussion by `sapien friction send` through `gh`.
  HTTP routes and a Friction page give the UI the same review-then-post
  loop. Discussions are not yet enabled on `gs-sinha/sapien`; `send` says
  so with the setting to flip.

Verified against the real `team` workspace with the dev binary, not only
in tests: binding one git-sourced service to its checkout on this machine
wrote the override (ignored), created `.gitignore`, left
`sapien.workspace.yaml` untouched (`git diff` empty), and the listing read
`local stage`; `GET /v1/services/{name}/binding` on a dev daemon returned mode team
for a second service with its checkout as a candidate, matched through the
other registered workspace where it is a local source; the service page in Chrome
showed "listening to local · branch stage · 74809c0 / can listen to team ·
stage" with the Read-from-team-source button, and the flows page its TIER
column and chips; `unbind` restored `team stage` and removed the file. Go
suite green with `-race` (38 packages, `go vet` clean), 131 UI tests, bundle
67.3 KB initial / 217.2 KB total gzipped against the 120/300 budget.

Not done, deliberately: fetching the workspace repo on the tick to show
"behind by N", and a read-only unshipped-files view -- both wait until
bindings have been used for a while. One rough edge seen live: a one-shot
`service bind` logs `semantic: list operations failed ... context canceled`
because the CLI exits while the semantic enqueue is in flight; harmless,
pre-existing for `service add`, worth silencing.

### Validated binding, the checkout picker, add-from-checkout, and shipping state (2026-09-14, same day)

Three follow-ups from the user's review of the first cut, each a question
before it was a request. "How are we autodetecting the path of the local
repo?" -- from the local sources of registered workspaces, matched by
origin, which meant the candidate offered for one service was a directory
whose name said it was a backup. "If a new hire clones the team repo and
writes a service, does onboarding work?" -- only the clunky way: push first,
add the URL with a name, then bind; a plain `service add <path>` would have
committed an absolute local path into the shared file. And "when I promote
a flow to team, that is basically a commit, right?" -- no, it was a move
into `flows/` that nothing recorded. The user chose a daemon-side browser
with remote validation over the working-directory-only alternative I had
argued for, because MCP lets an agent do the same from an instruction, and
asked for the shipping state plus an opt-in commit.

- **Binding is validated.** `BindWith` refuses a checkout whose origin
  names another repository or which has no API package unless forced; an
  empty name infers the service from the origin, so `sapien service bind
  .` from inside a checkout is the common case. `gitsrc.DescribeAgainst`
  adds last commit time, ahead/behind the team ref as of the clone's last
  fetch, and whether it is a worktree, so a stale clone reads as stale.
- **The picker is daemon-side.** `GET /v1/services/{name}/checkouts?path=`
  lists one directory level; each git repository is annotated with whether
  its origin is this service's (a cheap `.git/config` read, the full
  description only for a match). The Source panel's Browse dialog descends
  into folders, marks matches, greys the rest with "clone of <origin>", and
  offers "bind anyway" for forks. Over MCP: `bind_service`,
  `unbind_service`, `find_checkouts`.
- **`service add` is team-aware.** In a shared workspace -- one whose
  workspace file is tracked by a repository with a remote, not merely a
  folder inside some repo, which is what keeps the README quickstart
  adding plain local sources -- a checkout path commits its origin as a git
  source and binds the checkout here (`AddFromCheckout`). A path that is a
  subdirectory of its repository falls back to a local add unless `--team`
  asks for a source with `subdir`. `add_service` follows the same rule.
- **Promotion says whether it shipped.** Workspace-tier flows carry
  `shipped` from one `git status` and one `git log @{upstream}..HEAD` over
  the workspace repo: not committed, modified, not pushed, shipped. Shown
  on the flows page, in `flow list` and `list_flows`. `flow promote
  --commit` (and the "and commit" checkbox, `rescope_flow` with `commit`)
  commits the moved file, only that file, only into the workspace repo,
  never a push.

Verified live with the dev binary: from inside a real checkout, `service
bind .` inferred the service and read `local stage`; binding it to a
sibling checkout of a different service was refused naming both origins;
browsing the parent folder of the checkouts through the daemon returned
each sibling repository with its branch and origin and exactly one
`matches: true`. In a throwaway shared workspace with its own bare origins:
`service add <clone>` committed `type: git` with the origin URL and bound
the clone, the catalog indexed from the working copy before any push;
`flow promote --commit` produced one commit ("Promote flow smoke-flow to
the team workspace") and `flow list` read `not pushed`, then `shipped`
after the push. Go suite green with `-race` (38 packages, vet clean), 149
UI tests, bundle 67.5 KB initial / 220.1 KB total gzipped.

Two seams worth knowing: `rescope_flow`'s MCP text reads the commit sha
with a direct `git rev-parse` because `FlowAPI.RescopeWith` returns only
the flow, and the flow page fetches the ship state through a second
`list_flows` lookup because a single `Flow` does not carry it. Both are
cheap and both would be cleaner with the sha and the state on the engine's
own return values.

### Commit without a detour, and the workspace repository joins the tick (2026-09-14, same day)

Two more from using the first cut. Committing a flow that already sat at
the team tier meant moving it back to local and promoting it again with
the checkbox -- a workaround, not a workflow. And "does the workspace
itself get scanned once in a while?" -- no: the tick fetched every
service's source and never the workspace repository, so a teammate's
pushed flow waited for someone to remember `git pull`, and nothing said
it was waiting; the user's own team repo turned out to be four commits
ahead of origin with nobody told.

- **`Flows().Commit`**: one commit of one team-tier flow's file, refused
  for the other tiers, for a workspace outside git, and when there is
  nothing to commit; the summary comes back `not pushed`. `sapien flow
  commit <id>` and `--all`, `POST /v1/flows/{id}/commit`, `commit_flow`,
  and a Commit button beside the `not committed` and `modified` badges.
- **`Repo()` on the engine**: Status from refs on disk (branch, upstream,
  behind/ahead as of the last fetch, uncommitted count, when it last
  fetched); Fetch, read-only; Pull as `merge --ff-only`, refused with the
  reason when the tree is dirty, the branch tracks nothing, or the
  branches diverged, and followed by a reindex of the workspace tier;
  Sync as fetch-then-pull-when-clean that never fails for a skipped pull
  but says why. The daemon fetches on the same tick as the services and
  never pulls there, because the developer's uncommitted work is theirs;
  a fetch failure is logged once per distinct message. "Sync all",
  `sapien service sync` and `sync_service` include the repository;
  `sapien workspace status|pull|sync` and the four `/v1/workspace/repo`
  routes expose it; a `workspace.repo` event feeds the status bar, which
  shows `team · main`, `↓N new`, `↑N unpushed`, `N uncommitted`, and a
  Pull button only when the tree is clean.

Verified live. On the real team workspace `sapien workspace status`
read "you have 4 unpushed commits, 4 uncommitted files, last fetched 35m
ago"; `sapien service sync` synced every service and ended with "team
repo: not pulled: uncommitted changes (4 files)", after which status read
"last fetched just now". In a throwaway shared workspace with a bare
origin and a second clone: the teammate's pushed flow showed after
`workspace sync` as "pulled 1 commits" and listed as `team · shipped`; a
new file dropped into `flows/` listed `not committed`, `flow commit`
produced "Add flow mine to the team workspace" and the listing moved to
`not pushed` with status saying "1 unpushed commit"; a scratch file in
the tree made `workspace sync` report "not pulled: uncommitted changes (1
files)" and `workspace pull` refuse with E_CONFLICT and the commit-or-
stash hint. Go suite green with `-race` (38 packages, vet clean), 174 UI
tests, bundle 68.5 KB initial / 220.8 KB total gzipped.

A small follow-on from the user noticing the Agent tab's directory
list: it offered every service's package directory, which for a
team-source service is the managed clone under `~/.sapien/repos` -- an
agent started there would have been writing into a cache the daemon
resets. `terminalDirs` now lists only writable places: the workspace,
each locally-read service at its checkout (a bound service at the path
it is bound to), and home; the `api/` entries went too, since the
repository is where an agent works.

### The first friction report, and what it found (2026-09-14, same day)

Within an hour of `report_friction` existing, an agent (codex, over the
stdio bridge) filed one and the user posted it as
github.com/gs-sinha/sapien/discussions/1: `switch_workspace` to the team
workspace returned E_INTERNAL with an HTTP 401 while the CLI could read
that workspace fine, the failed switch left the session on the previous
workspace so the next `create_memory` landed in the wrong catalog, and
there was no `delete_memory` to clean up with.

Traced in `internal/cli/mcp_workspaces.go`: the bridge's switcher kept
the bearer token captured when the session started, and its raw HTTP
calls never re-resolved it, while the primary `engine.Remote` reconnects
on a 401 through its resolver. The daemon had been restarted at 19:48;
the agent's session predated that. `list_workspaces` masked the failure
by falling back to the local registry on any error. Fixes: the switcher
resolves the endpoint on demand and replays once on a 401 or connection
failure, exactly as `Remote.do` does, and decodes the daemon's error
envelope so an agent sees E_CONFLICT rather than "HTTP 409"; a failed
switch now says "this session is still bound to <previous>" and carries
`still_bound_to`; every write names the workspace it landed in;
`delete_memory` exists with write-memories permission. Tests: a stub
daemon that rotates its token proves List and Engine recover; the MCP
tests pin the still-bound text and the delete.

### "Daemon very much alive" (2026-09-14, same day)

Asked to shut every workspace but `team` down. Stopping the personal
workspace's daemon was not it: two MCP bridges from earlier agent
sessions were still bound to it, and the team daemon -- one process,
many workspaces -- had opened it on their behalf and held its lock.
Stopping the bridges and restarting the daemon was not it either: a
browser tab still carrying that workspace's header reopened it on its
next request, because `workspaces.Manager.Engine` opened whatever
directory a header named and registered it as a side effect. There was
no way to close a workspace short of stopping the daemon, and `workspace
forget` only edited the user config.

Now a request header opens only the primary or a registered workspace
(errs.WorkspaceNotFound otherwise); `Manager.Register`, behind `POST
/v1/workspaces`, is the explicit open that also registers, which is what
the MCP bridge already called; `Manager.CloseOne` closes one engine and
releases its lock, behind `DELETE /v1/workspaces?dir=` and `sapien
workspace close`; and `sapien workspace forget` closes it on the running
daemon as it unregisters, so "forgotten" means "stays closed". Verified
live: after `forget`, the daemon listed the workspace closed, its lock
file was gone, and a request carrying its header got 404 without
recreating the lock. Six existing tests that relied on the implicit open
now register the workspace first, which is what they were modelling.

### Three stages for memories and examples, and Push (2026-09-14, same day)

The user saw one flow read "committed, not pushed" and could not act on
it from the product: the only way to push was a terminal in the repo.
And memories and examples still had scopes but no stages. Decided: the
same ladder for all three kinds -- local, committed, pushed -- with
every step possible from the UI, push included. Push is the workspace
repository's own branch to its upstream, on request, never forced,
refused when the branch is behind, and never a service repository, so
the one rule that mattered ("Sapien never pushes on its own") still
holds; what changed is that the human's push no longer needs a shell.

- **Tier on disk.** A workspace-scope memory or example lives in
  `local/memories` / `local/examples` (this machine, ignored) or in
  `memories/` / `examples/` (the team's), and the tier is read from the
  path -- no column, no migration. A new one lands in the local tier, as
  a new flow does, and `Update` keeps whatever tier it already has, so
  editing a team memory's text cannot silently pull it back to local.
  `Move` changes the tier through the store's own rewrite, so the old
  file goes as the new one lands.
- **Shipped and Commit** for memories and examples exactly as for flows:
  one `git status` and one `git log @{upstream}..HEAD` per listing over
  the workspace-tier paths; `Commit` on one file with a default message.
  `sapien memory move|commit`, `sapien example move|commit`, the rescope
  tools with `tier`, `commit_memory`, `commit_example`, badges and
  buttons on both pages.
- **Push.** `gitsrc.PushRepo` sets the upstream when the branch has none
  and never forces; `Repo().Push` refuses when behind ("pull first") and
  is a no-op when nothing is ahead. `sapien workspace push`, `POST
  /v1/workspace/repo/push`, a Push button beside every "not pushed"
  badge and in the status bar. No push tool over MCP: pushing is the
  human's, and a test pins that no tool name carries "push".

Verified live on the team workspace after restarting its daemon: 25
memories at the local tier, 4 written by agents earlier at the team tier
reading "not committed", 3 examples the same, one unpushed commit and
nine uncommitted files in the status. In a throwaway shared workspace: a
new memory landed under `local/memories`, `memory move --to team` put it
in `memories/` as not committed, `memory commit` made it not pushed with
"Add memory <id> to the team workspace" in the log, `workspace push`
reported one commit and the bare origin had it, and after a second clone
pushed, a push from behind was refused with "pull first". Go suite green
with `-race` (38 packages, vet clean), 196 UI tests, bundle 69.3 KB
initial / 221.9 KB total gzipped. The acceptance scenario now expects a
new example at the local tier and moves it to the team's.
