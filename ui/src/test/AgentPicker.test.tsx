// Picker (ui/src/components/agent/Picker.tsx) tests: PLAN §34c Phase 7b
// item 6's "targets rendering" and "start posts the right URL".
//
// xterm/xterm-addon-fit are mocked because jsdom (this project's test
// environment) can't run real xterm.js: Terminal.open() reaches for
// HTMLCanvasElement.getContext('2d') and window.matchMedia, neither of
// which jsdom implements. WebSocket is mocked the same way
// state/events.ts's own tests mock it (src/test/events.test.ts), since
// startSession (state/agentTerminal.ts) opens a real one.
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

vi.mock('../api/agentExtra', async () => {
  const actual = await vi.importActual<typeof import('../api/agentExtra')>('../api/agentExtra');
  return {
    ...actual,
    getTerminalTargets: vi.fn().mockResolvedValue({
      commands: ['claude', 'codex', '/bin/zsh'],
      dirs: [
        { label: 'Workspace', path: '/ws' },
        { label: 'order-service', path: '/ws/services/order-service/api' },
      ],
    }),
  };
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

beforeEach(() => {
  MockWebSocket.instances = [];
  vi.stubGlobal('WebSocket', MockWebSocket as unknown as typeof WebSocket);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
  localStorage.clear(); // state/workspace.ts persists the selection there
});

// The selected workspace decides which directories the picker offers and
// which workspace Start opens the pane in, so every test sets it explicitly
// -- on the freshly imported store, which is the same module instance the
// component will see. A statically imported one would not be: afterEach's
// vi.resetModules() means each test's dynamic import re-evaluates the whole
// graph, state/workspace.ts included.
async function freshPicker(workspace = '') {
  const { useWorkspace } = await import('../state/workspace');
  useWorkspace.getState().select(workspace);
  const { Picker } = await import('../components/agent/Picker');
  return Picker;
}

describe('Picker', () => {
  it('renders commands and directories from GET /v1/terminal/targets', async () => {
    const Picker = await freshPicker();
    render(<Picker />);

    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Command' })).toBeInTheDocument());
    expect(screen.getByRole('option', { name: 'claude' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'codex' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '/bin/zsh' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Workspace' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'order-service' })).toBeInTheDocument();
  });

  it('defaults to claude and the first directory, and Start opens a WebSocket at the right URL', async () => {
    const Picker = await freshPicker();
    render(<Picker />);

    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Command' })).toHaveValue('claude'));
    expect(screen.getByRole('combobox', { name: 'Directory' })).toHaveValue('/ws');

    await userEvent.click(screen.getByRole('button', { name: 'Start' }));

    expect(MockWebSocket.instances).toHaveLength(1);
    const url = new URL(MockWebSocket.instances[0].url, 'http://localhost');
    expect(url.pathname).toBe('/v1/terminal');
    expect(url.searchParams.get('command')).toBe('claude');
    expect(url.searchParams.get('dir')).toBe('/ws');
    expect(url.searchParams.get('cols')).toBe('80');
    expect(url.searchParams.get('rows')).toBe('24');
  });

  it('picking a different command/directory changes the Start URL', async () => {
    const Picker = await freshPicker();
    render(<Picker />);
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Command' })).toHaveValue('claude'));

    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Command' }), 'codex');
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Directory' }), '/ws/services/order-service/api');
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));

    const url = new URL(MockWebSocket.instances[0].url, 'http://localhost');
    expect(url.searchParams.get('command')).toBe('codex');
    expect(url.searchParams.get('dir')).toBe('/ws/services/order-service/api');
  });

  it('starts the pane in the selected workspace, not the daemon primary', async () => {
    const Picker = await freshPicker('/lab/ws-heavy');
    render(<Picker />);
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Command' })).toHaveValue('claude'));

    await userEvent.click(screen.getByRole('button', { name: 'Start' }));

    // ?workspace= is what gives the PTY its working directory *and* its
    // SAPIEN_WORKSPACE (internal/server/handlers_terminal.go); without it
    // the daemon fell back to the primary workspace whatever the UI showed.
    const url = new URL(MockWebSocket.instances[0].url, 'http://localhost');
    expect(url.searchParams.get('workspace')).toBe('/lab/ws-heavy');
  });

  it('re-asks for targets when the workspace changes, so the directory list is never the old one', async () => {
    const Picker = await freshPicker();
    const { useWorkspace } = await import('../state/workspace');
    const { getTerminalTargets } = await import('../api/agentExtra');
    // The vi.mock factory's fn is shared across this file's tests, so count
    // from here rather than from zero.
    const fetchTargets = vi.mocked(getTerminalTargets);
    fetchTargets.mockClear();
    render(<Picker />);

    await waitFor(() => expect(fetchTargets).toHaveBeenCalledTimes(1));

    act(() => {
      useWorkspace.getState().select('/lab/ws-heavy');
    });

    await waitFor(() => expect(fetchTargets).toHaveBeenCalledTimes(2));
  });
});
