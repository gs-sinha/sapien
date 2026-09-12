import { beforeEach, describe, expect, it, vi } from 'vitest';

// The selection is per tab (sessionStorage), seeded for a brand-new tab
// from the last choice made anywhere (localStorage). Each case re-imports
// the module because the selection is read once, into a module variable,
// at load -- which is exactly the moment under test: a tab starting up.
async function freshTab() {
  vi.resetModules();
  return import('../state/workspace');
}

const KEY = 'sapien:workspace';

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

describe('workspace selection scope', () => {
  it('starts on the daemon primary when nothing is stored', async () => {
    const { currentWorkspace } = await freshTab();
    expect(currentWorkspace()).toBe('');
  });

  it('reloads into this tab’s own choice, not the last one made anywhere', async () => {
    // Tab A switched to /ws/platform after this tab had already chosen
    // /ws/payments; reloading this tab must not adopt tab A's workspace.
    sessionStorage.setItem(KEY, '/ws/payments');
    localStorage.setItem(KEY, '/ws/platform');

    const { currentWorkspace } = await freshTab();
    expect(currentWorkspace()).toBe('/ws/payments');
  });

  it('seeds a brand-new tab from the last choice made anywhere', async () => {
    localStorage.setItem(KEY, '/ws/platform');

    const { currentWorkspace } = await freshTab();
    expect(currentWorkspace()).toBe('/ws/platform');
  });

  it('keeps a tab pinned to the primary even after another tab switches', async () => {
    // "" is a real choice, not an absent one: a tab that deliberately
    // follows the daemon's primary must keep doing so.
    const { useWorkspace } = await freshTab();
    useWorkspace.getState().select('');
    localStorage.setItem(KEY, '/ws/platform');

    const { currentWorkspace } = await freshTab();
    expect(currentWorkspace()).toBe('');
  });

  it('records a switch in both scopes: this tab now, the next new tab later', async () => {
    const { useWorkspace, currentWorkspace } = await freshTab();
    useWorkspace.getState().select('/ws/platform');

    expect(currentWorkspace()).toBe('/ws/platform');
    expect(sessionStorage.getItem(KEY)).toBe('/ws/platform');
    expect(localStorage.getItem(KEY)).toBe('/ws/platform');
  });

  it('survives storage being unavailable', async () => {
    const boom = () => {
      throw new Error('blocked');
    };
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(boom);
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(boom);

    const { useWorkspace, currentWorkspace } = await freshTab();
    expect(currentWorkspace()).toBe('');
    expect(() => useWorkspace.getState().select('/ws/platform')).not.toThrow();
    expect(currentWorkspace()).toBe('/ws/platform');

    getItem.mockRestore();
    setItem.mockRestore();
  });
});
