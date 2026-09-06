# Flows

A flow is an ordered list of API calls with input bindings and
assertions, saved as `*.flow.yaml`. This covers the file shape and how to
run one; for the `${...}`/CEL expression language used inside a flow, see
[Expressions](#expressions) below or run:

```sh
sapien flow reference expressions
```

## Structure

```yaml
version: 1
id: order-allocation                # default: filename stem
name: Order allocation
description: Create an order, allocate a rider, verify it's online.
tags: [allocation, smoke]

inputs:
  customerId: { type: string, default: "cust_123" }
  city:       { type: string, required: true }

steps:
  - id: create
    call: order-service.createOrder
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
    extract:
      riderId: body.riderId
    assert:
      - status == 201
      - latency_ms < 2000

  - id: rider
    call: rider-service.getRider
    input: { riderId: "${steps.allocate.out.riderId}" }
    until: status == 200 && body.online == true      # optional polling
    poll: { interval: 1s, timeout: 30s }
    assert:
      - status == 200
      - body.online == true
      - { path: body.qcomSkill, eq: true, message: "QCOM riders must have qcomSkill" }
```

| Key | Meaning |
|---|---|
| `version` | Always `1`. Required. |
| `id` | Flow ID. Defaults to the file name minus `.flow.yaml`. |
| `name`, `description`, `tags` | Documentation only. |
| `inputs` | Map of name -> `{type, required, default, description}`. |
| `steps` | Ordered list of steps. Required, at least one. |

Step keys:

| Key | Meaning |
|---|---|
| `id` | Unique within the flow; `^[a-zA-Z_][a-zA-Z0-9_-]*$`. |
| `call` | Operation ID, `<service>.<operationId>`. A step needs `call` or `example` (or both, if they agree). |
| `example` | Saved example id to reuse; see [Examples in flows](#examples-in-flows). |
| `input` | Flat map bound by name to the operation's path/query/header params. |
| `params` | `{path:, query:, headers:}`, for name collisions the flat `input:` can't express. |
| `body` | Request body. |
| `headers` | Request headers; values may use `secret.NAME`. |
| `extract` | Map of name -> CEL over this step's own response; readable after as `steps.<id>.out.<name>`. |
| `assert` | List of bare CEL or structured assertions. |
| `until` | CEL; retries the step until true or `poll.timeout` elapses. |
| `poll` | `{interval: 1s, timeout: 30s}`; only used with `until`. |
| `timeout` | Per-request timeout, e.g. `10s`. |

## Bindings

`input:` binds by parameter name against the operation's contract: path
and query names match case-sensitively, header names case-insensitively.
Every required parameter needs a value from `input` or `params`; a
`${...}` template counts as provided even though its value isn't known
until the step runs. Use `params.path`/`params.query`/`params.headers`
only when a name exists in more than one location and the flat form
would be ambiguous.

`inputs`, `env`, and `steps.<id>.{request,status,headers,body,
latency_ms,out}` are readable everywhere. `status`, `headers`, `body`,
`latency_ms`, `request`, and `out` (bare, no `steps.<id>.` prefix) refer
to *this* step's own attempt and are only available in that step's own
`assert`/`extract`/`until` — not in its `input`/`body`/`headers`, since
the request hasn't been made yet.

## Examples in flows

A step may set `example: <id>` to reuse a saved request (`sapien example
list|show|add|rm`) instead of writing `call`/`input`/`body`/`headers` out
by hand:

```yaml
steps:
  - id: create
    example: create-qcom-order      # fills call, input, body, headers
    body: { customerId: "${inputs.customerId}" }   # replaces the example's body entirely
    assert:
      - status == 201
```

Resolution fills the step's `call`, `input`, `body`, and `headers` from
the example before validation and execution; explicit step fields win:

- `call`, if also set on the step, must name the same operation as the
  example, or validation reports `EXAMPLE_OPERATION_MISMATCH`.
- `input` keys and `headers` merge per key, the step's value winning on a
  collision.
- `body` on the step, if set, replaces the example's entirely (bodies
  aren't merged field-by-field).
- `${...}` templates inside the example are kept as-is and interpolated
  at run time exactly like a step's own templates.

A step needs `call`, `example`, or both; naming an id the workspace
doesn't know produces `UNKNOWN_EXAMPLE` with a "did you mean" suggestion,
the same way an unknown `call` does. `sapien flow validate`, `flow
create`, `flow run`, and `get_flow` all see the same expanded steps, so
what you save is the reference and what you run (or inspect) is the
resolved request.

## Expressions

Flow expressions are CEL. `assert`, `extract`, and `until` are bare CEL;
`${...}` inside any other string interpolates a CEL expression (a value
that is *exactly* one `${...}` keeps its native type instead of becoming
a string). The full reference, including every structured-assertion
compilation and the "missing key is an error, not null" rule, is:

```sh
sapien flow reference expressions
```

Structured assertions are shorthand for common comparisons:

| YAML | Meaning |
|---|---|
| `status: 201` | Response status equals 201. |
| `latency_ms: {lt: 2000}` | Response latency under 2000ms. |
| `{path: body.riderId, eq: "r1"}` | `body.riderId == "r1"`. |
| `{path: body.riderId, exists: true}` | The field is present. |
| `schema: contract` | Validate the body against the response schema. |

Add `message:` to a structured assertion to name it in a failure report.

## Assertions and polling

A step with `until:` retries the whole step (request + response) at
`poll.interval` until the expression is true or `poll.timeout` elapses;
timing out raises `E_UNTIL_TIMEOUT` (exit code 1, same bucket as an
assertion failure). Without `until`, a step runs once and its `assert:`
entries all evaluate against that one response.

## Running

```sh
sapien flow validate my-flow.flow.yaml     # or a saved flow's id
sapien flow create my-flow.flow.yaml       # save into the workspace
sapien flow list
sapien flow show order-allocation
sapien flow run order-allocation --env staging -i city=Bangalore
sapien flow run my-flow.flow.yaml --continue-on-failure --watch
```

`flow validate` runs automatically before every `flow run`; diagnostics
carry `code`, `line`, `message`, and where possible `suggestions[]` (see
`sapien flow reference flow` for the full code table). `-i key=val`
(repeatable, JSON-decoded when it parses) supplies flow inputs.
`--continue-on-failure` keeps running past a failed/errored step instead
of stopping; without it, remaining steps are marked `skipped`.
`--allow-production` is required to run against an environment with
`production: true`.

## CI and JUnit

```sh
sapien flow run order-allocation --env staging --report junit --out results.xml
sapien flow run order-allocation --report json
```

See [`ci.md`](ci.md) for a full GitHub Actions example, including
uploading the JUnit report and passing secrets as `SAPIEN_SECRET_*`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Every step and assertion passed. |
| 1 | An assertion failed, or an `until` timed out. |
| 2 | A resolution, transport, or engine error. |
| 3 | Blocked (production environment without `--allow-production`, or MCP permission denied). |

## When a run fails

A failed or errored step's own response is often unhelpful on its own: an
API returns `400 Automation Rule Exclusion`, or `409 NO_RIDER_AVAILABLE`,
and the real explanation lives somewhere else entirely. `sapien call`,
`sapien flow run`, `sapien run show`, and the MCP `execute_api`/`run_flow`/
`get_run` tools all close that loop automatically: for every failed or
errored step whose response status is 400 or above, Sapien looks for
candidate explanations and prints them under a `Might explain it:` block.

```
409 Conflict  62 ms

{
  "code": "NO_RIDER_AVAILABLE",
  "message": "no eligible rider is online for order \"ord_0001\""
}

Might explain it:
  contract contract says 409: No eligible rider is currently online
  doc      allocation-service/docs/allocation.md # No eligible rider  (matched: NO_RIDER_AVAILABLE)
           sapien docs show allocation-service docs/allocation.md --section "No eligible rider"
  memory   mem_01H...: NO_RIDER_AVAILABLE clears once a rider comes back online.
           sapien memory show mem_01H...
```

Three sources feed this, in priority order:

1. **Contract.** If the operation's own catalog entry documents a response
   for that exact status code, its description is the first hint --
   Sapien already knows what the API's authors said this status means.
2. **Docs.** Sapien pulls candidate tokens out of the response body (the
   string values of `code`, `error`, `error_code`, `errorCode`, `type`,
   `reason`, `title`, `message`, `detail`, and `details`, plus the status
   code itself) and searches service documentation for each one, scoped to
   the failing operation's service.
3. **Memories.** The same tokens are searched against memories, so a
   gotcha an agent or teammate captured about this exact failure before
   surfaces again instead of being rediscovered.

Every hint line is followed by the concrete command that opens it:
`sapien docs show <service> <path> --section <heading>` for a doc hint, or
`sapien memory show <id>` for a memory hint (a contract hint's description
is already inline, so there's nothing further to open).

Under `--json`, the same information is available as a top-level `hints`
array alongside the run's usual fields -- existing scripts that read
`.status`, `.steps`, or any other run field are unaffected; `hints` is
simply additional and is omitted for a run that has none. MCP's
`execute_api`, `run_flow`, and `get_run` mirror both the text block and a
structured `hints` field for the same reason.

This never blocks or slows down a passing run: nothing runs unless a step
actually failed or errored with an HTTP response, and a lookup or search
error is logged and skipped rather than hiding the hints another step
found.


## Setup and teardown

A flow may declare `setup:` and `teardown:` step lists alongside `steps:`.
Setup runs first and its results are addressable as `steps.<id>` like any
other step. Teardown always runs after the main steps, even when a step
failed or errored or the run was cancelled, in order, continuing past its
own failures; teardown outcomes are recorded on the teardown steps but never
change the run's status. Put what a flow creates in setup and what releases
it in teardown, and a failing flow stops leaving litter behind.

```yaml
setup:
  - id: bag
    call: sorting-service.createBag
    body: { type: RTO }
    extract: { bagId: body.id }
steps:
  - id: scan
    call: sorting-service.scanIntoBag
    input: { bagId: "${steps.bag.out.bagId}" }
    assert: [status == 200]
teardown:
  - id: release
    call: sorting-service.releaseBag
    input: { bagId: "${steps.bag.out.bagId}" }
```

## Resuming a run

A long flow that fails at step 12 does not have to start again from step 1.

```sh
sapien run resume <run-id>                 # reuse setup and every step before the failure
sapien run resume <run-id> --from scan     # choose the resume point
sapien flow run my-flow --resume <run-id> --until scan
```

The resumed run copies the earlier run's setup steps and every main step
before the resume point, request, response, and extracted values included,
so `${steps.x.out.y}` and `steps.x.body` resolve exactly as before, and
marks them `reused`. The resume point defaults to the first step that did
not pass in the earlier run. `--from` and `--until` bound execution;
teardown still runs. A reused step whose definition changed since that run
is still reused, with a warning on the step. Inputs default to the earlier
run's. Over MCP the same options are `run_flow(resume_from, from_step,
until_step)`. Fix the flow first when the failure was in the flow: `sapien
flow patch`, or edit the file and `sapien flow update <id> --file`.

## Editing flows

`patch_flow` (MCP) and `sapien flow patch <id> --ops @ops.json` apply
step-level operations to the YAML file, preserving comments and order:
`set_step` (replace a step by id), `merge_step` (set some of a step's keys,
for example one assertion list), `add_step` (with `after`, `before`, or
`phase: setup|teardown`), `remove_step`, `set_inputs`, `set_meta`. When
the file was already edited on disk, `update_flow(id, path)` or `sapien
flow update <id> --file <path>` re-reads and validates it. `create_flow`'s
`path` is relative to the workspace's `flows/` directory and must stay
inside it; the tool returns a summary (id, path, step counts, operations,
warnings), never the document. `get_flow` returns the YAML source, and a
bare assertion may be written as `- status == 200` or `- expr: status ==
200`, so what you read back is valid to send.


## Soft assertions

An assertion with `soft: true` records a mismatch as a warning on the step
instead of failing it. The run stays green, the result is kept on the step
(`soft: true, passed: false`), the summary counts it under
`assertions_warned`, and the next run of the same flow reports every soft
assertion that flipped since the previous run, so a gap you recorded
announces itself when it closes, and a regression when it opens.

```yaml
assert:
  - status == 200
  - expr: body.portTracking != null
    soft: true
    message: port-leg tracking is not wired yet
```

`sapien flow run` and `run resume` print `soft mismatch:` lines after the
run table and `soft assertion now passing since run <id>:` when one flips;
`run_flow` over MCP does the same in its text.

## Reading run results cheaply

`run_flow` returns a summary by default: one line per step, assertion
details for failed steps, no bodies. `detail: failed` adds bodies only for
failed or errored steps; `detail: full` returns everything (capped per body,
and large for a long flow). `get_run(id, include_bodies=true, step=<id>)`
shows one step in full. `get_flow(id, detail: outline)` lists a flow's
steps one per line without bodies or prose, and `validate_flow(path)`
validates a file already on disk without echoing its text.
