// Module-level terminal + WebSocket store for the agent pane (PLAN §34c
// Phase 7b). The xterm.js Terminal, its fit addon, its (React-external)
// DOM node, and the WebSocket are plain module-level variables -- the
// same pattern state/events.ts uses for its `socket` -- rather than React
// state or refs owned by AgentPage, specifically so navigating to another
// route inside the SPA and back does not end the running agent process:
// AgentPage calls attachTerminal(el) on mount, which reparents the
// persistent terminal node into whatever wrapper element it just
// rendered, and does nothing special on unmount. The node simply becomes
// parentless (but stays alive, still receiving output over the still-open
// WebSocket) until some AgentPage instance reattaches it.
//
// Only ever imported from src/pages/AgentPage.tsx and
// src/components/agent/*, so xterm and xterm-addon-fit (~75 KB gz
// combined) load only in that lazy route's chunk.
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import { create } from 'zustand';
import 'xterm/css/xterm.css';
import { terminalWebSocketURL } from '../api/agentExtra';

export type SessionPhase = 'idle' | 'connecting' | 'running' | 'exited' | 'error';

interface AgentTerminalState {
  phase: SessionPhase;
  command: string;
  dir: string;
  exitCode: number | null;
  errorMessage: string | null;
}

// Raw, non-reactive singletons. Deliberately not part of the zustand
// state below (writing a Terminal instance into store state on every
// keystroke would be both pointless -- nothing renders it directly -- and
// a way to accidentally trigger re-renders keyed off object identity).
let ws: WebSocket | null = null;
let term: Terminal | null = null;
let fitAddon: FitAddon | null = null;
let container: HTMLDivElement | null = null;

// A hand-off prompt (PLAN §34c Phase 7b item 5) waiting to be pasted into
// the PTY once the agent's own output settles. There is no protocol-level
// "ready" signal from the agent, so "settled" is approximated as "no
// output for PROMPT_SETTLE_MS after connecting" -- best-effort, and
// documented as such in ui/README.md.
const PROMPT_SETTLE_MS = 600;
let pendingPrompt: string | null = null;
let promptTimer: ReturnType<typeof setTimeout> | null = null;

export const useAgentTerminal = create<AgentTerminalState>(() => ({
  phase: 'idle',
  command: '',
  dir: '',
  exitCode: null,
  errorMessage: null,
}));

// ensureTerminal creates the singleton Terminal/FitAddon/container the
// first time it's needed (lazily, so nothing is instantiated until
// AgentPage actually mounts) and returns them on every later call.
function ensureTerminal(): { term: Terminal; fitAddon: FitAddon; container: HTMLDivElement } {
  if (!term) {
    term = new Terminal({
      convertEol: true,
      fontSize: 13,
      cursorBlink: true,
      scrollback: 5000,
    });
    fitAddon = new FitAddon();
    term.loadAddon(fitAddon);

    container = document.createElement('div');
    container.style.height = '100%';
    container.style.width = '100%';
    term.open(container);

    // The browser's only path for the user's keystrokes to reach the
    // process: raw bytes over the WebSocket as a binary frame, never
    // interpolated into anything server-side.
    term.onData((data) => {
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });
  }
  return { term, fitAddon: fitAddon!, container: container! };
}

// attachTerminal reparents the persistent terminal DOM node into el (an
// AgentPage-owned wrapper) and fits it to that element's size. Safe to
// call on every AgentPage mount, including before any session has
// started (it shows an empty, ready terminal).
export function attachTerminal(el: HTMLElement): void {
  const { container: c, fitAddon: fa } = ensureTerminal();
  if (c.parentElement !== el) {
    el.appendChild(c);
  }
  fa.fit();
}

// fitTerminal re-fits the terminal to its current container size (call
// from a ResizeObserver) and reports the new size so the caller can push
// a resize control frame.
export function fitTerminal(): { cols: number; rows: number } | null {
  if (!term || !fitAddon) return null;
  fitAddon.fit();
  return { cols: term.cols, rows: term.rows };
}

function clearPromptTimer() {
  if (promptTimer) {
    clearTimeout(promptTimer);
    promptTimer = null;
  }
}

// scheduleQuietPrompt (re)starts the "output has settled" countdown; each
// incoming chunk resets it, so the prompt is pasted only once the agent
// has stopped drawing its startup screen.
function scheduleQuietPrompt() {
  if (!pendingPrompt) return;
  clearPromptTimer();
  promptTimer = setTimeout(() => {
    const text = pendingPrompt;
    pendingPrompt = null;
    if (text && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(new TextEncoder().encode(text)); // no trailing "\n": never press Enter for the user
    }
  }, PROMPT_SETTLE_MS);
}

export interface StartOptions {
  command: string;
  dir: string;
  /** Hand-off text (PLAN §34c Phase 7b item 5) to paste once output settles. */
  prompt?: string;
}

// startSession ends any previous session, clears the terminal, and opens
// a new PTY over a fresh WebSocket at GET /v1/terminal.
export function startSession(opts: StartOptions): void {
  endSession();

  const { term: t, fitAddon: fa } = ensureTerminal();
  t.reset();
  pendingPrompt = opts.prompt?.trim() ? opts.prompt : null;

  useAgentTerminal.setState({
    phase: 'connecting',
    command: opts.command,
    dir: opts.dir,
    exitCode: null,
    errorMessage: null,
  });

  fa.fit();
  const cols = t.cols || 80;
  const rows = t.rows || 24;

  let socket: WebSocket;
  try {
    socket = new WebSocket(terminalWebSocketURL({ command: opts.command, dir: opts.dir, cols, rows }));
  } catch (err) {
    useAgentTerminal.setState({
      phase: 'error',
      errorMessage: err instanceof Error ? err.message : 'failed to open a terminal connection',
    });
    return;
  }
  socket.binaryType = 'arraybuffer';
  ws = socket;

  socket.onopen = () => {
    if (ws === socket) useAgentTerminal.setState({ phase: 'running' });
  };

  socket.onmessage = (ev) => {
    if (ws !== socket) return;
    if (ev.data instanceof ArrayBuffer) {
      term?.write(new Uint8Array(ev.data));
      scheduleQuietPrompt();
      return;
    }
    let msg: { type?: string; code?: number };
    try {
      msg = JSON.parse(ev.data as string);
    } catch {
      return; // not JSON; ignore rather than treat as a protocol error
    }
    if (msg.type === 'exit') {
      clearPromptTimer();
      pendingPrompt = null;
      useAgentTerminal.setState({ phase: 'exited', exitCode: typeof msg.code === 'number' ? msg.code : null });
    }
  };

  socket.onclose = () => {
    if (ws !== socket) return;
    ws = null;
    // A close with no preceding "exit" frame (network drop, server
    // restart) still ends the session from the UI's point of view.
    if (useAgentTerminal.getState().phase !== 'exited') {
      useAgentTerminal.setState({ phase: 'exited', exitCode: null });
    }
  };

  socket.onerror = () => {
    // onclose always follows onerror for a browser WebSocket; state is
    // updated there once the close actually arrives.
  };
}

// sendResize tells the running PTY about a new size (a text control
// frame -- see api/agentExtra.ts's protocol doc comment).
export function sendResize(cols: number, rows: number): void {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'resize', cols, rows }));
  }
}

// endSession closes the socket. The server's protocol kills the PTY
// process when its WebSocket closes, so this is the one call that
// actually stops the agent; it does not dispose the Terminal itself, so
// its scrollback stays visible (e.g. showing the exit notice) until a new
// session starts.
export function endSession(): void {
  clearPromptTimer();
  pendingPrompt = null;
  if (ws) {
    const s = ws;
    ws = null;
    s.close();
  }
}

export function hasActiveSession(): boolean {
  return ws !== null;
}
