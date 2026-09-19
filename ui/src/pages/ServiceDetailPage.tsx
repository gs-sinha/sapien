import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { docs, environments, operations, services } from '../api/client';
import { Collapsible } from '../components/Collapsible';
import { DocViewer } from '../components/docs/DocViewer';
import { EmptyState } from '../components/EmptyState';
import { KeyValue } from '../components/KeyValue';
import { StatusPill } from '../components/StatusPill';
import { Table } from '../components/Table';
import { useAsync } from '../lib/useAsync';
import { subscribe } from '../state/events';
import { pushToast } from '../state/toast';
import { BindingPanel } from './services/BindingPanel';
import type { Doc, Environment, Operation, Service } from '../api/types';

interface DetailData {
  service: Service;
  ops: Operation[];
  envs: Environment[];
  docList: Doc[];
}

async function load(name: string): Promise<DetailData> {
  const [service, ops, envs, docList] = await Promise.all([
    services.get(name),
    operations.search({ service: name }) as Promise<Operation[]>,
    environments.list(),
    docs.list({ service: name }) as Promise<Doc[]>,
  ]);
  return { service, ops, envs, docList };
}

function matchesFilter(op: Operation, filter: string): boolean {
  if (!filter) return true;
  const f = filter.toLowerCase();
  return (
    op.id.toLowerCase().includes(f) ||
    (op.http?.path || '').toLowerCase().includes(f) ||
    (op.http?.method || '').toLowerCase().includes(f) ||
    (op.summary || '').toLowerCase().includes(f)
  );
}

export default function ServiceDetailPage() {
  const { name = '' } = useParams();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const { data, error, loading, reload } = useAsync(() => load(name), [name]);

  const [opFilter, setOpFilter] = useState('');
  const [syncing, setSyncing] = useState(false);
  const [removeConfirm, setRemoveConfirm] = useState('');
  const [removing, setRemoving] = useState(false);
  const [showRemove, setShowRemove] = useState(false);

  useEffect(
    () =>
      subscribe('catalog.changed', (e) => {
        if (!e.ids.service || e.ids.service === name) reload();
      }),
    [name, reload],
  );
  useEffect(
    () =>
      subscribe('service.sync_failed', (e) => {
        if (e.ids.service === name) pushToast('error', e.summary);
      }),
    [name],
  );

  const openDocPath = params.get('doc');
  const openDocSection = params.get('section');

  const closeDoc = () => {
    const next = new URLSearchParams(params);
    next.delete('doc');
    next.delete('section');
    setParams(next, { replace: true });
  };
  const openDoc = (path: string, section?: string) => {
    const next = new URLSearchParams(params);
    next.set('doc', path);
    if (section) next.set('section', section);
    else next.delete('section');
    setParams(next);
  };

  if (loading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="p-4 text-sm text-red-600">{error.message}</div>;
  if (!data) return null;
  const { service, ops, envs, docList } = data;

  const filteredOps = ops.filter((o) => matchesFilter(o, opFilter));
  const declaredEnvNames = Object.keys(service.environments || {});
  const workspaceEnvNames = new Set(envs.map((e) => e.name));
  const missingEnvs = declaredEnvNames.filter((n) => !workspaceEnvNames.has(n));

  const sync = async () => {
    setSyncing(true);
    try {
      await services.sync(name);
      pushToast('success', `Sync started for ${name}.`);
      reload();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Sync failed.');
    } finally {
      setSyncing(false);
    }
  };

  const remove = async () => {
    setRemoving(true);
    try {
      await services.remove(name);
      pushToast('success', `${name} removed.`);
      navigate('/ui/services');
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Remove failed.');
      setRemoving(false);
    }
  };

  return (
    <div className="p-4">
      <div className="mb-2 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-lg font-semibold">{service.name}</h1>
          <StatusPill status={service.status} />
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={sync}
            disabled={syncing}
            className="rounded border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-50 dark:border-slate-700"
          >
            {syncing ? 'Syncing…' : 'Sync'}
          </button>
          <button
            type="button"
            onClick={() => setShowRemove((v) => !v)}
            className="rounded border border-red-300 px-3 py-1.5 text-sm text-red-700 dark:border-red-900 dark:text-red-400"
          >
            Remove
          </button>
        </div>
      </div>
      <p className="mb-3 text-sm text-slate-500">{service.description}</p>

      {showRemove && (
        <div className="mb-4 rounded border border-red-300 bg-red-50 p-3 text-sm dark:border-red-900 dark:bg-red-950">
          <p className="mb-2 text-red-800 dark:text-red-300">
            This removes <span className="font-mono">{name}</span> from the workspace (its catalog, docs, and schemas). Type the
            service name to confirm.
          </p>
          <div className="flex items-center gap-2">
            <input
              value={removeConfirm}
              onChange={(e) => setRemoveConfirm(e.target.value)}
              placeholder={name}
              className="w-56 rounded border border-red-300 bg-white px-2 py-1 text-sm dark:border-red-800 dark:bg-slate-900"
            />
            <button
              type="button"
              disabled={removeConfirm !== name || removing}
              onClick={remove}
              className="rounded border border-red-400 bg-red-600 px-3 py-1 text-sm text-white disabled:opacity-40"
            >
              {removing ? 'Removing…' : 'Confirm remove'}
            </button>
          </div>
        </div>
      )}

      <BindingPanel service={service} onChanged={reload} />

      <KeyValue
        pairs={[
          ['owners', (service.owners || []).join(', ') || '-'],
          ['concepts', (service.concepts || []).join(', ') || '-'],
          [
            'source',
            `${service.source.type}${service.source.path ? ` ${service.source.path}` : ''}${service.source.url ? ` ${service.source.url}` : ''}`,
          ],
          ['package dir', service.package_dir],
          ['operations', service.operation_count],
        ]}
      />

      {service.error && (
        <div className="mt-3 rounded border border-red-300 bg-red-50 p-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
          {service.error}
        </div>
      )}

      <h2 className="mb-1 mt-5 text-sm font-semibold">Environments</h2>
      {declaredEnvNames.length === 0 ? (
        <p className="text-sm text-slate-400">This service declares no environment hints.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {declaredEnvNames.map((n) => (
            <li key={n} className="flex items-center gap-2">
              <span className={workspaceEnvNames.has(n) ? 'text-emerald-600' : 'text-amber-600'}>{workspaceEnvNames.has(n) ? '✓' : '✗'}</span>
              <span className="font-mono text-xs">{n}</span>
              <span className="text-slate-400">{service.environments?.[n]?.base_url}</span>
              {!workspaceEnvNames.has(n) && <span className="text-xs text-amber-600 dark:text-amber-400">not defined in this workspace</span>}
            </li>
          ))}
        </ul>
      )}
      {missingEnvs.length > 0 && (
        <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
          Run <code className="rounded bg-amber-100 px-1 dark:bg-amber-950">sapien env scaffold</code> to create environments/&lt;name&gt;.yaml
          from these services&apos; service.yaml hints, then fill in auth.
        </p>
      )}

      {(service.warnings?.length || 0) > 0 && (
        <Collapsible storageKey="service.warnings" title="Warnings" count={service.warnings!.length}>
          <ul className="space-y-1 text-sm text-amber-700 dark:text-amber-400">
            {service.warnings!.map((w, i) => (
              <li key={i}>
                <span className="font-mono text-xs">{w.code}</span> {w.message}
                {w.source && (
                  <span className="ml-1 text-xs text-slate-400">
                    ({w.source.file}
                    {w.source.line ? `:${w.source.line}` : ''})
                  </span>
                )}
              </li>
            ))}
          </ul>
        </Collapsible>
      )}

      {(service.accepted_warnings?.length || 0) > 0 && (
        <Collapsible storageKey="service.acceptedWarnings" title="Accepted warnings" count={service.accepted_warnings!.length}>
          <ul className="space-y-1 text-sm text-slate-500">
            {service.accepted_warnings!.map((w, i) => (
              <li key={i}>
                <span className="font-mono text-xs">{w.code}</span> {w.message} &mdash; {w.reason}
              </li>
            ))}
          </ul>
        </Collapsible>
      )}

      <h2 className="mb-2 mt-5 text-sm font-semibold">Docs ({docList.length})</h2>
      {docList.length === 0 ? (
        <p className="text-sm text-slate-400">No docs for this service.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {docList.map((d) => (
            <li key={d.id}>
              <button type="button" onClick={() => openDoc(d.path)} className="text-sky-700 underline dark:text-sky-400">
                {d.title}
              </button>{' '}
              <span className="font-mono text-xs text-slate-400">{d.path}</span>
            </li>
          ))}
        </ul>
      )}

      <div className="mb-2 mt-5 flex items-center justify-between">
        <h2 className="text-sm font-semibold">Operations ({filteredOps.length}{opFilter ? ` of ${ops.length}` : ''})</h2>
        <input
          value={opFilter}
          onChange={(e) => setOpFilter(e.target.value)}
          placeholder="filter by id, method, path, or summary"
          className="w-72 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        />
      </div>
      {filteredOps.length === 0 ? (
        <EmptyState title="No operations match" />
      ) : (
        <Table<Operation>
          rowKey={(o) => o.id}
          columns={[
            { key: 'method', header: 'Method', render: (o) => o.http?.method || '-' },
            { key: 'path', header: 'Path', render: (o) => <span className="font-mono text-xs">{o.http?.path || '-'}</span> },
            {
              key: 'id',
              header: 'ID',
              render: (o) => (
                <Link to={`/ui/operations/${encodeURIComponent(o.id)}`} className="text-sky-700 underline dark:text-sky-400">
                  {o.id}
                </Link>
              ),
            },
            { key: 'summary', header: 'Summary', render: (o) => o.summary || '-' },
          ]}
          rows={filteredOps}
        />
      )}

      {openDocPath && (
        <DocViewer service={name} path={openDocPath} section={openDocSection} operations={ops} onClose={closeDoc} />
      )}
    </div>
  );
}
