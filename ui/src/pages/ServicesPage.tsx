import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { repo, services } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { StatusPill } from '../components/StatusPill';
import { Table } from '../components/Table';
import { Timestamp } from '../components/Timestamp';
import { useAsync } from '../lib/useAsync';
import { AddServiceForm } from './services/AddServiceForm';
import { driftSuffix } from './services/BindingPanel';
import { subscribe } from '../state/events';
import { repoClause, useRepo } from '../state/repo';
import { pushToast } from '../state/toast';
import type { Service } from '../api/types';

function sourceLabel(s: Service): string {
  if (s.source.type === 'git') return s.source.url || 'git';
  return s.source.path || 'local';
}

// Which source this machine reads the service from: "local · <branch>" for
// a checkout, "team · <ref>" for the committed git source. Falls back to the
// source kind for a daemon that does not report bindings yet.
export function readsFromLabel(s: Service): string {
  const b = s.binding;
  if (b?.mode === 'local') {
    const base = b.local?.branch ? `local · ${b.local.branch}` : 'local';
    const drift = b.local ? driftSuffix(b.local) : '';
    return drift ? `${base} · ${drift}` : base;
  }
  if (b?.mode === 'team') return b.team?.ref ? `team · ${b.team.ref}` : 'team';
  return s.source.type === 'git' ? 'team' : 'local';
}

function ReadsFromPill({ service }: { service: Service }) {
  const writable = service.binding ? service.binding.writable : service.source.type !== 'git';
  return (
    <span
      title={writable ? 'Read from a local checkout: service-scoped knowledge is writable here' : 'Read from the team source: read-only here'}
      className="inline-block rounded-full bg-slate-100 px-2 py-0.5 font-mono text-xs text-slate-700 dark:bg-slate-800 dark:text-slate-300"
    >
      {readsFromLabel(service)}
    </span>
  );
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
      let message = 'Sync started for every service.';
      try {
        // The workspace folder is itself the team's git repo; "Sync all"
        // also syncs it (fetch, then a fast-forward pull when it's safe).
        const status = await repo.sync();
        useRepo.getState().setStatus(status);
        if (status.in_git) message = `Sync started for every service; ${repoClause(status)}.`;
      } catch {
        // Best effort: the service sync already succeeded, so the repo
        // clause is a bonus, not a reason to report failure.
      }
      pushToast('success', message);
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
            { key: 'reads', header: 'Reads from', render: (s) => <ReadsFromPill service={s} /> },
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
