import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { services } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { StatusPill } from '../components/StatusPill';
import { Table } from '../components/Table';
import { Timestamp } from '../components/Timestamp';
import { useAsync } from '../lib/useAsync';
import { AddServiceForm } from './services/AddServiceForm';
import { subscribe } from '../state/events';
import { pushToast } from '../state/toast';
import type { Service } from '../api/types';

function sourceLabel(s: Service): string {
  if (s.source.type === 'git') return s.source.url || 'git';
  return s.source.path || 'local';
}

export default function ServicesPage() {
  const { data, error, loading, reload } = useAsync(() => services.list(), []);
  const [syncingAll, setSyncingAll] = useState(false);

  useEffect(() => subscribe('catalog.changed', reload), [reload]);
  useEffect(
    () =>
      subscribe('service.sync_failed', (e) => {
        pushToast('error', e.summary);
      }),
    [],
  );

  const syncAll = async () => {
    setSyncingAll(true);
    try {
      await services.sync();
      pushToast('success', 'Sync started for every service.');
      reload();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Sync failed.');
    } finally {
      setSyncingAll(false);
    }
  };

  return (
    <div className="p-4">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold">Services</h1>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={syncAll}
            disabled={syncingAll}
            className="rounded border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-50 dark:border-slate-700"
          >
            {syncingAll ? 'Syncing…' : 'Sync all'}
          </button>
          <AddServiceForm onAdded={reload} />
        </div>
      </div>

      {loading && <div className="text-sm text-slate-400">Loading…</div>}
      {error && <div className="text-sm text-red-600">{error.message}</div>}
      {!loading && !error && (!data || data.length === 0) && (
        <EmptyState title="No services registered" hint="Register a service above, with `sapien service add`, or the add_service MCP tool." />
      )}
      {!loading && !error && data && data.length > 0 && (
        <Table<Service>
          rowKey={(s) => s.id}
          columns={[
            {
              key: 'name',
              header: 'Name',
              render: (s) => (
                <Link to={`/ui/services/${encodeURIComponent(s.name)}`} className="text-sky-700 underline dark:text-sky-400">
                  {s.name}
                </Link>
              ),
            },
            { key: 'status', header: 'Status', render: (s) => <StatusPill status={s.status} /> },
            { key: 'ops', header: 'Operations', render: (s) => s.operation_count },
            {
              key: 'warnings',
              header: 'Unaccepted warnings',
              render: (s) => {
                const n = (s.warnings || []).length;
                return <span className={n > 0 ? 'font-medium text-amber-700 dark:text-amber-400' : ''}>{n}</span>;
              },
            },
            { key: 'accepted', header: 'Accepted warnings', render: (s) => (s.accepted_warnings || []).length },
            { key: 'indexed', header: 'Last indexed', render: (s) => <Timestamp value={s.last_indexed} /> },
            { key: 'source', header: 'Source', render: (s) => <span className="font-mono text-xs">{sourceLabel(s)}</span> },
          ]}
          rows={data}
        />
      )}
    </div>
  );
}
