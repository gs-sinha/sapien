import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { agentHandoffURL, getTerminalTargets, terminalWebSocketURL } from '../api/agentExtra';
import { useWorkspace } from '../state/workspace';

// jsdom (and Node 16) has no native global `fetch`, so there is nothing to
// vi.spyOn -- stub a fresh mock function as the global instead, the same way
// client.test.ts does.
const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
});

afterEach(() => {
  useWorkspace.getState().select('');
  vi.unstubAllGlobals();
});

function okResponse(body: unknown): Response {
  return { ok: true, status: 200, statusText: 'OK', text: async () => JSON.stringify(body) } as Response;
}

describe('terminalWebSocketURL', () => {
  it('builds a ws:// URL with command/dir/cols/rows', () => {
    const url = terminalWebSocketURL({ command: 'claude', dir: '/ws', cols: 100, rows: 30, workspace: '' });
    const parsed = new URL(url);
    expect(parsed.protocol).toBe('ws:');
    expect(parsed.pathname).toBe('/v1/terminal');
    expect(parsed.searchParams.get('command')).toBe('claude');
    expect(parsed.searchParams.get('dir')).toBe('/ws');
    expect(parsed.searchParams.get('cols')).toBe('100');
    expect(parsed.searchParams.get('rows')).toBe('30');
    // The primary workspace is the absence of a selector, exactly as it is
    // for the header (internal/server/workspacectx.go).
    expect(parsed.searchParams.get('workspace')).toBeNull();
  });

  it('carries the selected workspace as ?workspace=, since a browser cannot set a header on an upgrade', () => {
    const url = terminalWebSocketURL({ command: 'claude', dir: '/lab/ws-heavy', cols: 80, rows: 24, workspace: '/lab/ws-heavy' });
    expect(new URL(url).searchParams.get('workspace')).toBe('/lab/ws-heavy');
  });
});

describe('getTerminalTargets', () => {
  it('sends the selected workspace, so the picker offers that workspace directories', async () => {
    fetchMock.mockResolvedValueOnce(okResponse({ commands: ['claude'], dirs: [{ label: 'Workspace', path: '/lab/ws-heavy' }] }));
    vi.stubGlobal('fetch', fetchMock);
    useWorkspace.getState().select('/lab/ws-heavy');

    const targets = await getTerminalTargets();
    expect(targets.dirs[0].path).toBe('/lab/ws-heavy');

    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe('/v1/terminal/targets');
    expect(init.headers['X-Sapien-Workspace']).toBe('/lab/ws-heavy');
  });

  it('omits the workspace header while the primary is selected', async () => {
    fetchMock.mockResolvedValueOnce(okResponse({ commands: [], dirs: [] }));
    vi.stubGlobal('fetch', fetchMock);

    await getTerminalTargets();

    const [, init] = fetchMock.mock.calls[0];
    expect(init.headers['X-Sapien-Workspace']).toBeUndefined();
  });
});

describe('agentHandoffURL', () => {
  it('builds a hand-off sentence naming the run and step', () => {
    const url = agentHandoffURL({ runId: 'run_123', step: 'createOrder' });
    expect(url.startsWith('/ui/agent?prompt=')).toBe(true);
    const prompt = new URLSearchParams(url.slice('/ui/agent?'.length)).get('prompt');
    expect(prompt).toBe('Look at Sapien run run_123 (use get_run) and explain why step createOrder failed.');
  });

  it('falls back to a run-only sentence when no step is given', () => {
    const url = agentHandoffURL({ runId: 'run_123' });
    const prompt = new URLSearchParams(url.slice('/ui/agent?'.length)).get('prompt');
    expect(prompt).toBe('Look at Sapien run run_123 (use get_run) and explain what happened.');
  });
});
