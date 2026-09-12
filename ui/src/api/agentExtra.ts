// Client-side helpers for the agent pane (PLAN §34c Phase 7b: "an agent
// pane that spawns the user's own claude or codex in an xterm.js terminal
// ... with 'ask the agent about this run' hand-offs"). Kept out of the
// shared api/client.ts per the wave ownership split -- that file is owned
// by another concurrent pass.
import { currentWorkspace } from '../state/workspace';
import { ApiClientError } from './client';

// ---- GET /v1/terminal/targets ----

export interface TerminalDir {
  label: string;
  path: string;
}

export interface TerminalTargets {
  commands: string[];
  dirs: TerminalDir[];
}

// getTerminalTargets fetches the commands and directories the terminal
// endpoint will accept, for the agent pane's picker. Mirrors
// api/client.ts's request() shape (credentials, error normalization)
// without importing its unexported internals.
export async function getTerminalTargets(): Promise<TerminalTargets> {
  let res: Response;
  // The same workspace selector api/client.ts's request() sends. It is not
  // optional here just because this endpoint is "only a picker": one daemon
  // serves many workspaces, and the directories it offers are the selected
  // workspace's -- the very list GET /v1/terminal then validates a start
  // against. Omitting the header offered the *primary* workspace's
  // directories however the picker was set, and started the pane there.
  const ws = currentWorkspace();
  try {
    res = await fetch('/v1/terminal/targets', {
      credentials: 'include',
      headers: {
        Accept: 'application/json',
        ...(ws ? { 'X-Sapien-Workspace': ws } : {}),
      },
    });
  } catch (err) {
    throw new ApiClientError('E_NETWORK', err instanceof Error ? err.message : 'network request failed');
  }

  const text = await res.text();
  let body: unknown;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = undefined;
    }
  }

  if (!res.ok) {
    if (body && typeof body === 'object' && 'code' in body) {
      const b = body as { code: string; message?: string };
      throw new ApiClientError(b.code, b.message || res.statusText);
    }
    throw new ApiClientError('E_HTTP_' + res.status, (text || res.statusText).slice(0, 500));
  }

  return body as TerminalTargets;
}

// ---- GET /v1/terminal WebSocket ----

export interface TerminalWebSocketParams {
  command: string;
  dir: string;
  cols: number;
  rows: number;
  /**
   * The selected workspace's directory; "" means the daemon's primary one.
   * Required rather than read from state/workspace.ts inside the builder
   * (which is what state/events.ts's wsURL does for /v1/events) so that the
   * URL and the workspace the caller records for the pane are the same
   * value, read at the same instant: a pane that is running against one
   * workspace while the store says another is the bug this parameter fixes.
   */
  workspace: string;
}

// terminalWebSocketURL builds the
// GET /v1/terminal?command=&dir=&cols=&rows=&workspace= URL that upgrades to
// the PTY WebSocket (internal/server/handlers_terminal.go).
// Protocol (binary frames = PTY stdin/stdout, a JSON {"type":"resize",...}
// text frame from the client, a final {"type":"exit","code":n} text frame
// from the server) is implemented in state/agentTerminal.ts. Same ws(s)://
// + same-host pattern as state/events.ts's wsURL, so the session cookie
// set by GET /ui/session authenticates the handshake exactly as it does
// for /v1/events.
export function terminalWebSocketURL(params: TerminalWebSocketParams): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const qs = new URLSearchParams({
    command: params.command,
    dir: params.dir,
    cols: String(params.cols),
    rows: String(params.rows),
  });
  // A browser cannot set headers on a WebSocket handshake, so the workspace
  // selector rides in the query string here, exactly as state/events.ts does
  // for /v1/events; the daemon accepts either (internal/server/
  // workspacectx.go). This is also what gives the pane its SAPIEN_WORKSPACE:
  // handleTerminal sets it from the engine this parameter resolved.
  if (params.workspace) qs.set('workspace', params.workspace);
  return `${proto}//${window.location.host}/v1/terminal?${qs.toString()}`;
}

// ---- hand-offs ----

// agentHandoffURL builds a /ui/agent?prompt=... link (PLAN §34c Phase 7b
// item 5): "ask the agent about this run". RunDetailPage is owned by a
// concurrent wave-2 pass, so no button calls this yet -- wire one up with,
// e.g.:
//
//   import { agentHandoffURL } from '../api/agentExtra';
//   <a href={agentHandoffURL({ runId: run.id, step: step.step_id })}>Ask the agent</a>
//
// AgentPage reads `prompt` off the URL, starts a session (auto-picking a
// command and the workspace directory if none is already running), and
// once the agent's own output settles, pastes the sentence into the PTY
// without pressing Enter for the user -- they read it and decide whether
// to send it.
export function agentHandoffURL(opts: { runId: string; step?: string }): string {
  const sentence = opts.step
    ? `Look at Sapien run ${opts.runId} (use get_run) and explain why step ${opts.step} failed.`
    : `Look at Sapien run ${opts.runId} (use get_run) and explain what happened.`;
  return `/ui/agent?prompt=${encodeURIComponent(sentence)}`;
}
