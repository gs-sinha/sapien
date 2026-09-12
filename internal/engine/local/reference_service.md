# Service package reference

This is what Sapien needs from a service so it can index its API, its
documentation, and its knowledge. Follow it when a user asks to "onboard
a service to Sapien", "add Sapien-style docs", or "make this service
searchable by Sapien". Derive everything from the service's real code:
never invent endpoints, fields, or status codes.

## Who reads what you write

Not a reviewer of this repo. The reader is an agent in a *different*
repository, working for someone who has never seen this code, who found
this service through `search_apis` and has to get a call right without
reading its source. Everything that reader knows about this service is
what you put in `api/`.

The most common complaint from that side is not a missing endpoint: it is
docs that restate the contract. Knowing that `POST /v1/allocations` takes
an `orderId` and returns an `allocationId` does not tell you that a QCOM
order needs a rider with `qcomSkill`, that calling it twice for one order
is safe, or that a `409` means try later rather than fix your payload.
That knowledge lives in the heads of the people who own the service, and
some of it is nowhere in the code either -- which is why onboarding
includes asking them (see "Interview the owner").

Three things make a service usable from outside, and Sapien measures all
three (see "Coverage"):

| | What it answers | Where it goes |
|---|---|---|
| the contract | what exists, what it takes, what it returns | `api/openapi.yaml` |
| the docs | when to call it, what it means, what breaks | `api/docs/*.md` |
| the examples | what a call that works actually looks like | `example:` in the contract, `api/examples/` |
| the task index | how callers describe work when they do not know an endpoint name | `tasks:` in `api/service.yaml` |

## Layout

```text
<repo>/
  api/
    openapi.yaml        # the contract (required; .yml or .json also work)
    service.yaml        # metadata: name, description, owners, concepts, envs
    docs/               # Markdown documentation, any depth
      <area>.md
    flows/              # optional: flows shipped with the service
      <name>.flow.yaml
    examples/           # optional: saved request examples (<id>.example.yaml)
    memories/           # optional: service-scoped memories (Sapien writes here)
```

`api/` is the package directory. Sapien also accepts `openapi.{yaml,yml,json}`
in the repo root or under `docs/`, `spec/`, or `openapi/`, and an explicit
`contract:` override, but `api/` is the convention and what agents should
create. `docs/` must sit next to the contract, so `api/docs/` when the
contract is `api/openapi.yaml`.

## openapi.yaml

OpenAPI 3.0 or 3.1. Cover every HTTP endpoint the service actually serves.

- Every operation has a stable camelCase `operationId`. Sapien addresses
  operations as `<service-name>.<operationId>`; an operation without one
  gets a synthesized ID like `get_v1_allocations_stats`, which is harder
  to search and to reference from docs.
- Every operation has a one-line `summary` and a `description` that says
  what it is for and when to use it, not just what it returns.
- Every parameter has a `description`; enums list their values.
- Request and response bodies use `components/schemas`, and every schema
  field has a `description`. Field names become searchable, and
  descriptions are what agents read to pick the right field.
- List every response code the handler can return, including error
  responses, each with a schema. Put machine-readable error codes in an
  enum or in the description.
- Group operations with `tags`, and mark retired endpoints
  `deprecated: true`.
- **Every operation that takes a request body carries an `example:`** --
  a complete, plausible payload, not a field-by-field echo of the schema.
  This is the authored home for an operation's payload: it travels with
  the code, Sapien serves it from `get_api` and prefills it in the UI's
  "Try it" form, and every other OpenAPI tool shows it too. Without one,
  the next caller compiles a body out of the field list by hand and
  usually does it in a throwaway script.

```yaml
  requestBody:
    required: true
    content:
      application/json:
        schema: { $ref: "#/components/schemas/CreateOrderRequest" }
        example:
          customerId: cust_8f21
          type: QCOM
          pickup: { lat: 12.9716, lng: 77.5946 }
          drop: { lat: 12.9352, lng: 77.6146 }
```

  Use values that look like the real thing (a realistic id shape, a real
  enum member, coordinates in the city the service serves), because a
  reader will copy them. Add a response `example:` on the primary success
  response too where the shape is not obvious. Where one operation has
  genuinely different modes, use `examples:` with a named entry per mode
  (`qcom`, `intercity`) rather than one payload that fits neither.

## service.yaml

```yaml
version: 1
name: allocation-service            # kebab-case; the prefix of every operation ID
description: Finds eligible riders and creates allocations for orders.
owners: [allocation-platform]       # teams or people
concepts: [rider allocation, dispatch, matching, qcom]   # domain terms callers search for
tasks:
  - id: allocate-rider
    phrases: [allocate a rider, dispatch an order, find a courier]
    targets:
      - operation: allocate
    tests:                         # held-out queries; tests are checked, never indexed
      - query: assign someone to collect this order
        expect_any: [allocate]
        top_k: 3
contracts: [openapi.yaml]           # relative to api/; more than one is allowed
environments:
  local: { base_url: http://localhost:8082 }
  staging: { base_url: https://allocation.staging.example.com }
```

`name` is required unless the workspace entry sets one. `concepts` are the
words a caller would type when they do not know the endpoint name; they
feed search and link documentation to the service. `environments` are
hints: the workspace's own environment files decide what actually runs.

`summary` and `description` are indexed by `search_apis`; write them in the
caller's vocabulary. `tasks` is the operation-level intent index. `phrases`
contains language callers really use, `targets` maps that language to one or
more operations, and `when` distinguishes multiple valid targets. Bare
operation IDs are expanded to `<service>.<operationId>`. A concise task may
use `phrase: mark delivered, proof of delivery` and `operation: completeTrip`.
Use `tests` for paraphrases that are not present in `phrases`; after sync,
Sapien runs each query and emits `UNDISCOVERABLE_OPERATION` when none of
`expect_any` appears in the top `top_k` results.

Task phrases also enrich their target operation's vector when the workspace
has optional semantic search enabled. This adds no extra vector per task.
Semantic search is disabled by default in `.sapien/config.yaml`; task phrase
and FTS5 retrieval continue to work without it.

## docs/*.md

One Markdown file per domain area, split by `##` headings. Each heading
becomes a searchable section, so write it in the caller's words as well as
making it specific ("How QCOM orders get a rider", not "derivedStatus state
machine" or "Rules"). Two files are not optional:

- **`overview.md`** -- the service in one page, for a reader who arrived
  from a search result: what this service owns and what it deliberately
  does not, the domain objects and their lifecycle (states, who moves
  them, what is terminal), who calls it and for what, the services it
  depends on and what must exist before a call can succeed, how auth and
  tenancy work, and what is different between environments.
- **one file per domain area** -- the rules of that area, at the depth
  below.

A section earns its place by answering something the contract cannot.
For each area, and for each operation in it, say:

- **Why it exists and when to call it** -- and when to call something else
  instead. Name the caller if it matters ("the mobile app on order
  confirmation", "only the ops console").
- **Preconditions**: what must already be true, in which other service,
  before this succeeds.
- **Business rules and invariants**: the eligibility rules, limits, and
  guarantees. What is checked, what is silently tolerated, what it means
  when a rule is violated.
- **What it changes**: side effects beyond the response -- rows written,
  events published, notifications sent, money moved -- and what a caller
  can observe afterwards.
- **Ordering, idempotency, and retries**: is a second identical call safe?
  Is there an idempotency key? What is safe to retry and after how long?
- **Every error code**: what causes it, whether the caller should retry,
  change the payload, or give up, and which of them are normal in
  healthy operation rather than bugs.
- **Concurrency and timing**: what races, what is eventually consistent,
  how long a state transition usually takes (so a caller knows whether to
  poll and for how long).
- **Deprecations**: what replaces a retired endpoint or field, and the
  cutoff if there is one.
- **The traps**: the thing everyone gets wrong the first time. A field
  that looks optional but is required in practice, a status that does not
  mean what it says, a pair of endpoints that must be called in order.

Write what is true, not what would be reassuring; a doc that omits a trap
is worse than no doc, because it will be trusted. If you could not
establish something, say so in the "Open questions" section (below)
rather than writing a plausible guess: the reader cannot tell your guesses
from your facts, and will act on both.

Sapien links prose to the contract by pattern, so write references in
these shapes:

| To reference | Write |
|---|---|
| an endpoint | `` `POST /v1/allocations` `` (method plus path, in backticks) |
| an operation | `` `allocation-service.allocate` `` |
| a schema | its component name, e.g. `Allocation` |
| a field | `` `qcomSkill` `` (a bare identifier in backticks) |
| a concept | the concept word from `service.yaml`, in plain prose |
| another service | its name, e.g. rider-service |

Inside fenced code blocks only `METHOD /path` mentions are recognised, so
curl examples still link, but everything else should be in prose.

Example:

```markdown
## QCOM allocation rules

`POST /v1/allocations` (`allocation-service.allocate`) matches an order to
a rider. `QCOM` orders may only be matched to a rider with `qcomSkill=true`
who is also online; see rider-service for what `qcomSkill` means.

### No eligible rider

If no rider qualifies, `allocation-service.allocate` returns `409` with
code `NO_RIDER_AVAILABLE`. Treat it as retryable.
```

### Open questions

End `overview.md` (or the area file it belongs to) with an `## Open
questions` section listing what you could not establish: the question,
what you looked at, and what a caller should do meanwhile.

```markdown
## Open questions

- Is `allocate` idempotent per order? The handler has no idempotency key
  and no unique constraint on `order_id`; until someone confirms, treat a
  retry as capable of creating a second allocation.
- What is the retention on `GET /v1/allocations/history`? Unknown; do not
  rely on data older than a month.
```

This is the honest form of a gap. It tells the next agent where the edge
of the knowledge is, keeps someone from inventing an answer later, and
gives the service's owners a short, specific list to fix.

## Interview the owner

Some of what makes a service usable is in nobody's code. Ask for it --
the user in the session owns or works on this service, and this is the
one chance to get it cheaply.

Do it in **one round, after you have drafted the package from the code**,
not before. A question asked cold ("tell me about your service") gets a
paragraph nobody can use; the same question asked with a draft in hand
("`allocate` has no idempotency key and no unique constraint on
`order_id` -- is calling it twice for one order safe?") gets an answer
worth writing down. Group the questions by area, keep the round short
(roughly five to fifteen, prioritised -- not one per operation), and make
each one answerable in a sentence.

Only ask what the code cannot tell you. Never ask what a method does,
what a field is called, or which status codes a handler returns: read
them. Ask about:

**The service and its subprojects**
- Which endpoints matter to callers outside this repo, and which are
  internal plumbing nobody else should call?
- Which of these operations are retired in practice but still deployed?
- What do people outside the team consistently get wrong about this
  service?
- If this repo holds several services or modules, where is the boundary,
  and which one owns each concept?

**Per operation (only for the ones that carry real behaviour)**
- Who calls this today, and for what?
- Is a second identical call safe? Is there an idempotency key?
- What else happens when this succeeds -- events, notifications,
  downstream writes?
- Which errors are normal in healthy operation, and which mean a bug?
- What must already exist, in which other service, for this to work?
- How long does the state change take to become visible?

**Across services and environments**
- Which services must be up for this to work, and which fail soft?
- What differs between staging and production beyond the base URL --
  seeded data, feature flags, stricter validation?
- Is there test data a caller can safely use, and anything they must
  never touch?

Then: write every answer into the contract or the docs where it belongs
(not into the chat), record the ones that are operational rather than
documentary as memories with `create_memory`, and put whatever the user
did not answer under "Open questions". Do not stall onboarding waiting
for answers -- register the service with what you have, and fold the
answers in when they arrive.

## Registering the service

Over MCP, once the package exists on disk:

```json
add_service { "path": "/absolute/path/to/repo" }
add_service { "url": "git@github.com:org/repo.git", "ref": "main", "subdir": "api" }
```

`path` must be absolute: the MCP server does not share the caller's
working directory. Pass `name` only if `service.yaml` has none.

From a shell, inside the service repo:

```sh
sapien service add "$PWD" --workspace /path/to/workspace
```

Both return the operation count, the sync status, and warnings such as
operations without an `operationId` or docs that mention unknown paths; see
"Warnings" below for what to do with them. Registering a name that is
already registered is not an error: `add_service` (and `sapien service
add`) re-syncs the existing service instead, the same as calling
`sync_service` (`sapien service sync <name>`) on it, and reports that it
did so.

## Coverage

Alongside the warnings, `add_service`, `sync_service`, and `get_service`
report how much of the service is *understandable*, not just indexed:

```text
allocation-service: 24 operations, status ok
docs: 18/24 operations documented in api/docs (31 sections)
examples: 15/19 operations that take a body have a request example
```

An operation counts as documented when some `api/docs` section mentions it
-- by operation id, or by an endpoint mention of its path. The contract's
own tag and `info` descriptions do not count: that is the contract
restating itself. Deprecated operations are left out of the totals
entirely; what they need is `deprecated: true`, not prose.

Closing these numbers is the job, not decoration. Four coverage warnings
name what is missing:

| Code | Means |
|---|---|
| `NO_NARRATIVE_DOCS` | there is no `api/docs/*.md` at all, so every operation is contract-only (reported once, not per operation) |
| `UNDOCUMENTED_OPERATION` | no doc section mentions this operation |
| `MISSING_REQUEST_EXAMPLE` | it takes a request body and no example exists anywhere |
| `NO_CONCEPTS` | `service.yaml` has no `concepts:`, so a search by domain term misses the service |

They are lint like any other: acceptable in `accepted_warnings` with a
reason (an internal endpoint no outside caller should use is a legitimate
thing to leave undocumented -- say so on the record), and never a reason a
registration fails.

## Warnings

A warning is a lint finding, never a block: an operation without an
`operationId`, a response with no `application/json` content, a doc that
mentions an unknown path, an operation no doc mentions, and similar. `status: ok` means the service
synced, not that every warning has been looked at, so it is safe -- and
expected -- for a service to carry warnings indefinitely.

Never silence a warning by misdescribing the wire. A warning that is true
stays true no matter how the contract describes it; changing the
description to make the warning stop firing (rewriting a genuinely
`text/plain` response to claim `application/json`, say) removes the
warning and leaves the contract wrong. Fix a warning when it points at a
real gap (a missing `operationId`, an undocumented field). When it does
not, because the contract already describes the wire faithfully, accept it
in `service.yaml` under `accepted_warnings`, with a reason a reviewer can
check:

```yaml
accepted_warnings:
  - code: UNSUPPORTED_MEDIA_TYPE            # required: the warning code
    match: "text/plain"                      # optional: substring matched case-insensitively against the warning message and its source file/pointer; omitted = every warning with this code
    reason: "These endpoints return text/plain by design (WebMvcConfigurer); the contract describes the wire faithfully."   # required, non-empty
```

An accepted warning moves out of the service's `warnings` into
`accepted_warnings`, carrying the reason; `get_service` returns both lists,
and `sapien service list` shows an `ACCEPTED` count (`sapien service
add`/`sync --show-accepted` also prints each accepted warning and its
reason). An `accepted_warnings` entry that stops matching anything -- the
gap it accepted was fixed, the endpoint was removed -- becomes its own
warning, `STALE_ACCEPTANCE`, naming the entry to remove, so an acceptance
can never quietly outlive the warning it was written for.

`sync_service` (`sapien service sync`) re-reads `api/` on demand, so run it
again after editing `accepted_warnings` itself, not just after editing the
contract.

## Examples

An operation's payload has two homes, and they do different jobs.

**The contract's `example:`** (see "openapi.yaml") is the authored one:
every operation that takes a body gets one during onboarding, from reading
the code. It is documentation -- nothing has proved it works -- but it is
what a caller starts from, it travels with the code, and it is the one
Sapien can offer before anyone has ever called the service.

**A saved example** (`examples/<id>.example.yaml`: `operation`, `input`,
`body`, `headers`, optional `expect` and `verified`) is the proven one.
Save it from a real call with `create_example(run_id=...)` over MCP or
`sapien call ... --save-example <id>` from a shell; `verified` is only
ever written by those paths, so a verified example is evidence, not a
claim. Put it at service scope when it would work in a fresh environment,
so it is committed with the code. Flows reuse one with a step
`example: <id>`.

Whatever exists, `get_api` returns a ready-to-send `request_example` and
the UI prefills its "Try it" form from it, labelled with where it came
from: a verified example first, then a saved one, then the contract's,
then a payload synthesized from the schema. Onboarding's job is to make
sure that answer is never the last one.

## Keeping docs current

Onboarding is only useful if `api/` keeps up with the code. Add this
section to the repo's `CLAUDE.md` or `AGENTS.md` (whichever the repo
uses; create `AGENTS.md` if neither exists), with the service name
filled in. `add_service` returns the same block, ready to paste.

```markdown
## Sapien

This service is indexed by Sapien as `<service-name>` from `api/`
(`openapi.yaml`, `service.yaml`, `docs/`), and read by agents in other
repositories that call this service. `api/` is the only thing they see.

When you add or change an endpoint, a request or response field, a status
code, or an error code, update `api/openapi.yaml` and the relevant
`api/docs/*.md` in the same change. Keep `operationId`s stable. A new
operation that takes a body needs an `example:` in the contract, and the
docs need to say when to call it, what it changes, and which errors mean
retry -- not just what the fields are.

Before editing `api/`, read what Sapien already knows with the `sapien`
MCP tools: `get_service("<service-name>")`, `search_apis`, and
`get_dsl_reference("service")` for the format. Sapien re-indexes on save;
`sapien service sync <service-name>` forces it.
```

## Onboarding checklist for agents

1. Read the routes, handlers, request and response types, error handling,
   and -- for the business rules -- the service layer behind them.
2. Write `api/openapi.yaml` (with an `example:` on every request body),
   `api/service.yaml`, and `api/docs/*.md` (`overview.md` plus one file
   per domain area) as above. Prefer fewer, accurate operations over
   guessed ones.
3. Ask the user the one round of questions the code cannot answer (see
   "Interview the owner"), and fold the answers into the contract, the
   docs, and memories. What is still unanswered goes under "Open
   questions".
4. Call `add_service` with the absolute repo path (or run
   `sapien service add`).
5. Read the coverage line it returns and close the gap: an undocumented
   operation, a body with no example, a service with no concepts. For each
   warning: fix it if it points at a real gap; if it already describes the
   wire faithfully, or the gap is deliberate, accept it in
   `service.yaml`'s `accepted_warnings` with a reason instead of editing
   the contract to silence it (see "Warnings" above).
6. Test retrieval from the outside. Run both `search_apis` and `search_docs`
   with several phrases a caller would plausibly type. Put important
   operation queries under `tasks[].tests`; if sync reports
   `UNDISCOVERABLE_OPERATION`, improve the task phrases, operation summary,
   or docs and sync again. Then call `get_api` on the important operation and
   check that its request example is one you would be happy to be handed.
7. Add the "Sapien" section above to the repo's `CLAUDE.md` or
   `AGENTS.md`, so the next change to the code updates `api/` too.
8. Record anything you learned that does not belong in the contract or
   docs -- an operational quirk, an invariant, a thing that broke once --
   as a memory with `create_memory`.
