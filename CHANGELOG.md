# Changelog

All notable changes to this project are documented in this file. The
format loosely follows [Keep a Changelog](https://keepachangelog.com/),
and phase numbers refer to PLAN.md §34's roadmap.

## [1.1.0] - 2026-09-06

### Added
- **Multiple workspaces, switchable from the CLI, the UI, and MCP.** One
  `sapien serve` now holds many workspaces instead of one daemon per
  workspace: each request selects one with an `X-Sapien-Workspace` header
  (or `?workspace=` for WebSockets) and the daemon opens it lazily, taking
  that workspace's own `daemon.lock` as it does. Every per-workspace
  on-disk invariant is unchanged — its own `.sapien` state directory,
  database, and lock — so one process still indexes one workspace, and two
  daemons can no longer reindex the same directory.
  - `sapien workspace list | current | use | add | forget` manage the
    registry, kept as `workspaces:` in the user config beside
    `default_workspace`. `sapien init` registers the workspace it creates.
  - `GET /v1/workspaces` lists what a daemon can serve; `POST /v1/workspaces`
    registers and opens another one.
  - The UI has a workspace picker in the nav. Because one daemon serves them
    all, switching stays on one origin and one session cookie — two daemons
    on `127.0.0.1` would have overwritten each other's, since cookies are
    not isolated by port.
  - MCP gains `list_workspaces` and `switch_workspace`; switching rebinds
    the session, leaving every other tool's schema unchanged. Both are
    registered only when there is somewhere to switch to.

### Fixed
- A workspace reached by two spellings of its path is one workspace again.
  On a case-insensitive filesystem `~/Desktop/ws` and `~/desktop/ws` are
  the same directory, and keying open workspaces by the path string let one
  daemon open it twice -- two engines, two database handles, two watchers
  on one directory, which is the double-indexing hazard the workspace lock
  exists to prevent, since both "holders" share a pid and each takes the
  lock from the other. It also showed a phantom extra workspace in the UI
  picker, `workspace list`, and `list_workspaces`. Identity is now decided
  by `os.SameFile` (device and inode) in the workspace manager and the
  registry, which is right on a case-insensitive filesystem without merging
  genuinely distinct paths on a case-sensitive one. A registered directory
  that no longer exists is still compared by string, so it stays listed
  with its own error rather than silently merging with another entry.
  The comparison lives once, as `workspace.SameDir`, and is used by the
  workspace manager, the registry, and `sapien workspace list` -- which
  had the same bug in its own membership check, so running it from a cwd
  that spelled the path differently listed the workspace you were standing
  in twice and put the current marker on the wrong row.
- `sapien service add <git-url>` no longer fails permanently when the
  managed clone predates the service's `api/` package. Registering a repo
  moments before the package was pushed left a clone frozen at the older
  commit, and because the name-derivation pass resolves a git source with
  `Ensure` (which never fetches), every retry re-read the same tree and
  failed with the same `no API package found under <cache path>`. That
  pass now fetches, so `service add <url>` and `service add <url> --name x`
  behave the same on a stale clone instead of only the named form working.
- When a git-sourced package genuinely is not found, the error now
  describes the clone it read: url, ref, commit, and whether that view of
  the remote is current. A clone that has not been fetched says so and
  points at `sapien service sync`; one that was just updated says the
  commit really does not carry the package. Previously the message named
  only a path under `~/.sapien/repos`, which reads as a configuration
  mistake in the one case where it is not one.

## [1.0.1] - 2026-09-06

### Changed
- Flow detail page (`/ui/flows/{id}`) is reordered around what it is used
  for: Run and live run progress at the top, then the steps, recent runs,
  and last the source. The agent-written description is clamped to two
  lines behind "Show more" and the flow's YAML is collapsed behind a
  disclosure that names its line count, so neither stands between the
  reader and the Run button or the step list. Running no longer navigates
  away: the run's status, step-by-step progress (live off the event
  stream), assertion counts, and errors appear in place, each step's card
  carries its status while the run is going, and "Open run details" links
  to the run's own page.
- Module path and distribution names moved from `github.com/growsimplee/sapien`
  to `github.com/gs-sinha/sapien` after the repository transfer: Go import
  path, `go install` target, installer and README URLs, the container image
  (`ghcr.io/gs-sinha/sapien`), the Homebrew tap (`gs-sinha/tap`), and the npm
  package (`@gs-sinha/sapien`). `go install ...@latest` resolves to this path
  from the next tagged release; v1.0.0 archives are unaffected.

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
