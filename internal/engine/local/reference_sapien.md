# What Sapien is, and what you can do with it

Sapien is a shared index of the services in an organisation, reachable
from any repository over MCP. For each service it holds the contract
(every endpoint, parameter, field, and status code), the service's own
narrative documentation, request examples that are known to work, the
memories people and agents have written about using it, and flows -- saved,
runnable sequences of calls.

It exists because the thing that blocks work in a repo is rarely this
repo. It is the service next door: which endpoint to call, what the
payload looks like, what the `409` means, and what has to exist before the
call can succeed. That knowledge is normally spread across another team's
codebase, a Swagger page, a Slack thread, and somebody's memory. Sapien is
where it is written down once so that the agent in the next repository
does not have to rediscover it.

You are in one of two roles, usually the first.

## Consuming a service you do not own

The path is: work out what you need, find the operation, read what is
known about it, then call it or write a flow.

1. **`get_context(intent)`** -- one call that bundles the likely
   operations, doc sections, memories, examples, and existing flows for a
   stated intent ("cancel an order and refund it"). Start here; it saves
   the four or five searches you would otherwise do by hand.
2. **`search_apis(query)`** -- find operations by intent ("find riders
   near a pickup"), by field name, by error code, or by URL. A pasted URL
   works, including a full staging URL with a query string.
3. **`get_api(id)`** -- one operation in detail: params, body and response
   fields, security, and a **`request_example`**: a ready-to-send payload,
   labelled with where it came from (a verified example that really was
   sent and worked, a saved one, the contract's own example, or one
   synthesized from the schema). Do not assemble a body from the field
   list when this is sitting there.
4. **`search_docs` / `get_doc`** -- the service's own prose: the business
   rules, the error catalogue, the ordering constraints, the traps. This is
   where the answer to "why is this failing" usually is, and it is the
   half of the picture the contract cannot carry.
5. **`get_relevant_memories(operations)`** -- what people and agents have
   learned since. A memory typed `invariant` or `testing` on an operation
   you are about to use is an assertion waiting to be written.
6. **`list_examples` / `get_example`** -- payloads that already worked.
7. **`list_flows` / `get_flow` / `run_flow`** -- an existing multi-step
   sequence. Check before writing your own; flows are living runbooks, and
   somebody else may have already paid for the discovery.
8. **`execute_api`** -- make one call against a configured environment.

Two habits make the difference. Never invent an operation id, a field, or
an error code: use the ids the tools return, because a plausible-looking
guess is exactly what these tools exist to prevent. And when you get a
call working, save it -- `create_example(run_id)` -- so the next agent
starts from a payload instead of a schema.

## Calling through Sapien rather than your own script

A shell script with curl does work. Doing it through `execute_api` (or
`sapien call`) is usually less work and always worth more afterwards:

- The environment resolves the base URL, the headers, and the secrets, so
  there is nothing to paste and nothing to leak into a transcript.
- The request and response are recorded as a run you can point another
  tool at (`get_run`), rather than scrollback.
- A failure comes back with the contract's own diagnosis -- a bad field
  name, a missing required param, a hint from a matching memory -- instead
  of a bare status code.
- A call that worked becomes a saved example in one step, which is how the
  next agent avoids the work you just did.
- Production is refused unless the environment is explicitly allowed, so
  the mistake that matters is hard to make by accident.

Write a flow instead of a script when there is more than one call and the
result matters twice: flows assert, extract values between steps, poll
until a condition holds, have setup and teardown, resume from a failed
step, and can be re-run by anyone later. See
`get_dsl_reference("flow")`.

## Onboarding a service you do own

Onboarding writes an `api/` package into the service's own repository --
contract, metadata, narrative docs, examples -- and registers it. After
that, every agent in every other repo can find and call the service
correctly, and `get_context` can answer questions about it.

The reader of what you write is an agent in another repository that has
never seen the code, so the docs must carry the business logic, not
restate the contract. Read `get_dsl_reference("service")` before starting:
it has the layout, the documentation requirements, the questions to ask
the service's owners, and what the coverage warnings mean.

## Where memories fit

A memory is a durable note attached to operations, fields, or a service:
an invariant, a gotcha, a testing convention, something that broke once.
Sapien surfaces them next to the operation they concern, and they feed
search ranking, so writing one is how a discovery survives past the end of
your session. `get_dsl_reference("memory")` has the types and scoping
rules.

## What Sapien does not do

It does not proxy traffic, replace a gateway, or hold runtime state. It
does not verify that a contract matches the running service -- a contract
can be wrong, which is why a verified example (one that really was sent)
outranks the contract's own. And it never guesses: if a tool did not
return an id, that id does not exist as far as Sapien is concerned.

## Reference topics

| Topic | Covers |
|---|---|
| `get_dsl_reference("sapien")` | this page |
| `get_dsl_reference("flow")` | the flow DSL: steps, assertions (including soft ones), setup/teardown, polling |
| `get_dsl_reference("expressions")` | the `${...}`/CEL language inside a flow |
| `get_dsl_reference("memory")` | memory types, scoping, and what is worth recording |
| `get_dsl_reference("service")` | the `api/` package: contract, docs, examples, and how to onboard a service |
