import { describe, expect, it } from 'vitest';
import { agentHandoffURL, terminalWebSocketURL } from '../api/agentExtra';

describe('terminalWebSocketURL', () => {
  it('builds a ws:// URL with command/dir/cols/rows', () => {
    const url = terminalWebSocketURL({ command: 'claude', dir: '/ws', cols: 100, rows: 30 });
    const parsed = new URL(url);
    expect(parsed.protocol).toBe('ws:');
    expect(parsed.pathname).toBe('/v1/terminal');
    expect(parsed.searchParams.get('command')).toBe('claude');
    expect(parsed.searchParams.get('dir')).toBe('/ws');
    expect(parsed.searchParams.get('cols')).toBe('100');
    expect(parsed.searchParams.get('rows')).toBe('30');
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
