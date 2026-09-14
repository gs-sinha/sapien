// Read-only git status of the workspace folder itself (the team's git
// repository), kept live without polling: seeded once from GET
// /v1/workspace/repo on app load, then updated from the `workspace.repo`
// event fired after every fetch, pull, or sync (the daemon's own ten-minute
// tick included). StatusBar reads this store directly; ServicesPage's "Sync
// all" and the Pull action write straight from their own responses too, so
// the badge never has to wait for the event to round-trip.
import { create } from 'zustand';
import { repo as repoApi } from '../api/client';
import { subscribe } from './events';
import type { RepoStatus } from '../api/types';

interface RepoState {
  status: RepoStatus | null;
  started: boolean;
  setStatus: (s: RepoStatus) => void;
  // Returns a Promise (rather than firing and forgetting) so a caller --
  // App.tsx doesn't need to, but a test does -- can await the initial seed.
  init: () => Promise<void>;
}

export const useRepo = create<RepoState>((set, get) => ({
  status: null,
  started: false,

  setStatus: (s) => set({ status: s }),

  init: () => {
    if (get().started) return Promise.resolve();
    set({ started: true });
    subscribe('workspace.repo', (e) => {
      if (e.repo) set({ status: e.repo });
    });
    return repoApi
      .status()
      .then((s) => set({ status: s }))
      .catch(() => {
        // A daemon not reachable yet, or too old for this route: the repo
        // segment just stays hidden until the first workspace.repo event.
      });
  },
}));

// One clause describing what the last fetch/sync/pull did, for a toast that
// already reported something else (e.g. "Sync all"). Order matters: a
// failed fetch dominates the story regardless of the other counters, and
// "already current" is only true once nothing else applies.
export function repoClause(status: RepoStatus): string {
  if (status.fetch_error) return `team repo: fetch failed (${status.fetch_error})`;
  if (status.pulled) return `team repo: pulled ${status.pulled_count ?? 0} commits`;
  if (status.skipped) return `team repo: not pulled (${status.skipped})`;
  return 'team repo: already current';
}
