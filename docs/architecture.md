# Architecture

## Four primitives

Sapien's knowledge model is four layers, each answering a different
question, each reinforcing the next (`Sapien.md` §2):

```
Contracts  ->  What should exist?           (OpenAPI, service-owned)
Flows      ->  How do APIs work together?   (the DSL, see flows.md)
Memories   ->  What have developers learned? (see memory.md)
Runs       ->  What actually happened?        (execution history)
```

## Concept boundaries

Each primitive has one canonical home; knowledge moves between them only
through an explicit, human-reviewed promotion, never automatically
(PLAN.md §36):

| Concern | Lives in | Never in |
|---|---|---|
| What operations/fields exist, formal meaning | OpenAPI (service repo) | memories, flows |
| Narrative documentation | `api/docs/**/*.md`, plus contract `info`/tag descriptions | memories (until promoted), flows |
| Service identity, owners, concepts, env base URLs | `service.yaml` | OpenAPI extensions |
| How operations compose, data passing, assertions | flow files | OpenAPI, memories |
| Learned, operational, environment-specific knowledge | memory files / personal DB | OpenAPI (until promoted), flows (until turned into assertions) |
| What actually happened | run records (DB) | files |
| Which services a workspace composes | `sapien.workspace.yaml` | service repos |

## One binary, three modes

A single `sapien` binary (`cmd/sapien`) is the CLI, the daemon, and the
MCP bridge:

| Mode | Used by | Engine location |
|---|---|---|
| one-shot CLI (`sapien search ...`) | humans, CI | in-process if no daemon is running for the workspace; proxied to the daemon if one is |
| `sapien serve` | desktop UI, MCP over HTTP, `--watch` clients | long-lived daemon; single SQLite writer, single file watcher, live runs |
| `sapien mcp` | agent hosts over stdio | always daemon-backed; starts the daemon if needed |

Every surface — CLI, HTTP API, MCP, and later the desktop UI — is a thin
adapter over one `Engine` facade (`internal/engine`), so anything the UI
can do the CLI/MCP can do too, structurally rather than by discipline.

```
        service repos (local working tree  |  managed git clone)
        api/openapi.yaml  api/service.yaml  api/flows/  api/memories/  api/docs/
                                   |  fs watch / git fetch
                                   v
 +----------------------------- sapien engine (library) -----------------------------+
 | registry -> openapi ingest -> normalized catalog -> SQLite (catalog, FTS5,        |
 |                                                     memory index, runs)           |
 | flow engine (CEL expressions) -> http runtime -> assertions -> run store          |
 | memory store (files + index) -> retrieval/ranking -> agent context builder        |
 | environments + OS-keychain secrets                                                |
 +-----------+------------------------+-----------------------+---------------------+
             | embedded               | localhost HTTP + WS   | MCP stdio / HTTP
       `sapien` CLI (one-shot)  `sapien serve` (daemon)  `sapien mcp`
             |                        |                       |
            CI              desktop UI (Phase 7)   Cowork / Claude Code / Codex
```

## Engine package map

`Engine` (`internal/engine`) exposes sub-services — `Services`,
`Catalog`, `Search`, `Flows`, `Runner`, `Runs`, `Memories`, `Context`,
`Envs` — with an in-process implementation (`engine/local`) and, once the
daemon path is wired, an HTTP-client one (`engine/remote`). No business
logic lives in the CLI, HTTP handlers, or MCP tools; they all call
through this facade.

| Package | Responsibility |
|---|---|
| `internal/domain` | Shared types: Service, Operation, Schema, Flow, Run, Memory, Environment |
| `internal/registry` | Service sources (local, git), discovery, sync |
| `internal/ingest/openapi` | OpenAPI 3.x -> normalized model (libopenapi) |
| `internal/ingest/docs` | Markdown/contract-embedded docs -> sectioned model with ref extraction |
| `internal/catalog` | SQLite catalog + field index |
| `internal/search` | FTS5 queries, ranking |
| `internal/flow` | DSL parse, validate, DSL reference text |
| `internal/expr` | CEL env, `${...}` templating, structured assertions -> CEL |
| `internal/runtime` | HTTP execution, redaction, timings |
| `internal/runner` | Flow execution state machine, polling, cancellation |
| `internal/runs` | Run persistence, retention |
| `internal/memory` | Memory files, index, subject resolution, secret scan |
| `internal/retrieval` | Memory ranking, agent context builder |
| `internal/env` | Environments, secrets (keychain + `SAPIEN_SECRET_*`), substitution |
| `internal/store` | SQLite open, migrations, single writer |
| `internal/server` | Local HTTP API + WS events + auth token |
| `internal/mcp` | MCP tools, resources, permissions, host config |
| `internal/daemon` | `sapien serve` process lifecycle (`daemon.json`) |
| `internal/cli` | Cobra command tree, the thin CLI adapter |

`internal/search`'s ranking is entirely lexical (SQLite FTS5 + bm25 +
additive boosts) and dependency-free — no embedding model, no network call,
no vector index required for any of it (the separate `semantic.enabled`
path, off by default, is the only part of search that isn't). Two things
fold the workspace's other knowledge into that same lexical ranking rather
than leaving it to a human to remember a contract's own words don't say:
`operations_fts` carries two extra columns alongside the standard op_id/
path/summary/description/tags/field ones, `doc_text` (the heading plus the
first 300 characters of every doc section that references an operation,
capped at 2 KB) and `memory_text` (the first 300 characters of every
active memory whose subject names that operation or one of its fields,
same cap), both weighted 2 in the bm25 call and both maintained by
`internal/catalog` (written by `Apply` right after it replaces a service's
docs, refreshed by `engine/local`'s memory-write hooks) rather than by
`internal/search` itself. A `matched_on` result carries `"docs"`/
`"memories"` when a hit came from one of those columns. Second, a small
`search_feedback` table (term, operation, count) learns from use: every
`Search().Operations` call is remembered for a few minutes, and whenever an
operation is actually used afterward (inspected, called, or referenced by a
saved flow) that use is credited back to whichever of the search's own
tokens led to it. The next search for those tokens gets a capped boost
(at most 30% of its own top score, so feedback can win a close race but
never invent relevance from nothing) and `matched_on` gains `"feedback"`.
Both mechanisms are pure SQL over data already in the workspace's SQLite
file — nothing external, nothing that stops working offline.

`spec/` holds the JSON Schemas for `sapien.workspace.yaml`,
`service.yaml`, `*.flow.yaml`, memory front matter, and
`environments/*.yaml`. `fixtures/logistics/` is the three-service
order/allocation/rider sample used by tests and the demo (see
[`../fixtures/logistics/README.md`](../fixtures/logistics/README.md)).

## Process model

A developer utility must not spawn background processes as a side
effect of a one-line command, and CI should be a plain process — so the
one-shot CLI runs the engine **in-process** by default and only talks to
a daemon if one is already running for that workspace. `sapien serve`
starts that daemon (binds `127.0.0.1`, single SQLite writer, single file
watcher, idle-exits after 30 min). `sapien mcp` always requires a daemon
and starts one if needed, so several MCP hosts attached to the same
workspace share one engine, one watcher, and one live-run state. See
PLAN.md §4 for the full rationale and the daemon-vs-hybrid tradeoff table.
