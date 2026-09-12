// Is the daemon this tab loaded from still the daemon that is running?
//
// Two things make a tab outlive its daemon. `sapien serve` idle-exits
// after thirty minutes with nothing connected, and any CLI or MCP call
// made after a `brew upgrade sapien` replaces the running daemon outright
// (internal/cli/daemonctl.go's replaceStaleDaemon), because the binary
// carries the UI embedded in it and the two must match. Both leave the
// page holding a build the daemon no longer serves.
//
// Neither is detectable from the event socket alone: it just closes and
// retries forever. So when reconnecting stops working, we ask
// /v1/health -- the one unauthenticated route -- who is there now, and
// compare it with whoever answered when this tab started.
//
// No polling. probe() is called on mount and then only when the socket
// has failed to come back, which is the only moment the answer can have
// changed in a way that matters.
import { create } from 'zustand';
import type { HealthResponse } from '../api/types';

/**
 * A liveness/identity probe, deliberately not api/client's `getHealth`.
 *
 * It sends no workspace header and no credentials, because it has to work
 * in exactly the states where a normal request cannot: /v1/health is the
 * one route outside authMiddleware (internal/server/middleware.go), so it
 * answers even with a stale session cookie, and omitting the workspace
 * header means a daemon that has never heard of this tab's workspace
 * still reports its own version rather than 404ing. Resolves to null when
 * nothing is listening.
 *
 * It lives here rather than in api/client so the dependency runs one way:
 * client.ts reports 401s to this store, and this store calls nothing of
 * client's.
 */
export async function probeDaemon(): Promise<HealthResponse | null> {
  try {
    const res = await fetch('/v1/health', { method: 'GET', headers: { Accept: 'application/json' } });
    if (!res.ok) return null;
    return (await res.json()) as HealthResponse;
  } catch {
    return null;
  }
}

export type DaemonState =
  /** No answer yet. */
  | 'unknown'
  /** The daemon that served this tab is still the one answering. */
  | 'ok'
  /** A daemon is answering, but it is a different build: reload to match it. */
  | 'replaced'
  /** Nothing is listening: it idle-exited or was stopped. */
  | 'gone';

interface DaemonStore {
  state: DaemonState;
  /**
   * This tab's session cookie was rejected. Tracked separately from
   * `state` because it is orthogonal: the daemon can be perfectly
   * reachable and the right build, and this tab still be signed in to a
   * previous one.
   */
  sessionStale: boolean;
  /** The version that answered first, i.e. the build this tab's UI came from. */
  loadedVersion: string | null;
  /** The version answering now, when it differs from loadedVersion. */
  runningVersion: string | null;
  probe: () => Promise<void>;
}

export const useDaemon = create<DaemonStore>((set, get) => ({
  state: 'unknown',
  sessionStale: false,
  loadedVersion: null,
  runningVersion: null,

  probe: async () => {
    const health = await probeDaemon();
    if (!health) {
      set({ state: 'gone' });
      return;
    }
    const loaded = get().loadedVersion;
    if (loaded === null) {
      // First answer of this tab's life: whatever is running now is, by
      // definition, the build the page was served by.
      set({ state: 'ok', loadedVersion: health.version, runningVersion: null });
      return;
    }
    if (health.version !== loaded) {
      set({ state: 'replaced', runningVersion: health.version });
      return;
    }
    // Same build, answering again: an idle-exited daemon that a `sapien
    // ui` relaunch brought back on the same port. Nothing to tell the
    // user here; if this tab's cookie is for the previous daemon, the
    // first 401 says so.
    set({ state: 'ok', runningVersion: null });
  },
}));

/** Called by api/client on a 401 (set) and on any success (clear). */
export function setSessionStale(stale: boolean): void {
  if (useDaemon.getState().sessionStale !== stale) {
    useDaemon.setState({ sessionStale: stale });
  }
}
