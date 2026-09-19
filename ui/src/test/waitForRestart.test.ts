// state/daemon.ts's waitForRestart, exercised directly with fake timers
// (same technique as events.test.ts's reconnect-backoff tests) so the
// 500ms-poll / 1.5s-fail-phase / 30s-total behaviour from PLAN §34f items
// 3/4 ("wait until health fails once or 1.5s pass, then until it
// succeeds") is verified without needing 30 real seconds per test.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDaemon, waitForRestart } from '../state/daemon';

const fetchMock = vi.fn();

function health(version = '1.3.1') {
  fetchMock.mockResolvedValueOnce({ ok: true, json: async () => ({ ok: true, version, workspace: '/ws' }) });
}
function unreachable() {
  fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  useDaemon.setState({ state: 'unknown', loadedVersion: null, runningVersion: null, sessionStale: false, restarting: false });
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('waitForRestart', () => {
  it('resolves immediately (no sleep) when the first probe already fails and the second already succeeds', async () => {
    unreachable();
    health('1.3.1');

    const result = await waitForRestart();
    expect(result?.version).toBe('1.3.1');
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('sets restarting on the store while waiting and clears it on success', async () => {
    unreachable();
    health('1.3.1');
    expect(useDaemon.getState().restarting).toBe(false);

    const promise = waitForRestart();
    expect(useDaemon.getState().restarting).toBe(true);
    await promise;
    expect(useDaemon.getState().restarting).toBe(false);
  });

  it('polls every pollMs through the fail phase, then through the success phase, until it succeeds', async () => {
    // Phase 1: stays reachable (the old process hasn't gone down yet) for
    // two polls, so the fail-phase timeout (not a failure) ends it.
    health('1.3.1');
    health('1.3.1');
    // Phase 2: two more unreachable polls (successor still starting), then up.
    unreachable();
    unreachable();
    health('1.4.0');

    let result: Awaited<ReturnType<typeof waitForRestart>> | undefined;
    waitForRestart({ pollMs: 500, failTimeoutMs: 1500, totalTimeoutMs: 30000 }).then((r) => {
      result = r;
    });

    // Enough ticks to cover both phases' polls plus their sleeps.
    await vi.advanceTimersByTimeAsync(5 * 500 + 100);

    expect(result?.version).toBe('1.4.0');
    // 2 (phase 1, both reachable) + 2 (phase 2 failures) + 1 (phase 2 success) = 5.
    expect(fetchMock).toHaveBeenCalledTimes(5);
  });

  it('gives up and resolves null once totalTimeoutMs elapses with nothing answering', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));

    let result: Awaited<ReturnType<typeof waitForRestart>> | undefined;
    waitForRestart({ pollMs: 500, failTimeoutMs: 1500, totalTimeoutMs: 5000 }).then((r) => {
      result = r;
    });

    await vi.advanceTimersByTimeAsync(5100);

    expect(result).toBeNull();
    expect(useDaemon.getState().restarting).toBe(false);
  });
});
