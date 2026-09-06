import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { examples } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { Table } from '../components/Table';
import { Timestamp } from '../components/Timestamp';
import { useAsync } from '../lib/useAsync';
import type { ExampleQueryParams, SavedExample } from '../api/types';

function FilterInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <input
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className="w-40 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
    />
  );
}

export default function ExamplesPage() {
  const [service, setService] = useState('');
  const [operation, setOperation] = useState('');
  const [tag, setTag] = useState('');
  const [text, setText] = useState('');

  const filter: ExampleQueryParams = { service: service || undefined, operation: operation || undefined, tag: tag || undefined, text: text || undefined };
  const { data, error, loading, reload } = useAsync(() => examples.list(filter), [service, operation, tag, text]);

  // No live event to refetch on (example writes don't emit an event type of
  // their own), so a window focus refetch keeps the list fresh after
  // saving an example from Try It in the same tab/another tab.
  useEffect(() => {
    window.addEventListener('focus', reload);
    return () => window.removeEventListener('focus', reload);
  }, [reload]);

  const hasFilters = !!(service || operation || tag || text);

  return (
    <div className="p-4">
      <h1 className="mb-4 text-lg font-semibold">Examples</h1>
      <div className="mb-3 flex flex-wrap gap-2">
        <FilterInput value={service} onChange={setService} placeholder="service" />
        <FilterInput value={operation} onChange={setOperation} placeholder="operation" />
        <FilterInput value={tag} onChange={setTag} placeholder="tag" />
        <FilterInput value={text} onChange={setText} placeholder="search text" />
      </div>

      {loading && <div className="text-sm text-slate-400">Loading…</div>}
      {error && <div className="text-sm text-red-600">{error.message}</div>}
      {!loading && !error && (!data || data.length === 0) && (
        <EmptyState
          title={hasFilters ? 'No examples match these filters' : 'No saved examples yet'}
          hint={hasFilters ? undefined : 'Save a working request from Try It (or `sapien call --save-example`) to build a library here.'}
        />
      )}
      {!loading && !error && data && data.length > 0 && (
        <Table<SavedExample>
          rowKey={(e) => e.id}
          columns={[
            {
              key: 'id',
              header: 'ID',
              render: (e) => (
                <Link to={`/ui/examples/${encodeURIComponent(e.id)}`} className="text-sky-700 underline dark:text-sky-400">
                  {e.id}
                </Link>
              ),
            },
            { key: 'operation', header: 'Operation', render: (e) => <span className="font-mono text-xs">{e.operation}</span> },
            { key: 'scope', header: 'Scope', render: (e) => e.scope },
            { key: 'verified', header: 'Verified env', render: (e) => (e.verified ? e.verified.env || 'yes' : 'draft') },
            { key: 'description', header: 'Description', render: (e) => e.description || '-' },
            { key: 'updated', header: 'Updated', render: (e) => <Timestamp value={e.updated} /> },
          ]}
          rows={data}
        />
      )}
    </div>
  );
}
