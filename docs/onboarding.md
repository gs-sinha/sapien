# Onboarding a service

Most services do not already have Sapien-style API docs, and writing
them by hand from an OpenAPI spec that may not even exist yet is slow.
The intended way to onboard a service is to have an agent do it, working
directly in that service's own repo where it can read the real routes,
handlers, and types. This is the human-facing walkthrough; the exact
package layout and rules an agent follows live in
[`internal/engine/local/reference_service.md`](../internal/engine/local/reference_service.md),
served over MCP as `get_dsl_reference("service")`.

This only works once the Sapien MCP server is installed for your agent
host. See "Install the MCP server everywhere" in
[`getting-started.md`](getting-started.md) if you have not done that
yet; with the default `--scope user` install it is already available in
every repo, including the one you are about to onboard.

## What to say to the agent

Open Claude Code, Codex, or Cursor in the service's repo, the same repo
whose code implements the API, and ask it in plain language. Any of
these work:

> Onboard this service to Sapien.

> Write Sapien-style API docs for this service and add it to Sapien.

> This service has no OpenAPI spec. Read the code and write one, then
> register it with Sapien.

You do not need to explain the package layout or mention `add_service`.
The MCP server's own instructions, sent to every connected host, already
tell the agent what to do when a user asks to onboard a service.

## What the agent does

1. Calls `get_dsl_reference("service")` to learn the exact package
   layout and the conventions docs must follow to link to the contract.
2. Reads the routes, handlers, request and response types, and error
   handling in the repo, and writes:

   ```text
   api/openapi.yaml   the contract, with an example: on every request body
   api/service.yaml   name, description, owners, concepts, environments
   api/docs/*.md      overview.md plus one file per domain area
   ```

3. **Asks you one round of questions.** This is the part that decides
   whether the docs are worth anything. The agent can read what an
   endpoint takes and returns; it cannot read why the endpoint exists, who
   calls it, whether calling it twice is safe, which errors are normal, or
   what everyone gets wrong the first time. It asks after drafting from
   the code, so the questions are specific ("`allocate` has no idempotency
   key and no unique constraint on `order_id` -- is calling it twice for
   one order safe?"), grouped by area, and answerable in a sentence.

   Answer what you know and say so when you do not: anything unanswered
   goes into an `## Open questions` section in the docs rather than being
   guessed at, which tells the next reader where the edge of the knowledge
   is. The round is bounded, and onboarding does not stall waiting for it.

4. Calls `add_service` with the repo's absolute path. The tool returns
   the operation count, a coverage line (see "Coverage" below), and any
   warnings -- most commonly an operation missing an `operationId`, a doc
   that references a path the contract does not have, an operation no doc
   mentions, or a request body with no example. If the service is already
   registered, `add_service` re-syncs it instead of failing, and says so.
5. Closes the coverage gap, and for each warning: fixes it if it points at
   a real gap, or, if the warning already describes the API correctly or
   the gap is deliberate, accepts it in `api/service.yaml` with a reason
   instead of editing the contract to make the warning go away (see
   "Warnings" below). Then confirms the result with `get_service`, a
   `search_apis` query you would plausibly type, and `get_api` on the most
   important operation to see the request example a caller will be handed.
6. Adds a "Sapien" section to the repo's `CLAUDE.md` or `AGENTS.md`,
   which `add_service` hands back ready to paste. It tells the next agent
   that touches the code to update `api/openapi.yaml` and `api/docs` in
   the same change, to keep `operationId`s stable, and to read what
   Sapien already knows before editing `api/`. This is what keeps the
   docs current after onboarding.
7. Records anything it learned that does not belong in the contract or
   the docs, such as an operational quirk or an invariant, as a memory
   with `create_memory`.

Nothing here executes a request against the service; onboarding only
reads the repo and writes files plus one `add_service` call. Commit the
`CLAUDE.md` or `AGENTS.md` change together with `api/`.

## Coverage

A contract that lints clean can still be useless to the next person. So
`add_service`, `sapien service add`/`sync`, and `get_service` also report
how much of the service is *understandable*:

```text
allocation-service: 24 operations, status ok
docs: 18/24 operations documented in api/docs (31 sections)
examples: 15/19 operations that take a body have a request example
```

An operation counts as documented when some `api/docs` section mentions it
-- by operation id, or by mentioning its endpoint. The contract's own tag
and `info` descriptions do not count; that is the contract restating
itself. Deprecated operations are left out of the totals.

Four warnings name the specific gaps: `NO_NARRATIVE_DOCS` (no
`api/docs/*.md` at all, reported once rather than per operation),
`UNDOCUMENTED_OPERATION`, `MISSING_REQUEST_EXAMPLE`, and `NO_CONCEPTS`.
They are lint like any other -- acceptable with a reason, never a reason
the registration fails. An internal endpoint no outside caller should use
is a legitimate thing to leave undocumented; accepting it says so on the
record instead of leaving the number unexplained.

These numbers are the honest measure of an onboarding. 24 indexed
operations with 6 documented means the service is findable and not yet
usable.

## Request examples

Every operation that takes a request body should carry an `example:` in
the contract, written during onboarding from the code:

```yaml
  requestBody:
    content:
      application/json:
        schema: { $ref: "#/components/schemas/CreateOrderRequest" }
        example:
          customerId: cust_8f21
          type: QCOM
```

It lives in the contract because it travels with the code and every
OpenAPI tool shows it. Sapien serves it wherever a payload is needed:
`get_api` returns a ready-to-send `request_example`, and the UI's "Try it"
form opens prefilled from it instead of with an empty textarea. The
precedence is: a verified example (one really sent, saved from a real run
with `create_example`), then a hand-written saved example, then the
contract's, then a payload synthesized from the schema -- and the UI and
`get_api` both say which one you are looking at, because "verified" and
"placeholders derived from the schema" deserve different amounts of trust.

## Warnings

`add_service` and `sapien service add`/`sync` report warnings: lint
findings, never a reason the registration fails. An operation without an
`operationId`, a response with no `application/json` content, a doc that
mentions a path the contract does not have, and similar all show up this
way. `status: ok` means the service synced, not that every warning has
been addressed, so a service can carry warnings indefinitely and that is
fine.

What is not fine is treating a warning as something to make disappear
rather than something to look at. A real onboarding session found three
of four services with warnings that were correct and permanent (an
endpoint that genuinely returns `text/plain`, say) alongside one where
the agent, chasing "zero warnings," rewrote twenty real `text/plain`
responses to claim `application/json`, which cleared the warning by
making the contract lie about the wire. Never let an agent do that. If a
warning is telling the truth, the fix is to accept it, not to edit around
it.

Accept a warning in `api/service.yaml` under `accepted_warnings`, with a
reason a reviewer can check:

```yaml
accepted_warnings:
  - code: UNSUPPORTED_MEDIA_TYPE
    match: "text/plain"
    reason: "These endpoints return text/plain by design; the contract describes the wire faithfully."
```

`code` and `reason` are required; `match` is optional and narrows
acceptance to warnings whose message or source mentions it, so one entry
does not accidentally wave through an unrelated warning that happens to
share the same code. An accepted warning still shows up, just separately:
`get_service` returns it under `accepted_warnings` with its reason, and
`sapien service list` has an `ACCEPTED` column. If you later fix the
underlying issue or remove the endpoint, the acceptance itself becomes a
new warning, `STALE_ACCEPTANCE`, telling you which entry to delete, so an
old acceptance can never quietly cover for a warning that no longer
applies. Run `sapien service sync <name>` (or `sync_service` over MCP)
after editing `accepted_warnings` the same way you would after editing
the contract.

## Reviewing the generated `api/` package

Treat the generated `api/` package like any other change an agent makes
to your codebase: read the diff before you commit it.

- Open `api/openapi.yaml` and check that every endpoint the service
  actually serves is covered, that status codes match what the handlers
  return, and that nothing was invented. The reference instructs the
  agent never to invent endpoints, fields, or status codes, but you are
  the one who knows the service, so verify it.
- Read `api/docs/*.md` for accuracy, especially business rules,
  invariants, and error handling. These are what another agent, or a
  teammate, will rely on later. Check `## Open questions` too: it is the
  list of things the agent could not establish, and it is usually the
  shortest useful to-do list you will get about your own service.
- Check `api/service.yaml` for a sensible `name`, `description`,
  `owners`, and `concepts`. `concepts` are the words someone would type
  when they do not know the operation name, so make sure they match how
  your team actually talks about the service.
- Run `get_service` and a couple of `search_apis` queries yourself, in
  the agent session or with `sapien search` from a terminal, to confirm
  the service reads the way you expect.

Once it looks right, commit `api/` in the service's repo like any other
source change.

## Keeping it current

The workspace's daemon watches every registered service's `api/`
directory and re-indexes it automatically when a file changes, so
editing `api/openapi.yaml` or a doc and saving is usually enough.

When you want to force a re-index right away, for example right after a
git pull, run:

```sh
sapien service sync <name>
sapien service sync                 # every service
```

If the code changes in a way that the docs no longer reflect, the fix is
the same as onboarding the first time: ask the agent to update the
service's Sapien docs from the current code, review the diff, and commit
it.

## Sharing with your team

A workspace is meant to be shared, and there are two things to commit:

- **The `api/` package, in the service's own repo.** It is ordinary
  source, reviewed the same way as any other pull request, and it
  travels with the code it documents.
- **The workspace folder, as its own git repo.** This is where
  `sapien.workspace.yaml`, `flows/`, `memories/`, and `environments/`
  live. Committing it means your team shares the same catalog of
  services, the same flows, and the same captured memories, without
  everyone re-onboarding the same services individually.

For a service repo a teammate does not have checked out locally, add it
to the workspace with a git URL instead of a local path:

```sh
sapien service add git@github.com:your-org/some-service.git \
  --ref main --subdir api
```

Sapien clones it into a managed cache under `~/.sapien/repos` and keeps it in
sync with `service sync`; nobody needs a local checkout just to have the
service show up in search, `get_context`, and flows.
