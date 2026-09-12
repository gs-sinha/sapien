// TerminalView (ui/src/components/agent/TerminalView.tsx) tests: PLAN
// §34c Phase 7b item 6's "resize sends the JSON frame" and "exit frame
// shows Restart". xterm/xterm-addon-fit/WebSocket/ResizeObserver are all
// mocked -- see AgentPicker.test.tsx's header comment for why (jsdom
// can't run real xterm.js, and doesn't implement ResizeObserver at all).
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('xterm', () => {
  class FakeTerminal {
    cols = 80;
    rows = 24;
    loadAddon() {}
    open() {}
    onData() {
      return { dispose() {} };
    }
    write() {}
    reset() {}
    dispose() {}
  }
  return { Terminal: FakeTerminal };
});

vi.mock('xterm-addon-fit', () => {
  class FakeFitAddon {
    fit() {}
    proposeDimensions() {
      return undefined;
    }
    activate() {}
    dispose() {}
  }
  return { FitAddon: FakeFitAddon };
});

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  url: string;
  binaryType = '';
  readyState = MockWebSocket.CONNECTING;
  sent: unknown[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }
  send(data: unknown) {
    this.sent.push(data);
  }
  close() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.();
  }
}

class MockResizeObserver {
  static instances: MockResizeObserver[] = [];
  private cb: () => void;
  constructor(cb: () => void) {
    this.cb = cb;
    MockResizeObserver.instances.push(this);
  }
  observe() {}
  disconnect() {}
  trigger() {
    this.cb();
  }
}

beforeEach(() => {
  MockWebSocket.instances = [];
  MockResizeObserver.instances = [];
  vi.stubGlobal('WebSocket', MockWebSocket as unknown as typeof WebSocket);
  vi.stubGlobal('ResizeObserver', MockResizeObserver as unknown as typeof ResizeObserver);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
  localStorage.clear(); // state/workspace.ts persists the selection there
});

// vi.resetModules() in afterEach means each test gets its own copy of the
// whole graph, so the workspace store has to be imported from it too rather
// than statically -- otherwise the test would be driving a different store
// than agentTerminal.ts subscribed to.
async function freshModules() {
  const { useWorkspace } = await import('../state/workspace');
  const state = await import('../state/agentTerminal');
  const { TerminalView } = await import('../components/agent/TerminalView');
  return { startSession: state.startSession, useWorkspace, TerminalView };
}

describe('TerminalView', () => {
  it('sends a resize control text frame when its container resizes', async () => {
    const { startSession, TerminalView } = await freshModules();
    render(<TerminalView />);
    expect(MockResizeObserver.instances).toHaveLength(1);

    act(() => {
      startSession({ command: '/bin/zsh', dir: '/ws' });
    });
    const ws = MockWebSocket.instances[0];
    act(() => {
      ws.readyState = MockWebSocket.OPEN;
      ws.onopen?.();
    });

    ws.sent = []; // clear the no-op sent before the socket was open
    act(() => {
      MockResizeObserver.instances[0].trigger();
    });

    expect(ws.sent).toHaveLength(1);
    expect(JSON.parse(ws.sent[0] as string)).toEqual({ type: 'resize', cols: 80, rows: 24 });
  });

  it('shows a Restart control and the exit code once an exit frame arrives', async () => {
    const { startSession, TerminalView } = await freshModules();
    render(<TerminalView />);

    act(() => {
      startSession({ command: '/bin/zsh', dir: '/ws' });
    });
    const ws = MockWebSocket.instances[0];
    act(() => {
      ws.readyState = MockWebSocket.OPEN;
      ws.onopen?.();
    });

    expect(screen.queryByRole('button', { name: 'Restart' })).not.toBeInTheDocument();

    act(() => {
      ws.onmessage?.({ data: JSON.stringify({ type: 'exit', code: 2 }) });
    });

    expect(await screen.findByRole('button', { name: 'Restart' })).toBeInTheDocument();
    expect(screen.getByText(/exit code 2/)).toBeInTheDocument();
  });

  it('shows an error notice if the WebSocket cannot be constructed', async () => {
    const { startSession, TerminalView } = await freshModules();
    render(<TerminalView />);

    const OriginalWebSocket = globalThis.WebSocket;
    class ThrowingWebSocket {
      constructor() {
        throw new Error('boom');
      }
    }
    vi.stubGlobal('WebSocket', ThrowingWebSocket as unknown as typeof WebSocket);

    act(() => {
      startSession({ command: '/bin/zsh', dir: '/ws' });
    });

    expect(await screen.findByText(/Could not start: boom/)).toBeInTheDocument();
    vi.stubGlobal('WebSocket', OriginalWebSocket);
  });

  // A browser exposes nothing about a rejected upgrade -- no status, no body
  // -- so a socket that closes without ever opening is the only evidence the
  // daemon refused the start (e.g. a dir outside the selected workspace's
  // allowlist). It must not read as "the agent exited".
  it('reports a refused upgrade as a failed start, not as a finished session', async () => {
    const { startSession, TerminalView } = await freshModules();
    render(<TerminalView />);

    act(() => {
      startSession({ command: '/bin/zsh', dir: '/ws-a' });
    });
    act(() => {
      MockWebSocket.instances[0].close(); // closed before any onopen
    });

    expect(await screen.findByText(/refused to open a terminal in \/ws-a/)).toBeInTheDocument();
    expect(screen.queryByText(/Session ended/)).not.toBeInTheDocument();
  });

  // Switching workspace ends the pane rather than leaving a PTY attached to
  // the workspace the user just navigated away from: its cwd and its
  // SAPIEN_WORKSPACE were fixed at spawn and cannot follow the switch.
  it('ends the session and says why when the workspace changes', async () => {
    const { startSession, useWorkspace, TerminalView } = await freshModules();
    render(<TerminalView />);

    act(() => {
      startSession({ command: '/bin/zsh', dir: '/lab/ws-a' });
    });
    const ws = MockWebSocket.instances[0];
    act(() => {
      ws.readyState = MockWebSocket.OPEN;
      ws.onopen?.();
    });

    act(() => {
      useWorkspace.getState().select('/lab/ws-heavy');
    });

    expect(ws.readyState).toBe(MockWebSocket.CLOSED);
    expect(await screen.findByText(/the workspace changed/)).toBeInTheDocument();
    // Restarting would re-run the previous workspace's directory, which the
    // daemon now rejects, so it is not offered.
    expect(screen.getByRole('button', { name: 'Restart' })).toBeDisabled();
  });
});
