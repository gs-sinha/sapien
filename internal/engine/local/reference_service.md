# Service package reference

This is what Sapien needs from a service so it can index its API, its
documentation, and its knowledge. Follow it when a user asks to "onboard
a service to Sapien", "add Sapien-style docs", or "make this service
searchable by Sapien". Derive everything from the service's real code:
never invent endpoints, fields, or status codes.

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

## service.yaml

```yaml
version: 1
name: allocation-service            # kebab-case; the prefix of every operation ID
description: Finds eligible riders and creates allocations for orders.
owners: [allocation-platform]       # teams or people
concepts: [rider allocation, dispatch, matching, qcom]   # domain terms callers search for
contracts: [openapi.yaml]           # relative to api/; more than one is allowed
environments:
  local: { base_url: http://localhost:8082 }
  staging: { base_url: https://allocation.staging.example.com }
```

`name` is required unless the workspace entry sets one. `concepts` are the
words a caller would type when they do not know the endpoint name; they
feed search and link documentation to the service. `environments` are
hints: the workspace's own environment files decide what actually runs.

## docs/*.md

One Markdown file per domain area, split by `##` headings. Each heading
becomes a searchable section, so make headings specific ("QCOM
allocation rules", not "Rules").

Write for an engineer who has never seen the service:

- Business rules and invariants, and what happens when they are violated.
- State transitions, ordering and idempotency constraints, retries.
- Every error code, what causes it, and what the caller should do.
- Dependencies on other services and what must exist first.
- Deprecations and what replaces them.
- Anything that is true but not expressible in OpenAPI.

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

## Warnings

A warning is a lint finding, never a block: an operation without an
`operationId`, a response with no `application/json` content, a doc that
mentions an unknown path, and similar. `status: ok` means the service
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

A saved example is a known-good request for one operation
(`examples/<id>.example.yaml`: `operation`, `input`, `body`, `headers`,
optional `expect` and `verified`). Save one from a real call with
`create_example(run_id=...)` over MCP or `sapien call ... --save-example
<id>` from a shell; `verified` is only ever written by those paths. Put it
at service scope when it would work in a fresh environment, so it is
committed with the code and the next caller starts from it instead of
rediscovering the payload. Flows reuse one with a step `example: <id>`.

## Keeping docs current

Onboarding is only useful if `api/` keeps up with the code. Add this
section to the repo's `CLAUDE.md` or `AGENTS.md` (whichever the repo
uses; create `AGENTS.md` if neither exists), with the service name
filled in. `add_service` returns the same block, ready to paste.

```markdown
## Sapien

This service is indexed by Sapien as `<service-name>` from `api/`
(`openapi.yaml`, `service.yaml`, `docs/`).

When you add or change an endpoint, a request or response field, a status
code, or an error code, update `api/openapi.yaml` and the relevant
`api/docs/*.md` in the same change. Keep `operationId`s stable.

Before editing `api/`, read what Sapien already knows with the `sapien`
MCP tools: `get_service("<service-name>")`, `search_apis`, and
`get_dsl_reference("service")` for the format. Sapien re-indexes on save;
`sapien service sync <service-name>` forces it.
```

## Onboarding checklist for agents

1. Read the routes, handlers, request and response types, and error
   handling in the repo.
2. Write `api/openapi.yaml`, `api/service.yaml`, and `api/docs/*.md` as
   above. Prefer fewer, accurate operations over guessed ones.
3. Call `add_service` with the absolute repo path (or run
   `sapien service add`).
4. For each warning it reports: fix it if it points at a real gap in the
   contract or docs; if it already describes the wire faithfully, accept
   it in `service.yaml`'s `accepted_warnings` with a reason instead of
   editing the contract to silence it (see "Warnings" above). Then confirm
   with `get_service` and a `search_apis` query a caller would plausibly
   type.
5. Add the "Sapien" section above to the repo's `CLAUDE.md` or
   `AGENTS.md`, so the next change to the code updates `api/` too.
6. Record anything you learned that does not belong in the contract or
   docs as a memory with `create_memory`.
