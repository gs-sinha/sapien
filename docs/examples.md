# Examples

An example is a saved request for exactly one operation: a payload a
human or agent found to work, kept at the workspace layer so flows, the
CLI, MCP hosts, and a UI can replay it without rediscovering it. Where a
memory captures a fact ("this field means X"), an example captures a
working request.

## Every operation has a request example

Nothing that needs a payload should have to compile one out of a schema,
so Sapien always has an answer to "what does a call to this look like?".
`get_api` returns it as `request_example`, `GET
/v1/operations/{id}/example` serves it to the UI (whose "Try it" form
opens prefilled from it), and both say where it came from:

| Source | Means | Trust |
|---|---|---|
| `verified` | a saved example written from a real run | it was sent and it worked |
| `saved` | a hand-written saved example | someone thought it would work |
| `contract` | the `example:` in the service's own openapi.yaml, written at onboarding | documentation, never sent |
| `schema` | synthesized from the request schema: required fields, plus any field the contract gives a value for | placeholders; a shape, not a payload |

Onboarding's job is to make sure `schema` is never the best available
answer -- every operation that takes a body gets an `example:` in the
contract (see [`onboarding.md`](onboarding.md)) -- and a saved example
from a real call is what turns that into evidence.

The UI's "Whole shape" button asks the same endpoint with `?fields=all`,
which fills in every field the schema declares, optional ones included,
for a human who would rather delete fields than look them up.

Examples are deliberately narrow: one operation each, checked against
the contract exactly as a flow step is, with the same diagnostics. An
example that needs several calls belongs in a flow, not an example.

## Shape

An example is one YAML file:

```yaml
# <workspace>/examples/create-qcom-order.example.yaml   (workspace scope)
# <repo>/api/examples/create-qcom-order.example.yaml    (service scope)
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

`input`/`body`/`headers` may carry `${inputs.x}` templates, the same
expression syntax a flow step uses; a flow or `sapien call --example`
supplies the values with `-i`.

`verified` is the important field: it is written only by save-from-run
paths (`sapien call --save-example`, `sapien run save-example`, the MCP
`create_example` tool with a `run_id`), never by hand, so a tested
example can always be told from a drafted one. `sapien example add`
never sets it.

## Scope

The same asymmetry as memories: scope decides storage and sharing, not
what the example is about.

| Scope | Stored as | Visible to |
|---|---|---|
| `workspace` | `<workspace>/examples/*.example.yaml` | everyone who has the workspace; no git history unless the workspace itself is a git repo |
| `service` | `<service-repo>/api/examples/*.example.yaml` | everyone who has that service repo, reviewed like any other change |

`--scope` defaults to `workspace`. Move an example between scopes
without losing it:

```sh
sapien example rescope create-qcom-order --scope service
```

## Capture

Three ways to get an example, in increasing order of confidence:

**Hand-written**, before you have ever run the request:

```sh
sapien example add order-service.createOrder --id create-qcom-order \
  -p customerId=c1 --body '{"type":"QCOM","pickup":{"lat":12.97,"lng":77.59}}' \
  --tag qcom --tag happy-path
```

Flags: `--id` (default: derived from the operation), `--description`,
`--tag` (repeatable), `--scope` (default `workspace`), `-p key=value`
(repeatable), `--body json|@file`, `-H 'Key: value'` (repeatable). This
form never carries `verified`.

**From a call**, capturing the run that just happened:

```sh
sapien call order-service.createOrder -p customerId=c1 \
  --save-example create-qcom-order --tag qcom
```

`--save-example <id>` saves the call's result as a verified example,
whatever the run's outcome, alongside any `--save-as` you also passed.
It accepts the same `--scope`, `--description`, and `--tag` flags as
`example add`.

**From a past run**, when the call that proved it out already
happened, possibly as part of a flow:

```sh
sapien run save-example run_01J8Z5K3W2RQ4X7M9N --step create \
  --name create-qcom-order
```

`--step` defaults to the run's only (or first) step. Both save-from-run
paths print:

```
saved example create-qcom-order (verified against stage, status 201) -> <workspace>/examples/create-qcom-order.example.yaml
```

`sapien example add` and `sapien call --save-example` both end with a
reminder of how to use what was just saved, plus the same "local to
this machine" note the memory commands print when a workspace-scoped
item has no git history to carry it:

```
use it: `sapien call --example create-qcom-order`, or in a flow step: `example: create-qcom-order`
```

From an MCP host, the equivalent is the `create_example` tool: pass a
`run_id` (and optionally `step_id`) to save a verified example the same
way `run save-example` does, attributed to that client. `list_examples`,
`get_example`, and `rescope_example` are the MCP equivalents of `example
list`, `example show`, and `example rescope`. See [`mcp.md`](mcp.md).

## Replay

```sh
sapien example list
sapien example list --operation order-service.createOrder --tag qcom
sapien example show create-qcom-order
sapien example rm create-qcom-order
```

`example list` filters: `--operation`, `--service`, `--tag`, `--text`
(substring over id, description, tags). `example show --json` returns
the full struct; without `--json` it prints the same YAML the file on
disk carries.

**Directly**, as a one-step run:

```sh
sapien call order-service.createOrder --example create-qcom-order
sapien call order-service.createOrder --example create-qcom-order -i city=Bengaluru
```

`--example <id>` loads the example's `input`/`body`/`headers` as the
base for the call; an explicit `-p`, `--body`, or `-H` on the same
command line overrides it. `-i key=value` supplies `${inputs.x}` values
for a templated body, the same way `flow run -i` does.

**In a flow**, a step may set `example: <id>` instead of spelling out
`call`/`input`/`body`/`headers` itself; the example fills those in, and
any field the step sets explicitly overrides the example's. The
validator resolves the id the same way it resolves an operation
reference, with "did you mean" suggestions when it does not exist.

## Why `verified` matters

An unverified example is still useful, a starting point someone typed
by hand, but it has not been checked against anything real: the
contract validation on `example add` confirms the shape is plausible,
not that the service accepts it. Prefer a verified example when one
exists (`example list` sorts them first); when you save one from a call
or a run, you are turning a guess into a fact the next reader, human or
agent, can trust without re-deriving it.
