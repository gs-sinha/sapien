// The workspace this tab is looking at.
//
// One daemon serves many workspaces (internal/workspaces), selected per
// request by the X-Sapien-Workspace header, so switching is a client-side
// choice rather than a different daemon on a different port -- which is the
// point: cookies are not isolated by port, so two daemons on 127.0.0.1 would
// overwrite each other's session.
//
// The selection lives here rather than in a React context because
// api/client.ts (not a component) has to read it on every request. It is
// deliberately not in the URL: a workspace is the frame every route is read
// in, and putting it in every path would rewrite every link in the app.
import { create } from 'zustand';

// The selection is per *tab*, not per browser profile. Multiple tabs is
// the way to multitask here -- watch a run in one, edit a flow in another
// -- and ids from one workspace never resolve in another, so a single
// shared selection meant tab B silently adopted tab A's workspace on its
// next reload and then 404ed on every id in its own URL.
//
// sessionStorage is per tab and survives reload, which is exactly the
// scope wanted. localStorage keeps the last choice as the seed for a
// *new* tab, so opening one still lands where you were working rather
// than snapping back to the primary; from then on the two tabs diverge.
const STORAGE_KEY = 'sapien:workspace';

export interface WorkspaceInfo {
  dir: string;
  name: string;
  primary?: boolean;
  open?: boolean;
  services?: number;
  error?: string;
}

// currentDir is read by api/client.ts on every request, so it is kept as a
// plain module variable as well as store state: a header builder should not
// have to subscribe to a store.
let currentDir = load();

function load(): string {
  // This tab's own choice first; the last-used one only seeds a tab that
  // has never made one.
  try {
    const own = sessionStorage.getItem(STORAGE_KEY);
    if (own !== null) return own;
  } catch {
    // Fall through to localStorage, then to the primary.
  }
  try {
    return localStorage.getItem(STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

function persist(dir: string): void {
  // Written to both: sessionStorage is this tab's answer on reload,
  // localStorage is the seed the next new tab starts from. The empty
  // string is a real choice ("the daemon's primary") and is stored as
  // one in sessionStorage -- removing the key instead would make the
  // tab fall back to localStorage and pick up another tab's workspace,
  // which is the bug this split exists to fix.
  try {
    sessionStorage.setItem(STORAGE_KEY, dir);
  } catch {
    // Private window or blocked storage: the choice just doesn't survive
    // a reload, which is better than failing the switch.
  }
  try {
    if (dir) localStorage.setItem(STORAGE_KEY, dir);
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    // As above.
  }
}

/** The workspace directory to send with API requests; "" means the daemon's primary. */
export function currentWorkspace(): string {
  return currentDir;
}

interface WorkspaceState {
  current: string;
  list: WorkspaceInfo[];
  setList: (list: WorkspaceInfo[]) => void;
  select: (dir: string) => void;
}

export const useWorkspace = create<WorkspaceState>((set) => ({
  current: currentDir,
  list: [],
  setList: (list) =>
    set((state) => {
      // A stored selection that the daemon no longer offers (the workspace
      // was forgotten, moved, or is served by a different daemon) falls back
      // to the primary rather than leaving every request 404ing.
      if (state.current && !list.some((w) => w.dir === state.current)) {
        currentDir = '';
        persist('');
        return { list, current: '' };
      }
      return { list };
    }),
  select: (dir) => {
    currentDir = dir;
    persist(dir);
    set({ current: dir });
  },
}));
