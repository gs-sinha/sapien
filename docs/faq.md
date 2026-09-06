# FAQ

## Why not just use Postman / Insomnia / Bruno?

Those are collection managers built around a human clicking "Send" and
reading a response pane. Sapien starts from a different question: an API
already has a source of truth — its OpenAPI contract, owned by the
service repo — so Sapien indexes that instead of asking you to
re-describe every endpoint in a proprietary collection format. Flows,
memories, and run history then layer on top of that same contract, and
every layer is a plain file a human can review and a coding agent can
read and write directly — no proprietary sync format, no workspace lock-in.
The audience is explicitly broader than a human clicking through a GUI:
CI and MCP agents are first-class callers of the exact same engine, not
an afterthought bolted on with a chat panel.

## Why does OpenAPI stay in the service repo instead of living in Sapien?

Because it's the formal, reviewable definition of what an API actually
does, and that belongs next to the code that implements it — reviewed in
the same pull request, versioned in the same history. Sapien treats a
workspace as a *composition* of services (`sapien.workspace.yaml` lists
which service repos you work with), never as their owner. This is also
why promotion (memory -> contract description, PLAN.md §26) is explicit
and human-reviewed rather than automatic: writing into a service's
contract is a change to that service's repo, and Sapien is a guest there.

## Why is the LLM external instead of built into the engine?

Three reasons, in order of how much they'd cost to reverse:

- **Determinism.** The engine's job — ingest a contract, run a flow,
  rank a search, redact a secret — has to be reproducible and testable
  without a model in the loop. An LLM call is neither.
- **No vendor lock-in.** The engine ships no provider client and no API
  key handling. Point any MCP host (Claude Code, Codex, Cowork, or a
  future one) at `sapien mcp` and it authors flows and memories through
  the same tool surface; Sapien doesn't pick your model for you.
  See [`mcp.md`](mcp.md).
- **The validator is the product's natural-language layer instead.**
  Because there's no LLM to paper over a bad tool call, `validate_flow`'s
  diagnostics (line numbers, "did you mean", nearest field paths) carry
  the actual repair loop. That gets real design attention precisely
  because there's nothing softer to fall back on.

A bring-your-own-key authoring panel inside a future desktop UI is
deferred until the external-MCP path is proven (PLAN.md §24, §37) — it's
a UI convenience layered on the same tools, not a different engine.

## What actually runs on my machine?

Everything, until you explicitly point a `flow run` or `call` at a
production environment. `sapien` is one static binary: the one-shot CLI
runs the engine in-process (no background process left behind); `sapien
serve` is an opt-in local daemon bound to `127.0.0.1` with a bearer token
from `daemon.json` (mode 0600) and no CORS; `sapien mcp` talks to that
daemon over stdio. Secrets resolve from the OS keychain or
`SAPIEN_SECRET_*` env vars (CI), never from a committed file. Nothing
phones home — no usage telemetry by default (PLAN.md §30). See
[`architecture.md`](architecture.md) for the full picture and
[`mcp.md#production-safeguards`](mcp.md#production-safeguards) for how
execution against production is gated.

## Do I need a daemon running to use the CLI?

No. `sapien search`, `sapien call`, `sapien flow run`, and friends run
the engine in-process by default and exit cleanly — nothing is left
running. A daemon (`sapien serve`) only starts if you run it yourself,
or as a side effect of `sapien mcp` (which is always daemon-backed, so
several MCP hosts attached to one workspace can share state).

## What if two people (or an agent and a human) edit the same workspace?

Workspace and environment files, flows, and shared memories are plain
files meant to be committed and reviewed like code — conflicts are git's
job, same as any other text file. The SQLite index is a derived cache:
it's always rebuildable from those files (plus your personal, unshared
memories) via `sapien reindex` / `sapien memory reindex`, so it's never
the source of truth you need to reconcile by hand.

## Is Sapien trying to replace API documentation?

No — narrative documentation belongs in `api/docs/**/*.md` in the
service repo (or the contract's own `info`/tag descriptions), and Sapien
indexes it as a first-class, searchable citizen alongside operations,
not a second-class blob. Memories are explicitly *not* documentation:
they're where knowledge starts before someone decides it's canonical
enough to promote. See the concept-boundaries table in
[`architecture.md`](architecture.md#concept-boundaries).
