import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { memories } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { Table } from '../components/Table';
import { Timestamp } from '../components/Timestamp';
import { useAsync } from '../lib/useAsync';
import { subscribe } from '../state/events';
import type { Memory, MemoryScope, MemoryType } from '../api/types';

const memoryTypes: MemoryType[] = ['note', 'semantic', 'behavioral', 'testing', 'invariant', 'environment', 'gotcha'];
const memoryScopes: MemoryScope[] = ['personal', 'workspace', 'service', 'flow'];

export default function MemoriesPage() {
  const [q, setQ] = useState('');
  const [submitted, setSubmitted] = useState('');
  const [type, setType] = useState<MemoryType | ''>('');
  const [scope, setScope] = useState<MemoryScope | ''>('');
  const [service, setService] = useState('');

  const filter = { type: type || undefined, scope: scope || undefined, service: service || undefined };

  const { data, error, loading, reload } = useAsync(
    () =>
      submitted
        ? memories.search({ q: submitted, ...filter }).then((res) => res.map((s) => s.memory))
        : memories.list(filter),
    [submitted, type, scope, service],
  );

  useEffect(() => {
    const un1 = subscribe('memory.created', reload);
    const un2 = subscribe('memory.changed', reload);
    return () => {
      un1();
      un2();
    };
  }, [reload]);

  return (
    <div className="p-4">
      <h1 className="mb-4 text-lg font-semibold">Memories</h1>
      <form
        className="mb-3 flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setSubmitted(q);
        }}
      >
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="search memories"
          className="w-64 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        />
        <select
          value={type}
          onChange={(e) => setType(e.target.value as MemoryType | '')}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        >
          <option value="">any type</option>
          {memoryTypes.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
        <select
          value={scope}
          onChange={(e) => setScope(e.target.value as MemoryScope | '')}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        >
          <option value="">any scope</option>
          {memoryScopes.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <input
          value={service}
          onChange={(e) => setService(e.target.value)}
          placeholder="service"
          className="w-32 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        />
        <button type="submit" className="rounded border border-slate-300 px-3 py-1 text-sm dark:border-slate-700">
          Search
        </button>
      </form>

      {loading && <div className="text-sm text-slate-400">Loading…</div>}
      {error && <div className="text-sm text-red-600">{error.message}</div>}
      {!loading && !error && (!data || data.length === 0) && (
        <EmptyState title="No memories" hint="Memories captured by agents or people during calls and runs show up here." />
      )}
      {!loading && !error && data && data.length > 0 && (
        <Table<Memory>
          rowKey={(m) => m.id}
          columns={[
            {
              key: 'id',
              header: 'ID',
              render: (m) => (
                <Link to={`/ui/memories/${encodeURIComponent(m.id)}`} className="font-mono text-xs text-sky-700 underline dark:text-sky-400">
                  {m.id}
                </Link>
              ),
            },
            { key: 'type', header: 'Type', render: (m) => m.type },
            { key: 'scope', header: 'Scope', render: (m) => m.scope },
            {
              key: 'subject',
              header: 'Subject',
              render: (m) => m.subject.operation || m.subject.service || m.subject.flow || m.subject.concept || '-',
            },
            { key: 'text', header: 'First line', render: (m) => <span className="line-clamp-1 max-w-md text-slate-500">{(m.text.split('\n')[0] || '').slice(0, 140)}</span> },
            { key: 'status', header: 'Status', render: (m) => m.status },
            { key: 'updated', header: 'Updated', render: (m) => <Timestamp value={m.updated} /> },
          ]}
          rows={data}
        />
      )}
    </div>
  );
}
