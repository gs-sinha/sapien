// Event stream store (PLAN 34c: "Everything updates from the event stream").
//
// Memory discipline: this store never keeps a request or response body. Each
// incoming domain.Event (which for run.finished, memory.created, etc. can
// carry a full Run or Memory with bodies/text) is immediately reduced to a
// StoredEvent -- type, time, and a short summary line plus a few ids -- and
// the raw payload is discarded. At most maxEvents (500) are kept; older
// events are dropped from the front.
import { create } from 'zustand';
import { getRecentEvents } from '../api/client';
import { useDaemon } from './daemon';
import { currentWorkspace } from './workspace';
import type { Event, EventType, RepoStatus } from '../api/types';

export type ConnectionStatus = 'connecting' | 'open' | 'closed';

export interface StoredEvent {
  type: EventType;
  time: string;
  summary: string;
  // The run/step status carried by a run.started, run.step, or run.finished
  // event, kept as its own field (it's already parsed out for `summary`) so a
  // subscriber can follow a run's progress without re-parsing the summary
  // line. Undefined for every other event type.
  status?: string;
  // The full RepoStatus payload of a workspace.repo event: state/repo.ts's
  // store is fed straight from this rather than from a fresh GET, so it
  // needs the payload itself, not just a summary line. Undefined for every
  // other event type.
  repo?: RepoStatus;
  ids: {
    run_id?: string;
    step_id?: string;
    flow_id?: string;
    memory_id?: string;
    service?: string;
  };
}

const maxEvents = 500;

function asRecord(v: unknown): Record<string, unknown> | undefined {
  return v && typeof v === 'object' ? (v as Record<string, unknown>) : undefined;
}
function str(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined;
}
function num(v: unknown): number {
  return typeof v === 'number' ? v : 0;
}
function bool(v: unknown): boolean {
  return v === true;
}

// asRepoStatus rebuilds the typed RepoStatus a workspace.repo event carries
// as its raw (untyped) payload, the same fields GET /v1/workspace/repo
// answers.
function asRepoStatus(p: Record<string, unknown>): RepoStatus {
  return {
    in_git: bool(p.in_git),
    root: str(p.root),
    branch: str(p.branch),
    remote: str(p.remote),
    upstream: str(p.upstream),
    behind: num(p.behind),
    ahead: num(p.ahead),
    dirty: num(p.dirty),
    fetched_at: str(p.fetched_at),
    fetch_error: str(p.fetch_error),
    pulled: typeof p.pulled === 'boolean' ? p.pulled : undefined,
    pulled_count: typeof p.pulled_count === 'number' ? p.pulled_count : undefined,
    skipped: str(p.skipped),
  };
}

// summarize reduces one raw domain.Event to a StoredEvent, matching the
// payload shapes actually emitted (internal/runner, internal/registry/sync.go,
// internal/engine/local/{flows,memories,services}.go): never retains bodies.
export function summarize(ev: Event): StoredEvent {
  const p = asRecord(ev.payload) || {};
  const ids: StoredEvent['ids'] = {};
  let summary: string = ev.type;
  let eventStatus: string | undefined;
  let eventRepo: RepoStatus | undefined;

  switch (ev.type) {
    case 'run.started':
    case 'run.finished': {
      ids.run_id = str(p.id);
      ids.flow_id = str(p.flow_id);
      const status = str(p.status);
      eventStatus = status;
      const summaryObj = asRecord(p.summary);
      const passed = summaryObj ? Number(summaryObj.steps_passed ?? 0) : undefined;
      const total = summaryObj ? Number(summaryObj.steps_total ?? 0) : undefined;
      summary = `run ${ids.run_id ?? ''} ${status ?? ''}`.trim();
      if (passed !== undefined && total !== undefined) summary += ` (${passed}/${total})`;
      break;
    }
    case 'run.step': {
      ids.run_id = str(p.run_id);
      ids.step_id = str(p.step_id);
      const status = str(p.status);
      eventStatus = status;
      summary = `run ${ids.run_id ?? ''} step ${ids.step_id ?? ''}: ${status ?? ''}`.trim();
      break;
    }
    case 'memory.created':
    case 'memory.changed': {
      ids.memory_id = str(p.id);
      const action = str(p.action);
      const scope = str(p.scope);
      summary = `memory ${ids.memory_id ?? ''}${action ? ` ${action}` : ''}${scope ? ` (${scope})` : ''}`.trim();
      break;
    }
    case 'flow.changed': {
      ids.flow_id = str(p.id);
      const name = str(p.name);
      const area = str(p.area);
      summary = ids.flow_id ? `flow ${ids.flow_id}${name ? ` (${name})` : ''}` : area ? `flows changed (${area})` : 'flows changed';
      break;
    }
    case 'catalog.changed': {
      ids.service = str(p.service);
      const added = Array.isArray(p.added) ? p.added.length : 0;
      const removed = Array.isArray(p.removed) ? p.removed.length : 0;
      const changed = Array.isArray(p.changed) ? p.changed.length : 0;
      summary = `${ids.service ?? 'catalog'}: +${added} -${removed} ~${changed}`;
      break;
    }
    case 'service.sync_failed': {
      ids.service = str(p.service);
      summary = `${ids.service ?? 'service'} sync failed: ${str(p.error) ?? ''}`.trim();
      break;
    }
    case 'workspace.repo': {
      const r = asRepoStatus(p);
      eventRepo = r;
      summary = r.fetch_error
        ? `team repo: fetch failed (${r.fetch_error})`
        : r.pulled
          ? `team repo: pulled ${r.pulled_count ?? 0} commits`
          : `team repo: ↓${r.behind} ↑${r.ahead}${r.dirty ? ` (${r.dirty} uncommitted)` : ''}`;
      break;
    }
    default:
      summary = ev.type;
  }

  return { type: ev.type, time: ev.time, summary, status: eventStatus, repo: eventRepo, ids };
}

type Listener = (e: StoredEvent) => void;
const listeners = new Map<EventType, Set<Listener>>();

// subscribe registers fn to be called (synchronously, outside React's render
// cycle) for every future event of exactly `type`. Returns an unsubscribe
// function. Pages use this instead of re-rendering on every event: e.g. a
// run detail page subscribes to "run.step"/"run.finished" and checks
// e.ids.run_id against its own id before refetching.
export function subscribe(type: EventType, fn: Listener): () => void {
  let set = listeners.get(type);
  if (!set) {
    set = new Set();
    listeners.set(type, set);
  }
  set.add(fn);
  return () => {
    set!.delete(fn);
  };
}

function notify(e: StoredEvent) {
  listeners.get(e.type)?.forEach((fn) => fn(e));
}

interface EventsState {
  status: ConnectionStatus;
  events: StoredEvent[];
  unreadCount: number;
  started: boolean;
  start: () => void;
  stop: () => void;
  markRead: () => void;
  // restart reconnects the socket after the selected workspace changes,
  // dropping the previous workspace's buffered events.
  restart: () => void;
  // exposed for tests
  _append: (e: StoredEvent) => void;
}

let socket: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectDelayMs = 1000;
const maxReconnectDelayMs = 30000;
let stopped = false;

function wsURL(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  // A browser cannot set headers on a WebSocket handshake, so the workspace
  // selector rides in the query string here; the daemon accepts either
  // (internal/server/workspacectx.go).
  const ws = currentWorkspace();
  const query = ws ? `?workspace=${encodeURIComponent(ws)}` : '';
  return `${proto}//${window.location.host}/v1/events${query}`;
}

export const useEvents = create<EventsState>((set, get) => ({
  status: 'closed',
  events: [],
  unreadCount: 0,
  started: false,

  _append: (e: StoredEvent) => {
    set((state) => {
      const events = [...state.events, e];
      if (events.length > maxEvents) events.splice(0, events.length - maxEvents);
      return { events, unreadCount: state.unreadCount + 1 };
    });
    notify(e);
  },

  markRead: () => set({ unreadCount: 0 }),

  start: () => {
    if (get().started) return;
    set({ started: true, status: 'connecting' });
    stopped = false;

    getRecentEvents(maxEvents)
      .then((recent) => {
        if (stopped) return;
        const summarized = recent.map(summarize);
        set((state) => ({ events: [...summarized, ...state.events].slice(-maxEvents) }));
      })
      .catch(() => {
        // A daemon that isn't reachable yet is fine; the socket connect
        // below will retry and surface `status`.
      });

    connect(get);
  },

  stop: () => {
    stopped = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    socket?.close();
    socket = null;
    set({ started: false, status: 'closed' });
  },

  // restart drops every buffered event and reconnects against whatever
  // workspace is now selected. Without the flush, a page subscribing by
  // run or flow id would react to the previous workspace's events, whose
  // ids can collide across workspaces.
  restart: () => {
    const wasStarted = get().started;
    get().stop();
    set({ events: [], unreadCount: 0 });
    if (wasStarted) get().start();
  },
}));

function connect(get: () => EventsState) {
  if (stopped) return;
  useEvents.setState({ status: 'connecting' });
  let ws: WebSocket;
  try {
    ws = new WebSocket(wsURL());
  } catch {
    scheduleReconnect(get);
    return;
  }
  socket = ws;

  ws.onopen = () => {
    reconnectDelayMs = 1000;
    useEvents.setState({ status: 'open' });
  };
  ws.onmessage = (msg) => {
    try {
      const ev = JSON.parse(msg.data as string) as Event;
      useEvents.getState()._append(summarize(ev));
    } catch {
      // malformed frame; ignore
    }
  };
  ws.onclose = () => {
    useEvents.setState({ status: 'closed' });
    scheduleReconnect(get);
  };
  ws.onerror = () => {
    ws.close();
  };
}

function scheduleReconnect(get: () => EventsState) {
  if (stopped) return;
  // One failed connect is ordinary -- a daemon restarting, a laptop
  // waking. A second means the socket is not coming back on its own, and
  // the interesting question becomes *why*: the daemon is gone, or it was
  // replaced by a build that no longer serves this tab's UI. Asking here,
  // rather than on a timer, keeps the "no polling" rule: this fires only
  // while reconnection is already failing, and stops as soon as it works.
  if (reconnectDelayMs > 1000) {
    void useDaemon.getState().probe();
  }
  if (reconnectTimer) clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(() => {
    if (!stopped) connect(get);
  }, reconnectDelayMs);
  reconnectDelayMs = Math.min(reconnectDelayMs * 2, maxReconnectDelayMs);
}
