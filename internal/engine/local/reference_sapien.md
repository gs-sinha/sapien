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

### Choosing whether to use embeddings

`search_apis` always uses the local SQLite index, including authored `tasks:`
phrases. That path needs no model and is the default. If the machine has spare
capacity and paraphrase matching would help, semantic search can be enabled in
the workspace's `.sapien/config.yaml`:

```yaml
semantic:
  enabled: true
  kind: ollama
  base_url: http://localhost:11434
  model: nomic-embed-text
  batch_size: 8
```

When enabled, one background worker embeds operations and docs. Task phrases
enrich their target operation's existing vector, so they do not create a
second vector per task. Results helped by embeddings include `semantic` in
`matched_on`. Set `enabled: false` or omit the block for SQLite-only search;
then Sapien starts no semantic worker and makes no embedding calls. The same
block in `~/.sapien/config.yaml` supplies a user default, and a workspace can
override it with `semantic: {enabled: false}`. Restart the Sapien daemon after
changing this setting.

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
In a shared workspace `add_service` with a checkout path commits the
checkout's origin as the team's git source and binds the checkout on this
machine, so the service indexes at once and teammates receive it when the
`api/` package is pushed; pass `local` for a path-only source.

## Where memories fit

A memory is a durable note attached to operations, fields, or a service:
an invariant, a gotcha, a testing convention, something that broke once.
Sapien surfaces them next to the operation they concern, and they feed
search ranking, so writing one is how a discovery survives past the end of
your session. `get_dsl_reference("memory")` has the types and scoping
rules.

## Where your work lives: tiers and bindings

A team commits one workspace that lists every service as a git source.
That is the team's view: Sapien reads each service from a managed clone
it refreshes from GitHub, and that clone is read-only -- nothing you
write into a service read this way can survive, so service scope is
refused there. `list_services` and `get_service` say what each service is
read from: `team` (the git source, read-only) or `local` (a checkout on
this machine, writable, with its branch). When the user asks you to read a
service from their checkout, call `bind_service` with the absolute path
(the service is inferred from the checkout's origin; the origin must be
that service's repository); `find_checkouts` lists the clones this machine
knows with their branch, last commit and drift so you can ask which one
they mean, and `unbind_service` returns to the team source. From then on
service-scoped memories, examples and flows land in that checkout's
`api/` and ship in their pull request. If you need to record something
about a read-only service, use workspace scope with a service subject; a
human can promote it later.

Flows climb a ladder. `create_flow` writes to the local tier by default
(`<workspace>/local/flows`, this machine only, never committed): run it
there until it is green, then `rescope_flow` to `workspace` so the team
gets it, or to `service` when that service is bound. A workspace-tier
flow carries `shipped` (not committed, modified, not pushed, shipped);
`commit_flow`, or `rescope_flow` with `commit`, records the file in the
workspace repository when the user asks, one file per commit, never a
push. Memories climb the
same way with `rescope_memory`: personal (this machine) -> workspace (the
team's repo) -> service (the owning repo). Nothing is committed or pushed
by Sapien; what reaches the team is what the developer commits.

## When Sapien itself gets in your way

If a tool returned the wrong shape, a capability was missing, or the
documentation misled you, file it with `report_friction`. The report is
queued on this machine and never posted by you; a human reviews it and
may publish it as a GitHub Discussion on the Sapien repo, so keep
secrets, hostnames and payloads out of it.

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
