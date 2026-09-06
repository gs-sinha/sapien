# Changelog

All notable changes to this project are documented in this file. The
format loosely follows [Keep a Changelog](https://keepachangelog.com/),
and phase numbers refer to PLAN.md §34's roadmap.

## [1.0.0] - 2026-09-06

Phases 0–6 are engine-only and, together, produce a releasable product
for agent-driven use (PLAN.md §34). Status per phase reflects this
repository as documented; see [`docs/BUILD-LOG.md`](docs/BUILD-LOG.md)
for the detailed, running build record.

### Phase 0 — Foundations
- Repo skeleton, JSON Schemas in `spec/` (workspace, service, flow, memory, environment)
- `fixtures/logistics`: three-service sample (order/allocation/rider) with in-memory mock servers
- CI (`go test`/`vet`/`fmt`) and a benchmark harness

### Phase 1 — Catalog
- `sapien service add` (local sources), OpenAPI 3.x ingest (`libopenapi`) into a normalized model
- Documentation ingest by section, with reference extraction to operations/schemas/concepts
- SQLite catalog with FTS5 search over operations and docs
- `sapien describe`, `sapien docs`, filesystem watch, `sapien reindex`

### Phase 2 — Execution
- Environments and OS-keychain secrets (with `SAPIEN_SECRET_*` env-var fallback for CI)
- `sapien call`, the flow DSL, CEL expressions, a validator with suggestions
- Sequential flow runner with polling (`until`/`poll`), structured assertions, run records, redaction
- JUnit report output for CI

### Phase 3 — Memory
- Memory files (Markdown + front matter) and index, subject/reference model
- `sapien memory add|search|list`, structural + lexical ranking
- Agent context builder, `sapien context`

### Phase 4 — Daemon + MCP
- `sapien serve`, the local HTTP API, `/events` WebSocket, `Engine` local/remote split
- MCP server (`sapien mcp`) with the full PLAN.md §23 tool table, permissions, doc/schema tools, DSL reference, server instructions
- Host config helper (`sapien mcp config`)

### Phase 5 — Git sources + promotion
- Managed git clones for services, sync timer
- `get_promotion_target` / `sapien memory promote`

### Phase 6 — Engine release
- Optional semantic search
- GoReleaser packaging, Homebrew tap, `curl | sh` installer, npm/`npx` wrapper, CI Docker image
- Benchmarks in CI, user documentation (this package)

### Onboarding journey (after Phase 6)
- `get_dsl_reference("service")` / `sapien://reference/service` / `sapien flow reference service`: the api/ package layout and onboarding checklist an agent follows to write Sapien-style docs for a service
- `add_service` MCP tool behind a new `write_services` permission class, so an agent can register the service it just documented from inside that service's repo
- `sapien mcp config --scope user|local|project` (default `user`, so the Claude Code entry works in every repo), a `cursor` client, and idempotent `--write` for claude-code
- `sapien service add` resolves relative paths against the current directory; paths inside the workspace are stored relative to it
- `add_service` returns a "## Sapien" section for the repo's `CLAUDE.md`/`AGENTS.md` so future code changes keep `api/` current; the reference and server instructions say to paste it
- `sapien mcp` replaces a daemon left over from a previous build instead of failing with a restart hint
- README "Make it operational", `docs/onboarding.md`, and refreshed getting-started/MCP docs

### Feedback round 1 (after the first real onboarding)
- Warning acceptance: `service.yaml: accepted_warnings` with a required reason; `STALE_ACCEPTANCE`; accepted counts in CLI and MCP; guidance never to silence a warning by misdescribing the wire
- Memory scope rule at the call site (`create_memory`, `memory add`), write-time hints (storage, similar memories, promotion), `memory rescope` / `rescope_memory`
- `sapien env scaffold` and `sapien env probe`; env errors name the services declaring a missing environment; service summaries list declared-but-undefined environments
- MCP permissions hot-reload from `.sapien/mcp.yaml`; the stdio bridge reconnects when the daemon restarts or is recycled
- `add_service` re-syncs an already-registered service; new `sync_service` tool
- Workspace resolution: `$SAPIEN_WORKSPACE` and `default_workspace` (recorded by `mcp config --write`); `sapien init` tips
- Run-failure hints ("Might explain it") from the contract, docs, and memories in `call`, `flow run`, `run show`, `execute_api`, `run_flow`, `get_run`

### Daemon — one per workspace
- Workspace lock (`.sapien/daemon.lock`) held for the daemon's lifetime; orphaned daemons from a replacement are detected and stopped; reindex keeps a flow's row when its file fails to parse; spawned daemons log to `.sapien/daemon.log`

### Flows — from the 58-step field report
- Soft assertions (`soft: true`): warnings, not failures; flips since the previous run are reported
- `run_flow` returns a summary by default (`detail: summary|failed|full`); `validate_flow(path)`; `get_flow(detail: outline)`
- Agent pane is terminal-only

### Flows — cheap iteration (from the 41-step field report)
- `setup:` and `teardown:` step lists; teardown always runs
- Resume and partial runs: `sapien run resume <run-id>`, `flow run --resume/--from/--until`, MCP `run_flow(resume_from, from_step, until_step)`; reused steps are marked and warn when their definition changed
- Round-trippable authoring: `expr:` accepted in assertions, `get_flow` returns YAML, `create_flow`/`update_flow` return summaries, `path` is relative to `flows/` and fenced
- `patch_flow` / `sapien flow patch`: step-level edits preserving comments; `update_flow(path)` re-reads a file edited on disk

### Search — find operations by URL
- Paste a full URL, a path with a base prefix, a path without a leading slash, or `METHOD <url>`: ranked by exact, templated, suffix, parent, and prefix matches, with lexical fallback; `sapien describe` accepts URLs; the UI never treats a path as an intent

### Search — knowledge columns, feedback loop, docs-mediated fusion
- Doc and memory text indexed on operations (`matched_on`: docs, memories); usage feedback boost (`feedback`)
- Docs-mediated second ranker blended 0.7/0.3 by default: Recall@1 0.72 -> 0.80 on the real-workspace harness; `SAPIEN_SEARCH_DOC_FUSION=off` disables
- `experiments/search-eval`: 104-query ground truth, static-embedding study (not adopted), sweep and compare scripts

### Phase 7b — Agent pane
- `/ui/agent` runs `claude`, `codex`, or your shell in an xterm pane over a PTY WebSocket, restricted to the workspace or registered repos; `?prompt=` hand-off from runs
- Flow steps editable in place (input, body, headers) with run-with-edits, save-to-flow, save-as-example; doc path encoding fix; null-safe API client

### Phase 7a — Live inspector UI
- `sapien ui` opens a browser UI served by the daemon at `/ui/` (embedded SPA, cookie session, live events); flows, runs with every step's payload and hints, edit-and-rerun, save-as-example, services with warnings and environments, operations with intent search, operation detail with docs/examples/memories, try-it, examples, memories, events
- Bundle budget enforced in the build (measured 64 KB initial, 118 KB total gzipped); no Electron, no polling
- `GET /v1/events/recent`, `GET /v1/runs/{id}/hints`; flow YAML `source` over the API

### Examples (saved, verified payloads; PLAN §34b)
- `internal/example` store: `<workspace>/examples/*.example.yaml` or `<repo>/api/examples/`, indexed in SQLite; `engine.ExampleAPI` on Local, Remote, and the daemon (`/v1/examples`)
- `verified` recorded only when saved from a real run (`sapien call --save-example`, `sapien run save-example`, MCP `create_example(run_id)`)
- Flow steps reuse one with `example: <id>`; validator suggests ids; explicit step fields override
- CLI `sapien example list|show|add|rm|rescope`, `sapien call --example <id>`
- MCP `list_examples`, `get_example`, `create_example`, `rescope_example`, `delete_example` behind `write_examples`; `execute_api` accepts `example`; `get_api` lists example ids; `get_context` carries an examples tier
- Replay fixes: a `headers:` entry satisfies a declared header param (so `-H`, `-p`, and saved headers are interchangeable); `FromRun` stores header params in `input` under their declared names and drops transport noise; human-mode errors print diagnostics; example files write integral numbers as integers
