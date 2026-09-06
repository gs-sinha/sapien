# Memory

A memory is a small, attributed note attached to a specific part of the
catalog: an operation, a field, a schema, a flow, an environment, or a
free-form concept. Memories are how operational knowledge that isn't
formally specified anywhere — "this field means X but doesn't imply Y",
"this endpoint is flaky under load" — gets captured once and found again,
by a human or an agent, exactly when it's relevant.

Memories are deliberately not documentation: they start as quick, often
personal notes; promoting one into a canonical OpenAPI description or doc
section is a separate, explicit, human-reviewed step (`memory promote`).
See [`architecture.md`](architecture.md#concept-boundaries) for the full
"what lives where" table.

## Shape

A memory is Markdown with YAML front matter — one file per memory, so
concurrent additions never conflict:

```markdown
---
id: mem_01J8Z5K3W2RQ4X7M9N
type: semantic          # semantic | behavioral | testing | invariant | environment | gotcha | note (default)
scope: workspace         # personal | workspace | service | flow
subject:
  operation: rider-service.getRider
  field: response.200.body.qcomSkill
tags: [qcom, allocation]
source: { kind: user }   # user | agent{client} | run{run_id} | ...
created: 2026-09-05T10:20:00Z
status: active
---
qcomSkill indicates whether the rider is eligible for quick-commerce
(QCOM) orders. It does not indicate the rider is online or available.

Implications: QCOM allocation should only select riders with
qcomSkill=true.
```

Only `id`, `scope`, `created`, and the body text are required; `type`
defaults to `note`. A subject may combine several fields (e.g. `operation`
+ `field`) for precise attachment, or just `service`/`concept` for a
looser one.

## Scope and where files live

| Scope | Stored as | Visible to |
|---|---|---|
| `personal` | a row in your local SQLite index only — no file | just you, just this machine |
| `workspace` | `<workspace>/memories/*.md` | everyone who has the workspace |
| `service` | `<service-repo>/api/memories/*.md` | everyone who has that service repo (written only on explicit "share with service") |
| `flow` | a workspace or service memory with `subject.flow` set | narrows visibility, doesn't change storage |

Shared memories are files precisely so they're git-reviewable and travel
with the workspace or service repo; personal notes stay zero-friction and
private. The SQLite index is always rebuildable from the files plus your
personal table (`sapien memory reindex`).

### Choosing a scope

`--scope` defaults to `workspace`, which is easy to keep using without
ever deciding it's the right one. Scope decides storage and sharing, not
what the memory is about: service scope is committed under
`<service>/api/memories`, reviewed like any other change, and reaches
everyone who clones that repo; workspace scope lives under
`<workspace>/memories`, has no git history unless the workspace itself is
a git repo, and is invisible to teammates who don't share your machine.
That asymmetry, not the subject matter, is the decision. It's easy to
default to reasoning about subject instead ("this is about a service, so
it must be service-scoped") but a workspace-scoped memory may legitimately
carry a service subject; scope and subject are independent.

The discriminating question: would this still be true in a fresh
environment with empty databases? Yes means service. No means workspace.
A memory that mixes a lasting contract fact ("this field means X") with
local environment data ("staging is slow right now") is the common
failure; the fix is to split it into two memories, not to pick a side.

If you captured a memory in the wrong scope, move it instead of deleting
and re-adding it:

```sh
sapien memory rescope mem_01J8Z5K3W2RQ4X7M9N --scope service
```

Rescoping to `service` scope requires a service subject; give one with
`--service <name>` if the memory doesn't already carry one. `memory add`
warns when the workspace directory has no `.git` (workspace memories
would live only on this machine) and, for a workspace-scoped memory that
names a service, suggests the rescope command directly.

## Capture

From the CLI:

```sh
sapien memory add --op rider-service.getRider \
  --field response.200.body.qcomSkill \
  "qcomSkill does not imply the rider is online"

sapien memory add --service allocation-service --type gotcha \
  --scope personal \
  "allocate retries internally; don't wrap it in your own retry loop"
```

Flags: `--op`, `--field` (needs `--op` or `--schema`), `--service`,
`--schema` (`<service>.<Name>`), `--flow`, `--concept`, `--scope`
(default `workspace`), `--type` (default `note`), `--tag` (repeatable),
and the global `--env` for `subject.environment`.

From an MCP host, the equivalent is the `create_memory` tool — an agent
records what it learned while authoring or running a flow, attributed to
that client (`source: agent{client}`). Its result carries the same hints
`memory add` prints: where the memory landed, the not-a-git-repo warning,
a suggestion to call `rescope_memory` when a workspace memory names a
service, and any similar existing memories found by `search_memories`.
`rescope_memory(id, scope, service?)` is the MCP equivalent of
`memory rescope`. See [`mcp.md`](mcp.md).

## Retrieval

```sh
sapien memory list --op rider-service.getRider
sapien memory list --scope workspace --type invariant
sapien memory search "qcom eligibility"
sapien memory show mem_01J8Z5K3W2RQ4X7M9N
sapien memory rm mem_01J8Z5K3W2RQ4X7M9N
sapien memory rescope mem_01J8Z5K3W2RQ4X7M9N --scope service
```

Two retrieval modes, matching the engine's `Memories` API:

- **Structural** (`get_relevant_memories` over MCP, and the context
  builder below): given a set of subjects — the operations you're about
  to use, their services and schemas, concept terms — returns memories
  that attach to them, ranked by how specific the match is (exact
  operation+field beats service-level beats a tag match).
- **Lexical search** (`memory search`, `search_memories` over MCP): free
  text, with the same structural signal folded in as a ranking boost so
  an on-topic memory still outranks an off-topic keyword hit.

`sapien context "<intent>"` builds the same `ContextBundle` an MCP
`get_context` call would: relevant operations, doc sections, memories,
and flows for a natural-language intent, in one budgeted response. Every
item in it carries a `tier` (`contract`/`documentation`/`memory`/`run`)
and memories additionally carry their `scope` and `source`, so a
consumer can weigh a canonical doc line above a personal note without
the engine editorializing.

## Promotion

`invariant` and `testing` memories are candidates for becoming a real
assertion or an OpenAPI description — knowledge that started as a note
because it was learned mid-task, but belongs somewhere canonical:

```sh
sapien memory promote mem_01J8Z5K3W2RQ4X7M9N
```

This only *locates* the promotion target — an OpenAPI file, line, and
JSON pointer, or a doc section, plus the memory's text and (where
computed) a suggested edit. It never writes the file itself: promoting
knowledge into a service's contract is an edit to that service's repo,
so a human or the authoring agent applies it explicitly and reviews the
diff. Nothing moves between memory, contract, and flow layers
automatically.

Memory is a staging area, not the destination, so `memory add` and
`create_memory` both check for close matches on every capture: if a new
memory's text closely resembles an existing one, they print it as
`similar: <id> (score) <first line>` and suggest updating that memory
instead of creating a near-duplicate, or promoting it now that a second
independent observation has confirmed it. Once a memory is promoted, mark
it `status: promoted` so retrieval stops surfacing the staging note in
favor of the canonical text.
