import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { runs } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { StatusPill } from '../components/StatusPill';
import { Timestamp } from '../components/Timestamp';
import { VirtualList } from '../components/VirtualList';
import { useAsync } from '../lib/useAsync';
import { subscribe } from '../state/events';
import type { Run, RunStatus } from '../api/types';

const gridCols = '160px 160px 110px 120px 160px 90px 90px';

const allStatuses: RunStatus[] = ['queued', 'running', 'passed', 'failed', 'errored', 'cancelled'];

function Row({ run }: { run: Run }) {
  return (
    <Link
      to={`/ui/runs/${encodeURIComponent(run.id)}`}
      className="grid items-center gap-2 border-b border-slate-100 px-3 py-2 text-sm hover:bg-slate-50 dark:border-slate-900 dark:hover:bg-slate-900"
      style={{ gridTemplateColumns: gridCols }}
    >
      <span>
        <StatusPill status={run.status} />
      </span>
      <span className="truncate">{run.flow_id || 'ad hoc call'}</span>
      <span className="truncate">{run.environment}</span>
      <span>
        <Timestamp value={run.started} />
      </span>
      <span>{run.duration_ms !== undefined ? `${run.duration_ms} ms` : '-'}</span>
      <span>
        {run.summary.steps_passed}/{run.summary.steps_total}
      </span>
      <span className="truncate font-mono text-xs text-slate-400">{run.id}</span>
    </Link>
  );
}

export default function RunsPage() {
  const { data, error, loading, reload } = useAsync(() => runs.list({ limit: 500 }), []);
  const [status, setStatus] = useState('');
  const [flow, setFlow] = useState('');

  useEffect(() => {
    const un1 = subscribe('run.started', reload);
    const un2 = subscribe('run.finished', reload);
    return () => {
      un1();
      un2();
    };
  }, [reload]);

  const flowOptions = useMemo(() => {
    const ids = new Set<string>();
    for (const r of data || []) if (r.flow_id) ids.add(r.flow_id);
    return Array.from(ids).sort();
  }, [data]);

  const filtered = useMemo(() => {
    return (data || []).filter((r) => (status ? r.status === status : true) && (flow ? r.flow_id === flow : true));
  }, [data, status, flow]);

  return (
    <div className="flex h-full flex-col p-4">
      <h1 className="mb-4 text-lg font-semibold">Runs</h1>
      <div className="mb-3 flex flex-wrap gap-2">
        <select
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        >
          <option value="">All statuses</option>
          {allStatuses.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <select
          value={flow}
          onChange={(e) => setFlow(e.target.value)}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        >
          <option value="">All flows</option>
          {flowOptions.map((f) => (
            <option key={f} value={f}>
              {f}
            </option>
          ))}
        </select>
      </div>
      {loading && <div className="text-sm text-slate-400">Loading…</div>}
      {error && <div className="text-sm text-red-600">{error.message}</div>}
      {!loading && !error && (!data || data.length === 0) && (
        <EmptyState title="No runs yet" hint="Calls and flow runs (from the CLI, MCP, or this UI) show up here." />
      )}
      {!loading && !error && data && data.length > 0 && filtered.length === 0 && (
        <div className="text-sm text-slate-400">No runs match the current filters.</div>
      )}
      {!loading && !error && filtered.length > 0 && (
        <div className="flex flex-1 flex-col overflow-hidden rounded border border-slate-200 dark:border-slate-800">
          <div
            className="grid items-center gap-2 border-b border-slate-200 bg-slate-50 px-3 py-2 text-xs font-medium uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:bg-slate-900"
            style={{ gridTemplateColumns: gridCols }}
          >
            <span>Status</span>
            <span>Flow</span>
            <span>Environment</span>
            <span>Started</span>
            <span>Duration</span>
            <span>Steps</span>
            <span>ID</span>
          </div>
          <VirtualList
            items={filtered}
            itemHeight={40}
            height={Math.min(640, Math.max(200, filtered.length * 40))}
            renderItem={(run) => <Row run={run} />}
          />
        </div>
      )}
    </div>
  );
}
