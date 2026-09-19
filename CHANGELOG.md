# Changelog

All notable changes to this project are documented in this file. The
format loosely follows [Keep a Changelog](https://keepachangelog.com/),
and phase numbers refer to PLAN.md §34's roadmap.

## [Unreleased]

## [1.4.0] - 2026-09-20

### Added
- **The agent pane starts `opencode`** as well as `claude`, `codex` and a
  shell, and finds it in `~/.opencode/bin` when a launcher's PATH lacks it.
- **A Changes page: the workspace repository like an editor's source-control
  panel.** One tree of every changed file -- flows, memories, examples,
  `sapien.workspace.yaml`, `environments/`, `.gitignore` -- with change marks
  that roll up folder by folder to a badge in the nav. Tick files or folders,
  write a message (one is suggested, and a file moved to another folder reads
  as a move, not an add and a remove), Commit, Pull, Push, and see each
  file's diff beside it. Bound service checkouts are listed read-only.
  `sapien workspace changes` and `sapien workspace commit -m <msg> [--all |
  paths...]` do the same from a shell. Still a human's request, the workspace
  repository only, never forced, and no MCP tool.
- **Folders for flows, memories and examples.** Any of them may live in a
  subfolder at any tier; the folder is read from the path and ids stay
  global, so moving never breaks a reference. The list pages get a folder
  sidebar, a breadcrumb, "Move to folder..." per row, multi-select "Move N to
  folder...", and drag a row (or the selection) onto a folder. `sapien
  flow|memory|example mv`, `--folder` on list and create, `folder` on the MCP
  create, rescope and list tools, and folder names are lexical search text.
  A service sync now finds a teammate's flow in a subfolder.
- **Conditions and loops in flows.** `when:` on a step skips it when false
  (recorded `skipped`, never a failure; the validator warns about an
  unguarded read of a step that may be skipped). A step with `steps:` is a
  loop block: `foreach: <list>` or `repeat: {until|while, max, interval}`,
  with `break_when`, `on_error` and a mandatory cap; inside, `iter.item` and
  `iter.index`, and `steps.<id>` is always the latest execution, so
  iteration N reads iteration N-1's cursor; after it, `steps.<block>.count`
  and `.iterations`. Run steps are keyed by iteration, runs resume at a block
  boundary, `patch_flow` reaches nested steps and adds `into:`, and an
  agent's view of a run collapses passing iterations to one line while
  keeping every failed one.
- **A Settings page.** Semantic search: turn it on, off or to another model
  with no daemon restart; it finds Ollama, lists and pulls models with
  progress, tests a config before saving it, and shows indexing live. Choose
  what is embedded (operations, examples, memories, doc sections) and edit
  the task prefixes, which otherwise follow the model. Daemon: version,
  uptime, load, and Restart. Updates: what is running, what is latest, a
  daily check that can be turned off, and an upgrade button for a script
  install (other installs are shown their own command). `sapien semantic
  status|enable|disable|reindex|test`, `sapien daemon restart`, `sapien
  upgrade`.
- **Change a service's branch from its page.** A git-sourced service's ref
  can be switched to any branch or tag of its remote, for this machine only
  (a `ref:` in `sapien.workspace.local.yaml`, with its own clone) or for the
  team (rewrites `sapien.workspace.yaml`). `sapien service set-ref`,
  `sapien service branches`.
- **`http://sapien.localhost:7717`.** `*.localhost` names pass the host
  guard, `sapien ui` opens that address (`--loopback-ip` for `127.0.0.1`),
  and the daemon's token is kept across restarts, so open tabs and MCP
  bridges survive one and the session cookie lasts 90 days.

### Changed
- **Semantic search is no longer an install step**, and sends each model the
  task prefixes it was trained with (`search_query:` / `search_document:`
  for `nomic-embed-text`, and so on); before, every model was sent bare
  text. A saved example's description is now embedded with the operation it
  calls. The index catches itself up when the daemon starts, so an upgrade
  that changes how texts are built needs no manual reindex.
- **`scripts/install.sh` upgrades in place**: says so and stops when already
  current, installs over the existing binary's directory, defers to `brew
  upgrade` for a Homebrew install, refuses to overwrite a dev build, and
  restarts a running daemon.
- Warnings on the service page and docs on the operation page collapse, and
  remember it per browser.

### Fixed
- **One memory longer than the embedding model's context failed the whole
  index** with Ollama's "the input length exceeds the context length" (it
  refuses even with `truncate` set, once an input is far enough over). A
  refused batch is now retried input by input, the over-long one embedded
  from its head, and the length that fit is remembered so later ones are
  clipped before they are sent; no table of per-model limits is needed.
  Settings also gains "Keep the model in memory" (`semantic.keep_alive`), so
  Ollama can give back the model's RAM sooner than its five-minute default.
- **A `${...}` template inside a bare CEL assertion, an `expr:`, `until`,
  `when`, `extract` or a loop field was never expanded**, so
  `body.id != "${steps.a.out.id}"` compared against the literal text and
  could never fail -- a flow could be green on assertions that checked
  nothing. Templates now expand everywhere (one template filling a whole
  string literal keeps its native type, as `eq:` does), the validator sees
  the references inside them, and a `${` that survives is an error
  (`TEMPLATE_IN_EXPR`). Structured assertions gain `lt`, `lte`, `gt`, `gte`,
  the comparisons that pushed authors into bare CEL. (Discussion #8.)
- **`out.<name>` was always empty in a step's own `assert` and `until`**,
  though documented as available: assertions ran before extraction.
  Extraction now runs first, tolerantly -- a failed assertion is still the
  primary outcome, a read of an `out` entry that failed to extract says why,
  and the validator rejects an `out.<name>` the step does not extract
  (`UNKNOWN_OUT`). (Discussion #6.)
- **`patch_flow`'s `add_step` refused an anchor it had just listed as
  present** when `before`/`after` named a step in another phase. Anchors
  resolve across the whole flow, nested blocks included, and the new step
  goes where the anchor lives; a contradicting `phase`/`into` says where that
  is. Patching keeps the file's indent width, writes new keys in the DSL's
  conventional order rather than alphabetically, keeps comments with their
  steps, and reports as `notes` a comment that went with a replaced step or
  stayed above a changed one. Blank lines between steps are still lost
  (yaml.v3). (Discussion #4.)
- The first save of a model Ollama had not loaded yet was refused: the
  pre-save probe gave up after 10s while the model was still loading. It
  waits 90s, and says why it is waiting.
- A build ahead of its release tag (`v1.3.1-43-gabc1234`) was offered that
  same tag as an update.
- The acceptance test read the developer's own `~/.sapien/config.yaml`.

## [1.3.1] - 2026-09-15

### Fixed
- **`.sapien/` is gitignored at the workspace root.** `sapien init` now
  writes `.sapien/` into the root `.gitignore` beside
  `sapien.workspace.local.yaml`, and binding or adding a service adds it to
  older workspaces. The `*` file inside `.sapien/` did not cover a clone of a
  workspace repository, which creates `.sapien/` on first open without it.
- **`sapien workspace forget` and `close` no longer panic when no daemon is
  running.**

## [1.3.0] - 2026-09-15

### Added
- **`sapien mcp config --allow-mutations`: one command to let agents run
  the flows they write.** Agents were denied every POST, PUT, PATCH and
  DELETE by default and the only way to allow them was hand-editing
  `.sapien/mcp.yaml`, so a new user's first "write a flow and run it" failed.
  The flag sets `default.execute_mutation: true` in the workspace's
  gitignored `.sapien/mcp.yaml` (creating it, or editing it with its other
  keys and comments kept), for non-production environments only, and warns
  when `~/.sapien/config.yaml` or a client entry still denies it. It works
  with `--client` alongside the host entry or on its own, and the
  `execute_mutation` denial now names it. The default is unchanged.
- **Team workspaces: one committed composition, per-machine bindings.** The
  committed `sapien.workspace.yaml` lists every service as a git source, the
  team's view; a gitignored `sapien.workspace.local.yaml` binds a service to
  a local checkout on this machine (`sapien service bind <name> <path>`,
  `unbind`, the service page's Source panel, `PUT/DELETE
  /v1/services/{name}/binding`). A bound service is read from the checkout,
  watched on every save, and writable for service-scoped knowledge, so a
  contribution rides the developer's own branch and pull request. Every
  service now says what it is listening to (`local` with branch, commit and
  uncommitted count, or `team` with the pinned ref) and what it could listen
  to, with candidate checkouts found from other registered workspaces.
- **Binding is validated and browsable.** A bind refuses a checkout whose
  origin is another repository, or which has no API package yet, unless
  forced; every checkout shows its last commit time, ahead/behind the team
  ref and whether it is a worktree, so a stale backup clone is obviously
  stale. The service page gained a daemon-side directory picker (`GET
  /v1/services/{name}/checkouts`) that annotates each repository with
  whether it is this service's. `sapien service bind <path>` alone infers
  the service from the checkout's origin. Agents get `bind_service`,
  `unbind_service` and `find_checkouts` over MCP.
- **`service add <path>` is team-aware.** In a shared workspace (one inside
  a git repository with a remote) it commits the checkout's origin as a
  git source and binds the checkout on this machine, so a new hire onboards
  a service before pushing it and the shared file never learns an absolute
  local path. `--local` keeps the old behaviour; `add_service` follows the
  same rule unless `local` is set.
- **Promoted flows show whether they shipped.** A workspace-tier flow
  carries `shipped`: not committed, modified, committed but not pushed, or
  shipped, from a read-only git status of the workspace repository, on the
  flows page, in `flow list` and in `list_flows`. `sapien flow promote
  --commit` (and the "and commit" checkbox, and `rescope_flow` with
  `commit`) also commits the moved file in the workspace repository;
  opt-in, never a push, never a service repository.
- **A team-tier flow can be committed on its own.** `sapien flow commit
  <id>` (`--all` for every uncommitted one), `POST /v1/flows/{id}/commit`,
  the `commit_flow` MCP tool and a Commit button beside the `not
  committed` / `modified` badge record the file in the workspace
  repository without moving it, so nobody has to demote and re-promote a
  flow to commit it. Same rules: one file per commit, never a push.
- **The workspace repository is fetched on the tick and pulled on request.**
  The daemon's git tick now also runs `git fetch` on the workspace's own
  repository, read-only. The status bar shows the branch, how many team
  commits are waiting, how many of yours are unpushed, and uncommitted
  files; a Pull button appears when the tree is clean and the branch is
  behind (`merge --ff-only`, then the workspace tier is reindexed). "Sync
  all", `sapien service sync` and `sapien workspace sync` also sync the
  repository: fetch, then pull when clean and behind, otherwise say why
  not. `sapien workspace status` and `GET /v1/workspace/repo` report it.
  Never a push.
- **Memories and examples climb the same ladder as flows.** A
  workspace-scope memory or example lives in the local tier
  (`local/memories`, `local/examples`, this machine only, the default for
  a new one) until it is moved to the team's `memories/` or `examples/`
  (`sapien memory move <id> --to team`, `sapien example move`, the rescope
  tools with `tier`, a button on the pages). A workspace-tier file shows
  whether it is not committed, modified, not pushed or shipped, and can be
  committed on its own (`memory commit`, `example commit`, `commit_memory`,
  `commit_example`, the Commit button). Personal memories have no tier.
- **Push from the product.** `sapien workspace push`, `POST
  /v1/workspace/repo/push`, and a Push button beside any "not pushed"
  badge and in the status bar send the workspace repository's unpushed
  commits to its upstream: never forced, refused when the branch is
  behind, only the workspace repository. Sapien still never pushes on its
  own; this is the human's click.
- **The Agent tab offers only writable directories.** The workspace, each
  service this machine reads from a local checkout (a bound service at its
  checkout), and home. A service still read from its team git source no
  longer appears: its package directory is a managed clone the daemon
  resets, so an agent started there would have been working in a cache.
  The `api/` subdirectory entries are gone too; the repository is the
  place to work.
- **Flow tiers.** A new flow lands in `local/flows` (this machine, ignored
  by git) by default; `sapien flow promote`, the `rescope_flow` MCP tool and
  the flow page move it to the team's `flows/` and, when the owning service
  is bound, into that service's `api/flows`. `scope: flow` memories follow
  their flow.
- **`report_friction`: agents can file feedback about Sapien itself.** An
  agent that hit a wrong tool shape, a missing capability or misleading
  docs queues a report under `~/.sapien/friction`; nothing leaves the
  machine. A human reviews it with `sapien friction list` / `show` and
  posts it as a GitHub Discussion on the Sapien repo with `sapien friction
  send` (through the `gh` CLI, which holds the auth; `friction.repo` and
  `friction.category` in the user config choose where). Reports that look
  like they carry a secret are refused at creation, since they go public.

### Changed
- **The inspector wears the website's theme.** Warm paper and stone
  neutrals, one moss accent for links and selection, the same system font
  stacks, tight headings and tracked labels, in light and dark. It is done
  in the Tailwind palette (`slate` is the stone scale, `sky` the accent), so
  component classes barely changed. The nav carries the 🗿 mark, JSON and
  YAML use the site's syntax colours (blue keys, amber strings, moss
  `${...}`), and the Agent terminal uses its code-panel colours.

### Fixed
- **A commit on a machine with no git identity says how to set one.**
  `flow commit`, `memory commit`, `example commit` and promote `--commit`
  failed with a bare `exit status 128` when git had no user to commit as
  (a fresh machine or container); the error now carries the two
  `git config` lines to run. The commit tests set their own identity, so
  they no longer depend on the machine they run on.
- **A forgotten workspace stays closed.** One daemon serves many
  workspaces and opened any directory a request header named, so a stale
  browser tab or an old MCP bridge kept reopening a workspace the user had
  shut down, taking its lock again. A request header now opens only the
  primary or a registered workspace; `POST /v1/workspaces` is the explicit
  way to open and register another. `sapien workspace forget` also closes
  the workspace on the running daemon, and `sapien workspace close` (and
  `DELETE /v1/workspaces?dir=`) closes one that stays registered.
- **`switch_workspace` survives a daemon restart, and a failed switch says
  so.** The first friction report an agent filed against Sapien
  (discussion #1): the stdio MCP bridge's workspace switcher kept the
  bearer token from when the session started, so after the daemon was
  replaced every switch failed with a 401 while every other call, which
  re-resolves its token, kept working; `list_workspaces` hid it by falling
  back to the local registry. The switcher now re-resolves and retries
  like the main client, a failed switch answers "still bound to <previous
  workspace>", and `create_memory`, `create_flow` and `create_example`
  name the workspace they wrote into.
- **`delete_memory` over MCP**, the counterpart of `sapien memory rm`; the
  same report had to park a misplaced memory at personal scope for lack
  of it.
- **Nothing is written into a managed git clone any more.** A service read
  from its git source is read-only for service-scoped memories, examples and
  flows: the daemon `reset --hard`s that clone every ten minutes, so a doc
  promotion written into it was reverted and a new memory was stranded where
  nothing pushes from. The write now fails with a hint to bind a checkout or
  use workspace scope, and the sync refuses to reset a clone that carries
  modified tracked files rather than discarding them.

## [1.2.0] - 2026-09-13

### Added
- **`/debug/pprof/*` and `/debug/memstats` on the daemon**, behind the same
  loopback Host check and bearer token as every other route, and
  deliberately outside the OpenAPI route table (Go's profile format is not
  Sapien's API to promise). There was previously no way to ask a running
  daemon anything about its own heap, which is why the allocation churn
  under Fixed had to be diagnosed from `vmmap`; `heap_alloc` against a
  climbing `total_alloc` answers "leak or churn?" in one request.
- **`daemon.memory_limit` (default `2GiB`)**, a soft heap ceiling
  (`GOMEMLIMIT`) the daemon applies to itself, plus a five-minute check that
  returns the heap to the OS once the daemon stops working -- after a burst
  of indexing nothing allocates, so nothing triggers a collection, and the
  process would otherwise sit on its high-water mark for hours. Set it to
  `0`/`off` for the old unbounded behaviour; a `GOMEMLIMIT` already in the
  environment always wins.
- **`sapien ui --install-app`: a macOS launcher for the inspector.** Writes
  `~/Applications/Sapien.app`, so the UI opens from Spotlight, the Dock or a
  Raycast hotkey instead of only from a terminal. It shells out to `sapien
  ui` rather than holding a URL, because a URL cannot survive: the daemon
  mints a fresh bearer token on every start and exits after thirty minutes
  with nothing connected, so a bookmark or a PWA install is stale within the
  hour. The bundle records the absolute path it was installed from (an app
  launched by Finder inherits none of the login shell's `PATH`, so a bare
  `sapien` would never be found under Homebrew) and logs a failed launch to
  `~/Library/Logs/Sapien/launch.log`, since a Finder launch has no terminal
  to fail in. Re-run it to repoint the launcher at another workspace.
- **The workspace picker is per tab.** Several tabs is how you multitask in
  the inspector -- a run in one, a flow in another -- but the selection
  lived in `localStorage`, so switching in one tab silently moved every
  other tab on its next reload, and ids in those tabs' URLs then resolved in
  the wrong workspace. It now lives in `sessionStorage`, which is per tab
  and survives reload; `localStorage` keeps the last choice only as the seed
  for a brand-new tab.
- **🗿 as the icon**, in the browser tab (`favicon.png`, a PNG rather than an
  SVG data URI because Safari does not render SVG favicons) and on the macOS
  launcher, whose `.icns` is embedded in the binary and written into the
  bundle's Resources.
- **A banner when a tab outlives its daemon.** `/v1/health` (the one route
  outside auth) is probed on load to record which build served the tab, and
  again once the event socket has actually failed to reconnect -- not on a
  timer. A different version answering means an upgrade replaced the daemon
  and the tab is running the old UI: it now says so and offers a reload.
  Nothing answering means the daemon idle-exited: it says that instead of
  retrying in silence. A third state found while testing this: the daemon
  can be reachable *and* the right build while this tab's cookie is for a
  previous one, since `serve` mints a new token on every start -- /v1/health
  is unauthenticated, so the app looked connected while every real request
  401ed. `api/client` now reports a 401 to the same store, and clears it on
  the next success, which is what the relaunch's own `/ui/session` tab does
  for every tab at the origin.
- **`get_dsl_reference("sapien")`: what Sapien is and what an agent can do
  with it.** Agents had the tools without the framing -- that Sapien is a
  cross-repo discovery layer holding contracts, the services' own
  documentation, working request examples, memories and runnable flows -- so
  they used it as a schema lookup and then called services from throwaway
  scripts. The new topic (also `sapien://reference/sapien`) covers
  consuming a service you do not own, why `execute_api` beats curl, where
  onboarding is documented, and where memories fit. The MCP `instructions`
  now open with the same one-sentence framing.
- **A ready-to-send request example on every operation.** `get_api`
  returns `request_example` at every detail level and `GET
  /v1/operations/{id}/example` serves the same thing to the UI, resolved
  once in Go (`internal/example.Resolve`): a verified saved example, else a
  hand-written one, else the contract's own `example:`, else a payload
  synthesized from the request schema -- labelled with which, because
  "really was sent" and "placeholders from the schema" deserve different
  trust. The UI's "Try it" form now opens prefilled and says where the
  payload came from, instead of opening with an empty textarea beside a
  schema panel; its "Whole shape" button asks the daemon for every field
  the schema declares.
- **Documentation and example coverage, measured and linted.** A contract
  that lints clean can still be unusable, so `add_service`,
  `sync_service`, `get_service` and `sapien service add`/`sync`/`list` now
  report how many operations the narrative docs actually reach and how many
  of those taking a body show a payload. Four new warnings name the gaps --
  `NO_NARRATIVE_DOCS`, `UNDOCUMENTED_OPERATION`, `MISSING_REQUEST_EXAMPLE`,
  `NO_CONCEPTS` -- acceptable in `service.yaml`'s `accepted_warnings` with a
  reason like any other lint finding. Contract-derived docs (tag and `info`
  descriptions) do not count towards coverage, deprecated operations are
  left out of the totals, and a service with no docs at all gets one
  warning rather than one per operation.
- **Onboarding now interviews the service's owner.** `get_dsl_reference("service")`
  is rewritten around the reader it actually has -- an agent in another
  repository -- and asks for the business logic the contract cannot carry:
  preconditions, invariants, side effects, idempotency, which errors are
  normal, and the traps. It tells the agent to ask the user one batched
  round of questions *after* drafting from the code (with a question bank
  at service, operation, and cross-service level), to write the answers
  into the docs and memories, and to record what is still unknown under
  `## Open questions` rather than guessing. Every request body gets an
  `example:` in the contract as part of onboarding.

### Changed
- **The daemon binds `127.0.0.1:7717` by default** instead of a random port.
  Every daemon replacement (an upgrade, an idle exit) used to strand every
  open tab at an address nothing was listening on, with no way for the page
  to find where the daemon went; with a stable origin, one `sapien ui`
  relaunch mints a session cookie the whole tab set shares, and a reload
  brings each of them back. `--port` still overrides it, and a port already
  in use falls back to a random one rather than refusing to start.

### Fixed
- **Every parsed contract used to be retained for the life of the daemon.**
  libopenapi memoizes node hashes, built schemas and JSON paths in eight
  process-global maps, keyed by pointers into the document being parsed, and
  nothing evicts them -- so each parse pinned its whole yaml tree and schema
  graph permanently. Parsing one 1.2 MB contract repeatedly and dropping
  every reference to it grew the live heap by 16.9 MB each time, still there
  after a forced GC; a daemon minutes old sat on a 1 GB heap of which 54%
  was retained libopenapi objects that nothing in Sapien could reach. The
  library exposes `ClearAllCaches` for precisely this ("call this between
  document lifecycles in long-running processes"), and `openapi.Ingest`
  returns nothing but domain types, so it now releases them once the last
  concurrent ingest is done. On a daemon indexing three services across two
  workspaces and then taking fourteen contract edits: 385 MB after the
  startup index and 1562 MB after the edits, down to 19 MB and 21 MB.
  physical footprint after three hours, still climbing at ~4 GB/min, with
  only ~69 MB resident: allocation churn, not a leak. The file watcher
  answers any change under a service package -- `api/docs/*.md` included --
  with a full `SyncOne`, which re-parsed the service's entire OpenAPI
  contract, and doc indexing recompiled one whole-word regexp per operation
  id and schema name *for every section of every file*. In a reproduction
  (two workspaces, three 1.2 MB contracts, a doc written every 1.5s) those
  two accounted for 61% and 30% of everything the process allocated. Now the
  contract ingest is content-addressed, so a change that leaves the contract
  bytes alone reuses the previous parse, and the ref matchers are compiled
  once per package instead of once per section. Same load: 275 MB/s of
  allocation and a footprint climbing past 9 GB in 80 seconds became 12 MB/s
  and a flat one, with CPU down from 81% of a core to 19%. A contract edit
  still changes the content hash and is re-parsed on the very next pass, so
  the watcher is exactly as prompt as it was.
- **A local change no longer fetches, or reverts, a git-sourced service.**
  Every watch-triggered resync ran `gitsrc.Manager.Sync` -- `git fetch
  --prune` followed by `git reset --hard origin/<ref>` -- so editing a file
  inside a managed clone put a network call on the save and then discarded
  the edit, if it was to a tracked file. An agent writing documentation into
  a git-sourced package was fighting the daemon for its own work. The
  staleness check on engine open did the same, which meant every `sapien
  serve` startup fetched and hard-reset every clone; `staleness.go`'s own
  doc comment had promised the opposite since it was written. Whether a sync
  reaches the network is now the caller's decision, named at the call site
  (`gitFetch` / `useCheckoutOnDisk`): the watcher and the staleness check
  build from the checkout on disk, while `Services().Sync`, `Reindex`,
  `service add` and the ten-minute timer fetch as before.
- **The watcher can no longer be starved by a file that is never finished.**
  The debounce is a trailing one -- every event restarts it -- so a writer
  that never paused for a full 200ms window (a git checkout, a build step
  regenerating a contract) deferred indexing indefinitely. A flush is now
  capped at 25 debounce windows (5s by default) from the oldest pending
  change, so a continuous stream still gets indexed while it runs.
- **A missing build asset is a 404, not the app shell.** The SPA history
  fallback answered any unknown path under `/ui/` with `index.html`,
  including requests for the content-hashed chunks an upgraded daemon no
  longer has -- so an open tab navigating to a not-yet-loaded route got
  `text/html` for a dynamic `import()` and failed with a MIME type error.
  Paths under `assets/` are now excluded from the fallback; client-side
  routes still get the shell.
- **`soft: true` is documented in the flow DSL reference.** Soft assertions
  shipped in the runner, the JSON schema and `docs/flows.md`, but not in
  the reference agents read over MCP, so they could not find the feature
  and rediscovered it from failed runs. The reference now covers soft
  assertions and the `expr:` object form, and a test walks every YAML key
  the parser accepts and fails if the reference does not mention it, so a
  future DSL addition cannot ship invisible.

## [1.1.0] - 2026-09-06

### Added
- **Multiple workspaces, switchable from the CLI, the UI, and MCP.** One
  `sapien serve` now holds many workspaces instead of one daemon per
  workspace: each request selects one with an `X-Sapien-Workspace` header
  (or `?workspace=` for WebSockets) and the daemon opens it lazily, taking
  that workspace's own `daemon.lock` as it does. Every per-workspace
  on-disk invariant is unchanged — its own `.sapien` state directory,
  database, and lock — so one process still indexes one workspace, and two
  daemons can no longer reindex the same directory.
  - `sapien workspace list | current | use | add | forget` manage the
    registry, kept as `workspaces:` in the user config beside
    `default_workspace`. `sapien init` registers the workspace it creates.
  - `GET /v1/workspaces` lists what a daemon can serve; `POST /v1/workspaces`
    registers and opens another one.
  - The UI has a workspace picker in the nav. Because one daemon serves them
    all, switching stays on one origin and one session cookie — two daemons
    on `127.0.0.1` would have overwritten each other's, since cookies are
    not isolated by port.
  - MCP gains `list_workspaces` and `switch_workspace`; switching rebinds
    the session, leaving every other tool's schema unchanged. Both are
    registered only when there is somewhere to switch to.

### Fixed
- A workspace reached by two spellings of its path is one workspace again.
  On a case-insensitive filesystem `~/Desktop/ws` and `~/desktop/ws` are
  the same directory, and keying open workspaces by the path string let one
  daemon open it twice -- two engines, two database handles, two watchers
  on one directory, which is the double-indexing hazard the workspace lock
  exists to prevent, since both "holders" share a pid and each takes the
  lock from the other. It also showed a phantom extra workspace in the UI
  picker, `workspace list`, and `list_workspaces`. Identity is now decided
  by `os.SameFile` (device and inode) in the workspace manager and the
  registry, which is right on a case-insensitive filesystem without merging
  genuinely distinct paths on a case-sensitive one. A registered directory
  that no longer exists is still compared by string, so it stays listed
  with its own error rather than silently merging with another entry.
  The comparison lives once, as `workspace.SameDir`, and is used by the
  workspace manager, the registry, and `sapien workspace list` -- which
  had the same bug in its own membership check, so running it from a cwd
  that spelled the path differently listed the workspace you were standing
  in twice and put the current marker on the wrong row.
- `sapien service add <git-url>` no longer fails permanently when the
  managed clone predates the service's `api/` package. Registering a repo
  moments before the package was pushed left a clone frozen at the older
  commit, and because the name-derivation pass resolves a git source with
  `Ensure` (which never fetches), every retry re-read the same tree and
  failed with the same `no API package found under <cache path>`. That
  pass now fetches, so `service add <url>` and `service add <url> --name x`
  behave the same on a stale clone instead of only the named form working.
- When a git-sourced package genuinely is not found, the error now
  describes the clone it read: url, ref, commit, and whether that view of
  the remote is current. A clone that has not been fetched says so and
  points at `sapien service sync`; one that was just updated says the
  commit really does not carry the package. Previously the message named
  only a path under `~/.sapien/repos`, which reads as a configuration
  mistake in the one case where it is not one.

## [1.0.1] - 2026-09-06

### Changed
- Flow detail page (`/ui/flows/{id}`) is reordered around what it is used
  for: Run and live run progress at the top, then the steps, recent runs,
  and last the source. The agent-written description is clamped to two
  lines behind "Show more" and the flow's YAML is collapsed behind a
  disclosure that names its line count, so neither stands between the
  reader and the Run button or the step list. Running no longer navigates
  away: the run's status, step-by-step progress (live off the event
  stream), assertion counts, and errors appear in place, each step's card
  carries its status while the run is going, and "Open run details" links
  to the run's own page.
- Module path and distribution names moved from `github.com/growsimplee/sapien`
  to `github.com/gs-sinha/sapien` after the repository transfer: Go import
  path, `go install` target, installer and README URLs, the container image
  (`ghcr.io/gs-sinha/sapien`), the Homebrew tap (`gs-sinha/tap`), and the npm
  package (`@gs-sinha/sapien`). `go install ...@latest` resolves to this path
  from the next tagged release; v1.0.0 archives are unaffected.

## [1.0.0] - 2026-09-06

Phases 0–6 are engine-only and, together, produce a releasable product
for agent-driven use (PLAN.md §34). Status per phase reflects this
repository as documented; see [`docs/BUILD-LOG.md`](docs/BUILD-LOG.md)
for the detailed, running build record.

### Phase 0 — Foundations
- Repo skeleton, JSON Schemas in `spec/` (workspace, service, flow, memory, environment)
- `fixtures/logistics`: three-service sample (order/allocation/rider) with in-memory mock servers
- CI (`go test`/`vet`/`fmt`) and a benchmark harness

### Phase 1 — Catalog
- `sapien service add` (local sources), OpenAPI 3.x ingest (`libopenapi`) into a normalized model
- Documentation ingest by section, with reference extraction to operations/schemas/concepts
- SQLite catalog with FTS5 search over operations and docs
- `sapien describe`, `sapien docs`, filesystem watch, `sapien reindex`

### Phase 2 — Execution
- Environments and OS-keychain secrets (with `SAPIEN_SECRET_*` env-var fallback for CI)
- `sapien call`, the flow DSL, CEL expressions, a validator with suggestions
- Sequential flow runner with polling (`until`/`poll`), structured assertions, run records, redaction
- JUnit report output for CI

### Phase 3 — Memory
- Memory files (Markdown + front matter) and index, subject/reference model
- `sapien memory add|search|list`, structural + lexical ranking
- Agent context builder, `sapien context`

### Phase 4 — Daemon + MCP
- `sapien serve`, the local HTTP API, `/events` WebSocket, `Engine` local/remote split
- MCP server (`sapien mcp`) with the full PLAN.md §23 tool table, permissions, doc/schema tools, DSL reference, server instructions
- Host config helper (`sapien mcp config`)

### Phase 5 — Git sources + promotion
- Managed git clones for services, sync timer
- `get_promotion_target` / `sapien memory promote`

### Phase 6 — Engine release
- Optional semantic search
- GoReleaser packaging, Homebrew tap, `curl | sh` installer, npm/`npx` wrapper, CI Docker image
- Benchmarks in CI, user documentation (this package)

### Onboarding journey (after Phase 6)
- `get_dsl_reference("service")` / `sapien://reference/service` / `sapien flow reference service`: the api/ package layout and onboarding checklist an agent follows to write Sapien-style docs for a service
- `add_service` MCP tool behind a new `write_services` permission class, so an agent can register the service it just documented from inside that service's repo
- `sapien mcp config --scope user|local|project` (default `user`, so the Claude Code entry works in every repo), a `cursor` client, and idempotent `--write` for claude-code
- `sapien service add` resolves relative paths against the current directory; paths inside the workspace are stored relative to it
- `add_service` returns a "## Sapien" section for the repo's `CLAUDE.md`/`AGENTS.md` so future code changes keep `api/` current; the reference and server instructions say to paste it
- `sapien mcp` replaces a daemon left over from a previous build instead of failing with a restart hint
- README "Make it operational", `docs/onboarding.md`, and refreshed getting-started/MCP docs

### Feedback round 1 (after the first real onboarding)
- Warning acceptance: `service.yaml: accepted_warnings` with a required reason; `STALE_ACCEPTANCE`; accepted counts in CLI and MCP; guidance never to silence a warning by misdescribing the wire
- Memory scope rule at the call site (`create_memory`, `memory add`), write-time hints (storage, similar memories, promotion), `memory rescope` / `rescope_memory`
- `sapien env scaffold` and `sapien env probe`; env errors name the services declaring a missing environment; service summaries list declared-but-undefined environments
- MCP permissions hot-reload from `.sapien/mcp.yaml`; the stdio bridge reconnects when the daemon restarts or is recycled
- `add_service` re-syncs an already-registered service; new `sync_service` tool
- Workspace resolution: `$SAPIEN_WORKSPACE` and `default_workspace` (recorded by `mcp config --write`); `sapien init` tips
- Run-failure hints ("Might explain it") from the contract, docs, and memories in `call`, `flow run`, `run show`, `execute_api`, `run_flow`, `get_run`

### Daemon — one per workspace
- Workspace lock (`.sapien/daemon.lock`) held for the daemon's lifetime; orphaned daemons from a replacement are detected and stopped; reindex keeps a flow's row when its file fails to parse; spawned daemons log to `.sapien/daemon.log`

### Flows — from the 58-step field report
- Soft assertions (`soft: true`): warnings, not failures; flips since the previous run are reported
- `run_flow` returns a summary by default (`detail: summary|failed|full`); `validate_flow(path)`; `get_flow(detail: outline)`
- Agent pane is terminal-only

### Flows — cheap iteration (from the 41-step field report)
- `setup:` and `teardown:` step lists; teardown always runs
- Resume and partial runs: `sapien run resume <run-id>`, `flow run --resume/--from/--until`, MCP `run_flow(resume_from, from_step, until_step)`; reused steps are marked and warn when their definition changed
- Round-trippable authoring: `expr:` accepted in assertions, `get_flow` returns YAML, `create_flow`/`update_flow` return summaries, `path` is relative to `flows/` and fenced
- `patch_flow` / `sapien flow patch`: step-level edits preserving comments; `update_flow(path)` re-reads a file edited on disk

### Search — find operations by URL
- Paste a full URL, a path with a base prefix, a path without a leading slash, or `METHOD <url>`: ranked by exact, templated, suffix, parent, and prefix matches, with lexical fallback; `sapien describe` accepts URLs; the UI never treats a path as an intent

### Search — knowledge columns, feedback loop, docs-mediated fusion
- Doc and memory text indexed on operations (`matched_on`: docs, memories); usage feedback boost (`feedback`)
- Docs-mediated second ranker blended 0.7/0.3 by default: Recall@1 0.72 -> 0.80 on the real-workspace harness; `SAPIEN_SEARCH_DOC_FUSION=off` disables
- `experiments/search-eval`: 104-query ground truth, static-embedding study (not adopted), sweep and compare scripts

### Phase 7b — Agent pane
- `/ui/agent` runs `claude`, `codex`, or your shell in an xterm pane over a PTY WebSocket, restricted to the workspace or registered repos; `?prompt=` hand-off from runs
- Flow steps editable in place (input, body, headers) with run-with-edits, save-to-flow, save-as-example; doc path encoding fix; null-safe API client

### Phase 7a — Live inspector UI
- `sapien ui` opens a browser UI served by the daemon at `/ui/` (embedded SPA, cookie session, live events); flows, runs with every step's payload and hints, edit-and-rerun, save-as-example, services with warnings and environments, operations with intent search, operation detail with docs/examples/memories, try-it, examples, memories, events
- Bundle budget enforced in the build (measured 64 KB initial, 118 KB total gzipped); no Electron, no polling
- `GET /v1/events/recent`, `GET /v1/runs/{id}/hints`; flow YAML `source` over the API

### Examples (saved, verified payloads; PLAN §34b)
- `internal/example` store: `<workspace>/examples/*.example.yaml` or `<repo>/api/examples/`, indexed in SQLite; `engine.ExampleAPI` on Local, Remote, and the daemon (`/v1/examples`)
- `verified` recorded only when saved from a real run (`sapien call --save-example`, `sapien run save-example`, MCP `create_example(run_id)`)
- Flow steps reuse one with `example: <id>`; validator suggests ids; explicit step fields override
- CLI `sapien example list|show|add|rm|rescope`, `sapien call --example <id>`
- MCP `list_examples`, `get_example`, `create_example`, `rescope_example`, `delete_example` behind `write_examples`; `execute_api` accepts `example`; `get_api` lists example ids; `get_context` carries an examples tier
- Replay fixes: a `headers:` entry satisfies a declared header param (so `-H`, `-p`, and saved headers are interchangeable); `FromRun` stores header params in `input` under their declared names and drops transport noise; human-mode errors print diagnostics; example files write integral numbers as integers
