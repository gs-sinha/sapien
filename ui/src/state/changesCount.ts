// Feeds the Nav "Changes" badge (PLAN §34f item 1) without polling: one
// GET on mount, like state/friction.ts's useFrictionCount, then a debounced
// refresh on the same events ChangesPage itself listens to. There is no
// smaller "just the count" endpoint in the contract, so this reuses the
// same GET /v1/workspace/repo/changes the page renders from; the daemon
// keeps that call cheap (a `git status`, not a diff).
import { create } from 'zustand';
import { repoChanges } from '../api/client';
import { subscribe } from './events';

interface ChangesCountState {
  count: number;
  inGit: boolean;
  refresh: () => void;
}

export const useChangesCount = create<ChangesCountState>((set) => ({
  count: 0,
  inGit: false,
  refresh: () => {
    repoChanges
      .get()
      .then((r) => set({ count: r.files.length, inGit: r.status.in_git }))
      .catch(() => {
        // Daemon not reachable yet, or workspace not in git: the badge
        // just stays hidden until the next successful refresh.
      });
  },
}));

let debounceTimer: ReturnType<typeof setTimeout> | null = null;
function debouncedRefresh() {
  if (debounceTimer) clearTimeout(debounceTimer);
  debounceTimer = setTimeout(() => useChangesCount.getState().refresh(), 300);
}

let subscribed = false;
export function ensureChangesCountSubscribed(): void {
  if (subscribed) return;
  subscribed = true;
  for (const type of ['workspace.repo', 'flow.changed', 'memory.created', 'memory.changed', 'catalog.changed'] as const) {
    subscribe(type, debouncedRefresh);
  }
}
