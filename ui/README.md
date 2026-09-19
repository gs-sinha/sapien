# Sapien UI

The web UI for Sapien (PLAN.md "34c. Phase 7a: live inspector UI"): a
single-page app served by the daemon at `/ui/`, built with Vite 4 + React 18
+ TypeScript + Tailwind 3, state in zustand, routing with react-router-dom
6. No LLM in it; it is the glass beside your agent's chat, showing what
happens through the shared daemon and letting you inspect, fix, rerun, and
save payloads.

## Requirements

This project targets Node 16.13 / npm 8 (the dev machine's actual
versions). Every dependency was chosen to run on that: Vite 4.5.x (not 5),
vitest 0.34.x (not 1+), `@vitejs/plugin-react` 4.x, React 18, TypeScript 5,
react-router-dom 6, Tailwind 3.4, zustand 4. Nothing here needs Node 18 or a
CDN; the whole app bundles.

## Scripts

- `npm run dev` -- Vite dev server with hot reload, proxying `/v1` and
  `/ui/session` to a running daemon (see "Dev workflow" below).
- `npm run build` -- type-checks (`tsc`), builds to `../internal/ui/dist`
  (so the Go daemon can `go:embed` it, no Node needed to build the Go
  binary), then runs the bundle-size gate (`npm run size`). Fails the build
  if either budget below is exceeded.
- `npm run size` -- prints gzipped size per chunk and checks the budget
  against an existing build, without rebuilding.
- `npm test` -- runs the vitest suite once (`npm run test:watch` to watch).
- `npm run preview` -- serves the production build locally for a final look.

## Dev workflow

The daemon (`sapien serve`, or the one `sapien mcp` starts) writes
`<workspace>/.sapien/daemon.json` with its actual port and bearer token,
e.g.:

```json
{ "port": 54213, "token": "sk_live_...", "pid": 12345 }
```

Point the dev server at it:

```sh
export SAPIEN_DAEMON_URL=http://127.0.0.1:54213   # the "port" field, as a URL
export SAPIEN_TOKEN=sk_live_...                     # the "token" field
npm run dev
```

The Vite proxy (`vite.config.ts`) forwards `/v1/*` and `/ui/session` to
`SAPIEN_DAEMON_URL` and injects `Authorization: Bearer $SAPIEN_TOKEN` on
every proxied request, so the app works in dev without ever going through
the cookie session flow (`GET /ui/session?token=...`) that the built app
uses when opened via `sapien ui`. If `SAPIEN_TOKEN` is unset the proxy
still forwards requests, just without the header -- fine only if the
daemon has auth disabled.

## Architecture

- `src/api/types.ts` -- TypeScript mirrors of `internal/domain/*.go`'s JSON
  shapes, plus the server's wire request/response types
  (`internal/server/wire.go`, `internal/engine/engine.go`). Hand-maintained;
  keep field names exactly in sync (snake_case, as emitted) when the Go
  side changes.
- `src/api/client.ts` -- typed `fetch` wrapper, `credentials: 'include'` so
  the `sapien_session` cookie authenticates every request. Errors are
  normalized to `ApiClientError` (`code`, `message`, `details`, `source`,
  `hint`), mirroring `internal/errs.Error`'s JSON body. A request that
  never reaches the daemon (network failure) gets `code: "E_NETWORK"`.
  Exports grouped by resource: `services`, `operations`, `schemas`, `docs`,
  `callOperation`, `flows`, `runs`, `memories`, `examples`, `buildContext`,
  `environments`, `secrets`, `getRecentEvents`, plus `getHealth`/`getWorkspace`.
- `src/state/events.ts` -- the live event stream. `useEvents()` (zustand)
  exposes `status` (`connecting`/`open`/`closed`), the capped `events`
  array, and `unreadCount`; `subscribe(type, fn)` lets a page react to one
  event type without re-rendering on every event (a run detail page
  filters `run.step`/`run.finished` by `ids.run_id`, the flows list
  refetches on `flow.changed`, services on `catalog.changed`).
- `src/state/theme.ts` -- dark/light, follows `prefers-color-scheme` until
  the user toggles, then persists the explicit choice in `localStorage`.
- `src/state/toast.ts` -- toast queue (`pushToast(kind, message)`).
- `src/components/*` -- `JsonView`, `YamlView`, `StatusPill`, `Table`,
  `EmptyState`, `Timestamp`, `KeyValue`, `VirtualList`, `Nav`, `SearchBox`,
  `StatusBar`, `Toasts`, `ErrorBoundary`, `NotFound`.
- `src/pages/*` -- one file per route (see below), each a default export so
  `React.lazy` can code-split it.
- `src/lib/useAsync.ts` -- tiny fetch-on-mount hook (no cross-page cache).
- `src/lib/yaml.ts` -- a minimal YAML dumper used only for read-only
  display (see "Known gap" below).

## Routes

`/ui/flows`, `/ui/flows/:id`, `/ui/runs`, `/ui/runs/:id`, `/ui/services`,
`/ui/services/:name`, `/ui/operations` (plain search, or `?intent=` to
build a context bundle), `/ui/operations/:id`, `/ui/examples`,
`/ui/examples/:id`, `/ui/memories`, `/ui/events`, `/ui/agent` (optionally
`?prompt=<text>`; see "Agent pane" below). These are wave-1 stub pages
that already show real data from the daemon; a follow-up wave adds
editing, running, and saving from these pages.

## Agent pane

`/ui/agent` (PLAN §34c Phase 7b) runs the user's own coding agent --
`claude`, `codex`, `opencode`, or a plain shell -- in a real PTY on the
daemon's machine (`GET /v1/terminal`, `internal/terminal` + `internal/server/
handlers_terminal.go`), rendered in the browser with xterm.js. It's a
split view, not a terminal-only page:

- **Left: `src/components/agent/MiniRunPanel.tsx`.** A run-id box that
  fetches `runs.get(id)` and renders its steps (status, request,
  response, error) through the same `JsonView`/`StatusPill`/`Timestamp`
  components the Runs pages use. This exists so a developer can look at a
  run's payloads while talking to the agent about them *without ever
  needing to leave `/ui/agent`* -- which sidesteps the harder problem of
  surviving SPA navigation for the common case of "I want to show the
  agent something from a run."
- **Right: `src/components/agent/Picker.tsx`** (command/directory picker
  and Start/Restart, backed by `GET /v1/terminal/targets` via
  `src/api/agentExtra.ts`) **over `src/components/agent/TerminalView.tsx`**
  (the xterm.js viewport, fit to its container via `xterm-addon-fit` and a
  `ResizeObserver`, with an exit/error notice and Restart button once the
  session ends).

**Surviving navigation.** The xterm.js `Terminal`, its fit addon, its
(React-external) DOM node, and the session's `WebSocket` are a
module-level singleton in `src/state/agentTerminal.ts` -- the same
pattern `src/state/events.ts` uses for its own socket -- rather than
React state owned by `AgentPage`. `TerminalView` calls `attachTerminal(el)`
on mount, which reparents the persistent terminal node into whatever
wrapper it just rendered, and does nothing special on unmount: the node
simply goes parentless (but stays alive, still receiving PTY output over
the still-open socket) until some `AgentPage` instance reattaches it. So
navigating to another route inside the SPA and back **does not** end the
running agent process; only an explicit Restart, the process exiting on
its own, or closing the tab does. Caveat: this has not been confirmed
against a live daemon in a real browser (start a session, navigate to
`/ui/runs`, navigate back, expect the same scrollback and live
connection) -- only reasoned through and covered by the mocked-xterm unit
tests below, which exercise the reattach code path but not real detach/
reattach DOM or xterm.js rendering behavior, since jsdom can't run real
xterm.js. Worth a manual pass before relying on it.

**WebSocket protocol** (`GET
/v1/terminal?command=&dir=&cols=&rows=&workspace=`, implemented in
`src/state/agentTerminal.ts`): binary frames each
direction are raw PTY bytes (stdin from the client, stdout+stderr from
the server -- the browser's only path for a keystroke to reach the
process); a `{"type":"resize","cols":n,"rows":n}` text frame from the
client on every fit change; a final `{"type":"exit","code":n}` text frame
from the server before it closes the socket. No user text is ever
interpolated into a command -- the server only ever spawns `claude`,
`codex`, `opencode`, or the user's `$SHELL`, resolved via `exec.LookPath`,
in a directory drawn from the workspace/registered services/home (see that
package's doc comment for the exact allowlist).

**Which workspace the pane belongs to.** One daemon serves many
workspaces, so both halves of this page have to say which one they mean,
and for a while neither did: `getTerminalTargets` hand-rolled its own
`fetch` instead of going through `src/api/client.ts`, and the socket URL
carried no selector at all -- so a pane opened while the picker showed a
second workspace still listed the *primary* workspace's directories and
started there, with `SAPIEN_WORKSPACE` naming the wrong one. The targets
request now sends `X-Sapien-Workspace` the way `client.ts` does, and the
socket appends `?workspace=` the way `src/state/events.ts` does (a
browser cannot set a header on a WebSocket handshake, which is why the
two routes take a query parameter). A PTY cannot follow a switch -- its
cwd and environment were fixed at spawn -- so `agentTerminal.ts`
subscribes to the workspace store and ends the session when it changes,
rather than leaving a live agent editing files in a workspace the UI is
no longer showing. A directory that does not belong to the selected
workspace is refused by the daemon with a 400 on the upgrade; since the
browser can see neither that status nor its body, the store treats a
close that never opened as a failed start and says so, instead of
reporting it as a session that ran and ended.

**Hand-offs.** `agentHandoffURL({ runId, step })` (`src/api/agentExtra.ts`)
builds a `/ui/agent?prompt=...` link with a sentence like "Look at Sapien
run `<id>` (use get_run) and explain why step `<step>` failed." `AgentPage`
reads `?prompt=`, auto-starts a session (preferring `claude`, then
`codex`, over the workspace directory) if none is running, and pastes the
text into the PTY once the agent's own output settles for ~600ms -- a
best-effort proxy for "the agent's prompt is on screen," since the
protocol has no explicit ready signal -- **without pressing Enter**: the
user reads it and decides whether to send it. No page currently links to
`agentHandoffURL` (`RunDetailPage` is owned by a concurrent pass); wire a
button to it with:

```tsx
import { agentHandoffURL } from '../api/agentExtra';
<a href={agentHandoffURL({ runId: run.id, step: step.step_id })}>Ask the agent</a>
```

**Bundle size.** xterm + `xterm-addon-fit` (~75 KB gz combined) are only
ever imported from `AgentPage` and its components, so they land solely in
that lazy route's chunk (about 72 KB gz on top of the ~220 KB raw the
libraries add) and never touch the initial bundle. Confirmed with
`npm run build`.

**Tests** (`src/test/AgentPicker.test.tsx`, `AgentTerminalView.test.tsx`,
`agentExtra.test.ts`) mock `xterm`/`xterm-addon-fit` (jsdom has no
`HTMLCanvasElement.getContext('2d')` or `window.matchMedia`, both of
which `Terminal.open()` needs) and `WebSocket`/`ResizeObserver` (jsdom has
neither), the same style `src/test/events.test.ts` already uses for its
own `WebSocket` mock. They cover: targets rendering, Start opening a
WebSocket at the right URL for the picked command/directory, a resize
sending the JSON control frame, and an exit frame showing the Restart
control.

## Performance budget

The product exists because heavier API tools are slow; this budget is
non-negotiable and enforced in CI via `npm run build`:

1. **Bundle size**: initial route chunk (the app shell: everything
   `index.html` loads eagerly, i.e. NOT a lazily-loaded page) under 120 KB
   gzipped; the whole built app under 300 KB gzipped. Check any time with
   `npm run size` (also runs automatically at the end of `npm run build`
   and fails the build over budget). Every page under `src/pages` is a
   `React.lazy` route specifically so the first paint doesn't pay for
   pages you haven't opened yet.
2. **Dependencies**: no component library, no Monaco/CodeMirror. Runtime
   deps are exactly `react`, `react-dom`, `react-router-dom`, `zustand`.
   Tailwind runs with content-based purging so shipped CSS only contains
   classes actually used.
3. **Memory discipline**: the event store keeps at most 500 events, and
   each is reduced to `{type, time, summary, ids}` the instant it arrives
   -- never a request or response body (see `src/state/events.ts`'s
   `summarize`). No page keeps a cross-navigation cache: a run detail page
   fetches its run on mount and the data is garbage the moment you
   navigate away. There is no polling anywhere; only the `/v1/events`
   WebSocket pushes updates.
4. **Large data on screen**: `JsonView` only mounts a node's children once
   you expand it, starts collapsed past depth 2, and pages arrays over 200
   items 200 at a time ("show more"); a string over 256 KB is shown
   truncated with copy/download of the raw text instead of being rendered.
   Long lists (`/ui/runs`, `/ui/events`, `/ui/operations` search results)
   render through `src/components/VirtualList.tsx`, a ~60-line
   scroll-position-based windowed list (deliberately not `react-window`,
   to keep the dependency list above true).

Targets to check by hand:
- First paint under 200 ms on localhost (open devtools' Performance panel,
  reload against `npm run preview`).
- A 1,000-item list (e.g. `/ui/events` after it accumulates events, or
  point `/ui/runs` at a workspace with many runs) scrolls without jank --
  should stay locked to your monitor's refresh rate in the Performance
  panel while scrolling.
- A run detail page open on a 50-step run stays under 60 MB of JS heap:
  Chrome's Task Manager (`Shift+Esc`) shows per-tab memory; take a heap
  snapshot in devtools' Memory panel before/after navigating away from the
  run to confirm it drops back down (nothing else should be holding a
  reference to the run's steps once the page unmounts).

## Known gaps (for the next wave)

- **Flow YAML**: `domain.Flow.Source` (the file's exact original text) is
  tagged `json:"-"` and no HTTP route returns it, so `/ui/flows/:id`
  reconstructs a YAML-ish view from the parsed fields (`src/lib/yaml.ts`)
  rather than showing the committed file's bytes. Round-trips the data,
  not comments or formatting. If a future Go change exposes raw source,
  swap `dumpYaml(flow)` for that text.
- **Run "Might explain it" hints**: `internal/diagnose` (used by the CLI
  and MCP tools) is not wired to any `/v1` route, so the run detail page
  does not show diagnose hints yet. The same result is reconstructable
  client-side from existing endpoints (`operations.get` for the contract
  hint, `docs.list`/`memories.search` for token search) if that's preferred
  over adding a Go endpoint; PLAN 34c's "Might explain it" bullet is
  otherwise unimplemented in wave 1.
- **Add/sync-service forms, flow editing, running from the UI, saving
  examples from a run, secrets management UI** -- all explicitly wave 2
  per the brief ("wave 2 agents fill the pages"); the API client already
  has typed functions for all of these (`services.add`, `flows.run`,
  `runs.runSource`, `examples.fromRun`, `secrets.*`, etc.) so wave 2 should
  only need to build UI against them.
