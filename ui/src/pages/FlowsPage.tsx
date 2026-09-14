import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { flows } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { Table } from '../components/Table';
import { Timestamp } from '../components/Timestamp';
import { useAsync } from '../lib/useAsync';
import { subscribe } from '../state/events';
import { FLOW_TIERS, ShippedBadge, TIER_NAMES, tierLabel, tierOf } from './flows/tier';
import type { FlowOwnerKind, FlowSummary } from '../api/types';

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
  // Tier chips: every tier shown until one is toggled off; composes with the text filter.
  const [tiers, setTiers] = useState<Set<FlowOwnerKind>>(() => new Set(FLOW_TIERS));

  useEffect(() => subscribe('flow.changed', reload), [reload]);

  const toggleTier = (t: FlowOwnerKind) =>
    setTiers((prev) => {
      const next = new Set(prev);
      if (next.has(t)) next.delete(t);
      else next.add(t);
      return next;
    });

  const filtered = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    return (data || []).filter((f) => tiers.has(tierOf(f.owner_kind)) && matches(f, needle));
  }, [data, filter, tiers]);

  return (
    <div className="p-4">
      <h1 className="mb-4 text-lg font-semibold">Flows</h1>
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by id, name, tag, or operation"
          className="w-full max-w-md rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        />
        <div role="group" aria-label="Tier" className="flex items-center gap-1">
          {FLOW_TIERS.map((t) => {
            const on = tiers.has(t);
            return (
              <button
                key={t}
                type="button"
                aria-pressed={on}
                onClick={() => toggleTier(t)}
                className={`rounded-full border px-2 py-0.5 text-xs ${
                  on
                    ? 'border-sky-600 bg-sky-50 text-sky-800 dark:border-sky-500 dark:bg-sky-950 dark:text-sky-300'
                    : 'border-slate-300 text-slate-400 dark:border-slate-700 dark:text-slate-500'
                }`}
              >
                {TIER_NAMES[t]}
              </button>
            );
          })}
        </div>
      </div>
      {loading && <div className="text-sm text-slate-400">Loading…</div>}
      {error && <div className="text-sm text-red-600">{error.message}</div>}
      {!loading && !error && (!data || data.length === 0) && (
        <EmptyState title="No flows yet" hint="Flows created by agents or saved from a run will show up here." />
      )}
      {!loading && !error && data && data.length > 0 && filtered.length === 0 && (
        <div className="text-sm text-slate-400">{filter ? `No flows match "${filter}".` : 'No flows in the selected tiers.'}</div>
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
            {
              key: 'tier',
              header: 'Tier',
              render: (f) => (
                <span className="flex items-center gap-1.5">
                  <span className="font-mono text-xs">{tierLabel(f.owner_kind, f.owner_id)}</span>
                  {tierOf(f.owner_kind) === 'workspace' && <ShippedBadge shipped={f.shipped} />}
                </span>
              ),
            },
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
