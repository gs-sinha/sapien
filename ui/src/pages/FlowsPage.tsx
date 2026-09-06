import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { flows } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { Table } from '../components/Table';
import { Timestamp } from '../components/Timestamp';
import { useAsync } from '../lib/useAsync';
import { subscribe } from '../state/events';
import type { FlowSummary } from '../api/types';

function matches(flow: FlowSummary, needle: string): boolean {
  if (!needle) return true;
  const haystack = [flow.id, flow.name, ...(flow.tags || []), ...(flow.operations || [])].join(' ').toLowerCase();
  return haystack.includes(needle);
}

function OperationsCell({ operations }: { operations?: string[] }) {
  if (!operations || operations.length === 0) return <span>-</span>;
  const shown = operations.slice(0, 3);
  const rest = operations.length - shown.length;
  return (
    <span className="font-mono text-xs">
      {shown.join(', ')}
      {rest > 0 ? ` +${rest} more` : ''}
    </span>
  );
}

export default function FlowsPage() {
  const { data, error, loading, reload } = useAsync(() => flows.list(), []);
  const [filter, setFilter] = useState('');

  useEffect(() => subscribe('flow.changed', reload), [reload]);

  const filtered = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    return (data || []).filter((f) => matches(f, needle));
  }, [data, filter]);

  return (
    <div className="p-4">
      <h1 className="mb-4 text-lg font-semibold">Flows</h1>
      <input
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder="Filter by id, name, tag, or operation"
        className="mb-3 w-full max-w-md rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      {loading && <div className="text-sm text-slate-400">Loading…</div>}
      {error && <div className="text-sm text-red-600">{error.message}</div>}
      {!loading && !error && (!data || data.length === 0) && (
        <EmptyState title="No flows yet" hint="Flows created by agents or saved from a run will show up here." />
      )}
      {!loading && !error && data && data.length > 0 && filtered.length === 0 && (
        <div className="text-sm text-slate-400">No flows match &quot;{filter}&quot;.</div>
      )}
      {!loading && !error && filtered.length > 0 && (
        <Table<FlowSummary>
          rowKey={(f) => f.id}
          columns={[
            {
              key: 'id',
              header: 'ID',
              render: (f) => (
                <Link to={`/ui/flows/${encodeURIComponent(f.id)}`} className="text-sky-700 underline dark:text-sky-400">
                  {f.id}
                </Link>
              ),
            },
            { key: 'name', header: 'Name', render: (f) => f.name || '-' },
            { key: 'steps', header: 'Steps', render: (f) => f.step_count },
            { key: 'operations', header: 'Operations', render: (f) => <OperationsCell operations={f.operations} /> },
            { key: 'tags', header: 'Tags', render: (f) => (f.tags || []).join(', ') || '-' },
            { key: 'updated', header: 'Updated', render: (f) => <Timestamp value={f.updated} /> },
          ]}
          rows={filtered}
        />
      )}
    </div>
  );
}
