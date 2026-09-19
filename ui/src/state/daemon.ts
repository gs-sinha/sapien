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
  /**
   * Set for the duration of an intentional restart/upgrade kicked off from
   * Settings (PLAN §34f items 3/4): the old daemon process going down makes
   * probe() briefly see 'gone', which would otherwise flash DaemonBanner's
   * GoneBanner ("wasn't running... start it again") over a restart the user
   * just asked for. DaemonBanner checks this before rendering that banner.
   */
  restarting: boolean;
  probe: () => Promise<void>;
}

export const useDaemon = create<DaemonStore>((set, get) => ({
  state: 'unknown',
  sessionStale: false,
  loadedVersion: null,
  runningVersion: null,
  restarting: false,

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

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export interface WaitForRestartOptions {
  /** How often to poll /v1/health while waiting (ms). Default 500. */
  pollMs?: number;
  /** End the "wait for the old process to go down" phase once this much time has passed with no failure seen (ms). Default 1500. */
  failTimeoutMs?: number;
  /** Give up entirely after this much time (ms). Default 30000. */
  totalTimeoutMs?: number;
}

/**
 * Settings' Restart/Upgrade actions (PLAN §34f items 3/4) call this right
 * after POST /v1/daemon/restart or /v1/update/apply return 202, both of
 * which tear down this process and spawn a detached successor on the same
 * port. Per the contract: "wait until health fails once or 1.5s pass, then
 * until it succeeds" -- phase one confirms the old process actually exited
 * (instead of racing it and declaring victory on its own still-healthy
 * answer), phase two waits out the successor's startup. Resolves with the
 * successor's HealthResponse, or null if it never answered within
 * totalTimeoutMs. Sets/clears `restarting` around the whole wait so
 * DaemonBanner's GoneBanner doesn't flash for the gap in between.
 */
export async function waitForRestart(opts: WaitForRestartOptions = {}): Promise<HealthResponse | null> {
  const pollMs = opts.pollMs ?? 500;
  const failTimeoutMs = opts.failTimeoutMs ?? 1500;
  const totalTimeoutMs = opts.totalTimeoutMs ?? 30000;
  useDaemon.setState({ restarting: true });
  const start = Date.now();
  try {
    while (Date.now() - start < failTimeoutMs) {
      if (!(await probeDaemon())) break;
      await sleep(pollMs);
    }
    while (Date.now() - start < totalTimeoutMs) {
      const health = await probeDaemon();
      if (health) return health;
      await sleep(pollMs);
    }
    return null;
  } finally {
    useDaemon.setState({ restarting: false });
  }
}

/** Called by api/client on a 401 (set) and on any success (clear). */
export function setSessionStale(stale: boolean): void {
  if (useDaemon.getState().sessionStale !== stale) {
    useDaemon.setState({ sessionStale: stale });
  }
}
