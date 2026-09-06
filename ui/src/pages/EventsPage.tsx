import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { EmptyState } from '../components/EmptyState';
import { Timestamp } from '../components/Timestamp';
import { VirtualList } from '../components/VirtualList';
import { useEvents } from '../state/events';
import type { StoredEvent } from '../state/events';
import type { EventType } from '../api/types';

const allTypes: EventType[] = [
  'run.started',
  'run.step',
  'run.finished',
  'catalog.changed',
  'service.sync_failed',
  'memory.created',
  'memory.changed',
  'flow.changed',
];

const typeColor: Record<string, string> = {
  'run.started': 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  'run.step': 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  'run.finished': 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  'catalog.changed': 'bg-violet-100 text-violet-800 dark:bg-violet-900/40 dark:text-violet-300',
  'service.sync_failed': 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300',
  'memory.created': 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300',
  'memory.changed': 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300',
  'flow.changed': 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
};

function TypePill({ type }: { type: string }) {
  return (
    <span className={`inline-block shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium ${typeColor[type] || 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300'}`}>
      {type}
    </span>
  );
}

function IdLinks({ ids }: { ids: StoredEvent['ids'] }) {
  const links: Array<{ to: string; label: string }> = [];
  if (ids.run_id) links.push({ to: `/ui/runs/${encodeURIComponent(ids.run_id)}`, label: `run:${ids.run_id}` });
  if (ids.flow_id) links.push({ to: `/ui/flows/${encodeURIComponent(ids.flow_id)}`, label: `flow:${ids.flow_id}` });
  if (ids.memory_id) links.push({ to: `/ui/memories/${encodeURIComponent(ids.memory_id)}`, label: `memory:${ids.memory_id}` });
  if (ids.service) links.push({ to: `/ui/services/${encodeURIComponent(ids.service)}`, label: `service:${ids.service}` });
  if (links.length === 0) return null;
  return (
    <span className="flex shrink-0 gap-2">
      {links.map((l) => (
        <Link key={l.to} to={l.to} className="text-xs text-sky-700 underline dark:text-sky-400">
          {l.label}
        </Link>
      ))}
    </span>
  );
}

function Row({ event }: { event: StoredEvent }) {
  return (
    <div className="flex items-center gap-3 border-b border-slate-100 px-3 py-2 text-sm dark:border-slate-900">
      <span className="w-16 shrink-0 text-xs text-slate-400">
        <Timestamp value={event.time} />
      </span>
      <TypePill type={event.type} />
      <span className="flex-1 truncate">{event.summary}</span>
      <IdLinks ids={event.ids} />
    </div>
  );
}

// Live feed off the events store (capped at 500, summaries only -- see
// src/state/events.ts). Pausing freezes the *displayed* list; the store
// keeps appending underneath so nothing is lost, and resuming flushes
// everything that arrived while paused in one go.
export default function EventsPage() {
  const liveEvents = useEvents((s) => s.events);
  const status = useEvents((s) => s.status);
  const markRead = useEvents((s) => s.markRead);

  const [paused, setPaused] = useState(false);
  const [frozen, setFrozen] = useState<StoredEvent[] | null>(null);
  const [activeTypes, setActiveTypes] = useState<Set<EventType>>(new Set());

  useEffect(() => {
    markRead();
  }, [markRead]);

  useEffect(() => {
    if (paused) {
      setFrozen((f) => f ?? liveEvents);
    } else {
      setFrozen(null);
    }
  }, [paused, liveEvents]);

  const displayEvents = paused && frozen ? frozen : liveEvents;
  const bufferedCount = paused && frozen ? Math.max(0, liveEvents.length - frozen.length) : 0;

  const toggleType = (t: EventType) => {
    setActiveTypes((prev) => {
      const next = new Set(prev);
      if (next.has(t)) next.delete(t);
      else next.add(t);
      return next;
    });
  };

  const filtered = useMemo(() => {
    const base = activeTypes.size === 0 ? displayEvents : displayEvents.filter((e) => activeTypes.has(e.type));
    return [...base].reverse();
  }, [displayEvents, activeTypes]);

  return (
    <div className="flex h-full flex-col p-4">
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold">Events</h1>
        <span className="text-xs text-slate-400">stream: {status}</span>
        <button
          type="button"
          onClick={() => setPaused((p) => !p)}
          className="rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700"
        >
          {paused ? `Resume${bufferedCount > 0 ? ` (${bufferedCount} buffered)` : ''}` : 'Pause'}
        </button>
      </div>

      <div className="mb-3 flex flex-wrap gap-1.5">
        <button
          type="button"
          onClick={() => setActiveTypes(new Set())}
          className={`rounded-full px-2 py-0.5 text-xs ${
            activeTypes.size === 0 ? 'bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900' : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300'
          }`}
        >
          All
        </button>
        {allTypes.map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => toggleType(t)}
            className={`rounded-full px-2 py-0.5 text-xs ${
              activeTypes.has(t) ? 'bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900' : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300'
            }`}
          >
            {t}
          </button>
        ))}
      </div>

      {filtered.length === 0 ? (
        <EmptyState title="No events yet" hint="Engine events (runs, catalog changes, memory writes) will stream here live." />
      ) : (
        <div className="flex flex-1 flex-col overflow-hidden rounded border border-slate-200 dark:border-slate-800">
          <VirtualList
            items={filtered}
            itemHeight={36}
            height={Math.min(700, Math.max(200, filtered.length * 36))}
            renderItem={(event) => <Row event={event} />}
          />
        </div>
      )}
    </div>
  );
}
