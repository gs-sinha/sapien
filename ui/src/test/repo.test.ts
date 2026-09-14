import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { RepoStatus } from '../api/types';

const repoStatus = vi.fn(async (): Promise<RepoStatus> => ({ in_git: false, behind: 0, ahead: 0, dirty: 0 }));

vi.mock('../api/client', () => ({
  repo: {
    status: () => repoStatus(),
  },
}));

beforeEach(() => {
  repoStatus.mockClear();
});

// Each case re-imports both modules fresh: init() guards against a second
// call with its own `started` flag, and state/events.ts's listener map is a
// module-level singleton, so a stale subscription from a previous test
// would otherwise leak into this one.
async function freshModules() {
  vi.resetModules();
  const repo = await import('../state/repo');
  const events = await import('../state/events');
  return { ...repo, ...events };
}

describe('repo store', () => {
  it('seeds status from repo.status() once on init()', async () => {
    repoStatus.mockResolvedValueOnce({ in_git: true, branch: 'main', behind: 1, ahead: 0, dirty: 0 });
    const { useRepo } = await freshModules();

    expect(useRepo.getState().status).toBeNull();
    await useRepo.getState().init();

    expect(repoStatus).toHaveBeenCalledTimes(1);
    expect(useRepo.getState().status).toEqual({ in_git: true, branch: 'main', behind: 1, ahead: 0, dirty: 0 });
  });

  it('only seeds once even if init() is called again', async () => {
    const { useRepo } = await freshModules();
    await useRepo.getState().init();
    await useRepo.getState().init();
    expect(repoStatus).toHaveBeenCalledTimes(1);
  });

  it('updates from a workspace.repo event, live, without a fresh GET', async () => {
    const { useRepo, useEvents, summarize } = await freshModules();
    await useRepo.getState().init();
    expect(repoStatus).toHaveBeenCalledTimes(1);

    const pushed: RepoStatus = { in_git: true, branch: 'main', behind: 0, ahead: 2, dirty: 0, pulled: false };
    useEvents.getState()._append(summarize({ type: 'workspace.repo', time: 't1', payload: pushed }));

    expect(useRepo.getState().status).toEqual(pushed);
    // The event alone updated the store; no extra GET was made.
    expect(repoStatus).toHaveBeenCalledTimes(1);
  });

  it('setStatus writes straight from a direct response (e.g. Pull or Sync all)', async () => {
    const { useRepo } = await freshModules();
    const next: RepoStatus = { in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pulled: true, pulled_count: 4 };
    useRepo.getState().setStatus(next);
    expect(useRepo.getState().status).toEqual(next);
  });
});

describe('repoClause', () => {
  it('reports the fetch error first, regardless of the other counters', async () => {
    const { repoClause } = await freshModules();
    expect(repoClause({ in_git: true, behind: 3, ahead: 0, dirty: 0, fetch_error: 'timeout' })).toBe('team repo: fetch failed (timeout)');
  });

  it('reports a completed pull', async () => {
    const { repoClause } = await freshModules();
    expect(repoClause({ in_git: true, behind: 0, ahead: 0, dirty: 0, pulled: true, pulled_count: 5 })).toBe('team repo: pulled 5 commits');
  });

  it('reports why a pull was skipped', async () => {
    const { repoClause } = await freshModules();
    expect(repoClause({ in_git: true, behind: 2, ahead: 0, dirty: 1, skipped: 'uncommitted changes' })).toBe(
      'team repo: not pulled (uncommitted changes)',
    );
  });

  it('reports "already current" once nothing else applies', async () => {
    const { repoClause } = await freshModules();
    expect(repoClause({ in_git: true, behind: 0, ahead: 0, dirty: 0 })).toBe('team repo: already current');
  });
});
