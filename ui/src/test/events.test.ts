import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/client', () => ({
  getRecentEvents: vi.fn().mockResolvedValue([]),
}));

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;
  closed = false;

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }
  close() {
    this.closed = true;
    this.onclose?.();
  }
}

async function freshEventsModule() {
  vi.resetModules();
  return import('../state/events');
}

beforeEach(() => {
  MockWebSocket.instances = [];
  vi.stubGlobal('WebSocket', MockWebSocket as unknown as typeof WebSocket);
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('summarize', () => {
  it('reduces run.step to type/time/summary/ids without keeping the payload', async () => {
    const { summarize } = await freshEventsModule();
    const s = summarize({
      type: 'run.step',
      time: '2026-01-01T00:00:00Z',
      payload: { run_id: 'run_1', step_id: 'call', status: 'passed', attempt: 1 },
    });
    expect(s).toEqual({
      type: 'run.step',
      time: '2026-01-01T00:00:00Z',
      summary: 'run run_1 step call: passed',
      ids: { run_id: 'run_1', step_id: 'call' },
    });
  });

  it('reduces catalog.changed with add/remove/change counts', async () => {
    const { summarize } = await freshEventsModule();
    const s = summarize({
      type: 'catalog.changed',
      time: 't',
      payload: { service: 'orders', added: ['a'], removed: [], changed: ['b', 'c'] },
    });
    expect(s.summary).toBe('orders: +1 -0 ~2');
    expect(s.ids.service).toBe('orders');
  });
});

describe('events store', () => {
  it('opens a WebSocket at /v1/events and tracks status', async () => {
    const { useEvents } = await freshEventsModule();
    useEvents.getState().start();
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0].url).toBe(`ws://${window.location.host}/v1/events`);
    expect(useEvents.getState().status).toBe('connecting');

    MockWebSocket.instances[0].onopen?.();
    expect(useEvents.getState().status).toBe('open');
  });

  it('appends incoming events and caps at 500', async () => {
    const { useEvents } = await freshEventsModule();
    useEvents.getState().start();
    const ws = MockWebSocket.instances[0];

    for (let i = 0; i < 510; i++) {
      ws.onmessage?.({ data: JSON.stringify({ type: 'flow.changed', time: String(i), payload: { id: `flow_${i}` } }) });
    }

    const events = useEvents.getState().events;
    expect(events).toHaveLength(500);
    // The oldest 10 were dropped; the first remaining event is #10.
    expect(events[0].ids.flow_id).toBe('flow_10');
    expect(events[events.length - 1].ids.flow_id).toBe('flow_509');
    expect(useEvents.getState().unreadCount).toBe(510);
  });

  it('notifies subscribe(type, fn) listeners only for matching events', async () => {
    const { useEvents, subscribe } = await freshEventsModule();
    useEvents.getState().start();
    const ws = MockWebSocket.instances[0];

    const runStepFn = vi.fn();
    const flowChangedFn = vi.fn();
    subscribe('run.step', runStepFn);
    subscribe('flow.changed', flowChangedFn);

    ws.onmessage?.({ data: JSON.stringify({ type: 'run.step', time: 't', payload: { run_id: 'r1', step_id: 's1', status: 'passed' } }) });

    expect(runStepFn).toHaveBeenCalledTimes(1);
    expect(runStepFn.mock.calls[0][0].ids.run_id).toBe('r1');
    expect(flowChangedFn).not.toHaveBeenCalled();
  });

  it('reconnects with backoff after the socket closes', async () => {
    const { useEvents } = await freshEventsModule();
    useEvents.getState().start();
    expect(MockWebSocket.instances).toHaveLength(1);

    MockWebSocket.instances[0].onopen?.();
    MockWebSocket.instances[0].close();
    expect(useEvents.getState().status).toBe('closed');
    expect(MockWebSocket.instances).toHaveLength(1); // not yet reconnected

    await vi.advanceTimersByTimeAsync(1000);
    expect(MockWebSocket.instances).toHaveLength(2);
  });

  it('stop() prevents further reconnects', async () => {
    const { useEvents } = await freshEventsModule();
    useEvents.getState().start();
    useEvents.getState().stop();
    MockWebSocket.instances[0].close();

    await vi.advanceTimersByTimeAsync(5000);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(useEvents.getState().status).toBe('closed');
  });
});
