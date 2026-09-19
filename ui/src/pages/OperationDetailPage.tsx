import { Link, useParams } from 'react-router-dom';
import { buildContext, docs, examples, memories, operations } from '../api/client';
import { Collapsible } from '../components/Collapsible';
import { Markdown } from '../components/docs/Markdown';
import { EmptyState } from '../components/EmptyState';
import { KeyValue } from '../components/KeyValue';
import { StatusPill } from '../components/StatusPill';
import { Table } from '../components/Table';
import { useAsync } from '../lib/useAsync';
import type { Field, Memory, Operation, Param, SavedExample, Schema } from '../api/types';

interface OpDoc {
  service: string;
  path: string;
  heading: string;
  body: string;
  // true when the full-text fetch (GET /v1/docs/{service}/{path}) failed
  // (e.g. "doc contract not found" for a contract-embedded doc whose path
  // no longer resolves) and `body` is instead the context bundle's own
  // token-budget-truncated snippet.
  unavailable?: boolean;
}

// The catalog has no "docs referencing operation X" HTTP route directly, but
// the context builder does this lookup internally (internal/retrieval
// builder.buildDocs -> catalog.DocsReferencing) whenever ContextRequest.Operations
// names the operation. Using it here just to *discover* which (service,
// path, heading) sections mention op.id, then re-fetching each section in
// full through GET /v1/docs/{service}/{path} (with `section`) avoids
// showing a context bundle's token-budget-truncated snippet -- except when
// that fetch 404s (a doc path that doesn't resolve on its own, e.g. a
// "contract#tag:Name" fragment the docs route can't find), in which case the
// bundle's own snippet is shown instead of an error line.
async function loadDocsForOperation(op: Operation): Promise<OpDoc[]> {
  const bundle = await buildContext({ intent: op.id, operations: [op.id], budget_tokens: 4000 });
  const uniqueKeys = new Map<string, { service: string; path: string; heading: string; snippet: string }>();
  // ContextBundle.docs has no `omitempty` on the Go side, so it's already
  // normalized to [] by client.ts's normalizeNulls in production -- this
  // guard is defense-in-depth (matches operations/IntentBundle.tsx doing
  // the same for every ContextBundle tier) in case that ever isn't true.
  for (const d of bundle.docs ?? []) {
    uniqueKeys.set(`${d.service}|${d.path}|${d.heading}`, { service: d.service, path: d.path, heading: d.heading, snippet: d.body });
  }
  return Promise.all(
    Array.from(uniqueKeys.values()).map(async ({ snippet, ...item }) => {
      try {
        const doc = await docs.get(item.service, item.path, item.heading);
        return { ...item, body: doc.sections?.[0]?.body ?? snippet };
      } catch {
        return { ...item, body: snippet, unavailable: true };
      }
    }),
  );
}

interface DetailData {
  op: Operation;
  fields: Field[];
  exampleList: SavedExample[];
  memoryList: Memory[];
  docSections: OpDoc[];
}

async function load(id: string): Promise<DetailData> {
  const op = await operations.get(id);
  const [fields, exampleList, memoryList, docSections] = await Promise.all([
    operations.fields(id),
    examples.list({ operation: id }),
    memories.list({ op: id }),
    loadDocsForOperation(op),
  ]);
  return { op, fields, exampleList, memoryList, docSections };
}

function schemaType(schema?: Schema): string {
  if (!schema) return '-';
  return schema.format ? `${schema.kind} (${schema.format})` : schema.kind;
}

export default function OperationDetailPage() {
  const { id = '' } = useParams();
  const { data, error, loading } = useAsync(() => load(id), [id]);

  if (loading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="p-4 text-sm text-red-600">{error.message}</div>;
  if (!data) return null;
  const { op, fields, exampleList, memoryList, docSections } = data;

  const requestFields = fields.filter((f) => f.path.startsWith('request.body'));
  const responseFields = fields.filter((f) => f.path.startsWith('response'));

  return (
    <div className="p-4">
      <div className="mb-1 flex flex-wrap items-center gap-2">
        <h1 className="text-lg font-semibold">{op.id}</h1>
        {op.deprecated && <StatusPill status="deprecated" />}
        {(op.tags || []).map((t) => (
          <span key={t} className="rounded bg-slate-100 px-1.5 py-0.5 text-[11px] text-slate-500 dark:bg-slate-800 dark:text-slate-400">
            {t}
          </span>
        ))}
      </div>
      <p className="mb-3 text-sm text-slate-500">{op.summary}</p>

      <div className="mb-3 flex items-center gap-3">
        <KeyValue
          pairs={[
            ['method', op.http?.method || '-'],
            ['path', <span className="font-mono text-xs">{op.http?.path || '-'}</span>],
            [
              'service',
              <Link to={`/ui/services/${encodeURIComponent(op.service_id)}`} className="text-sky-700 underline dark:text-sky-400">
                {op.service_id}
              </Link>,
            ],
          ]}
        />
        <Link
          to={`/ui/try/${encodeURIComponent(op.id)}`}
          className="ml-auto shrink-0 rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-800 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white"
        >
          Try it
        </Link>
      </div>

      {op.description && <p className="mb-4 max-w-2xl text-sm text-slate-600 dark:text-slate-400">{op.description}</p>}

      <h2 className="mb-2 mt-4 text-sm font-semibold">Parameters ({(op.params || []).length})</h2>
      {(op.params || []).length === 0 ? (
        <p className="text-sm text-slate-400">This operation takes no parameters.</p>
      ) : (
        <Table<Param>
          rowKey={(p) => `${p.in}:${p.name}`}
          columns={[
            { key: 'name', header: 'Name', render: (p) => <span className="font-mono text-xs">{p.name}</span> },
            { key: 'in', header: 'In', render: (p) => p.in },
            { key: 'type', header: 'Type', render: (p) => schemaType(p.schema) },
            { key: 'required', header: 'Required', render: (p) => (p.required ? 'yes' : '') },
            { key: 'description', header: 'Description', render: (p) => p.description || '-' },
          ]}
          rows={op.params || []}
        />
      )}

      <h2 className="mb-2 mt-5 text-sm font-semibold">
        Request body {op.request_body ? `(${op.request_body.content_type}${op.request_body.required ? ', required' : ''})` : ''}
      </h2>
      {!op.request_body ? (
        <p className="text-sm text-slate-400">This operation takes no request body.</p>
      ) : requestFields.length === 0 ? (
        <p className="text-sm text-slate-400">No fields recorded for this body.</p>
      ) : (
        <Table<Field>
          rowKey={(f) => f.path}
          columns={[
            { key: 'path', header: 'Path', render: (f) => <span className="font-mono text-xs">{f.path}</span> },
            { key: 'type', header: 'Type', render: (f) => f.type },
            { key: 'required', header: 'Required', render: (f) => (f.required ? 'yes' : '') },
            { key: 'description', header: 'Description', render: (f) => f.description || '-' },
          ]}
          rows={requestFields}
        />
      )}

      <h2 className="mb-2 mt-5 text-sm font-semibold">Response fields ({responseFields.length})</h2>
      {responseFields.length === 0 ? (
        <p className="text-sm text-slate-400">No response fields recorded.</p>
      ) : (
        <Table<Field>
          rowKey={(f) => f.path}
          columns={[
            { key: 'path', header: 'Path', render: (f) => <span className="font-mono text-xs">{f.path}</span> },
            { key: 'type', header: 'Type', render: (f) => f.type },
            { key: 'required', header: 'Required', render: (f) => (f.required ? 'yes' : '') },
            { key: 'description', header: 'Description', render: (f) => f.description || '-' },
          ]}
          rows={responseFields}
        />
      )}

      {docSections.length === 0 ? (
        <>
          <h2 className="mb-2 mt-5 text-sm font-semibold">Docs</h2>
          <p className="text-sm text-slate-400">No docs reference this operation.</p>
        </>
      ) : (
        <Collapsible
          storageKey="operation.docs"
          title="Docs"
          count={docSections.length}
          summary={docSections.map((d) => d.heading).join(', ')}
        >
          <div className="space-y-3">
            {docSections.map((d, i) => (
              <div key={i} className="rounded border border-slate-200 p-3 dark:border-slate-800">
                <div className="mb-1 flex items-center justify-between gap-2">
                  <span className="font-mono text-xs text-slate-400">
                    {d.service}/{d.path}#{d.heading}
                  </span>
                  <Link
                    to={`/ui/services/${encodeURIComponent(d.service)}?doc=${encodeURIComponent(d.path)}&section=${encodeURIComponent(d.heading)}`}
                    className="shrink-0 text-xs text-sky-700 underline dark:text-sky-400"
                  >
                    View full doc
                  </Link>
                </div>
                <Markdown text={d.body} />
                {d.unavailable && <p className="mt-1 text-xs italic text-slate-400">Full text unavailable &mdash; showing summary.</p>}
              </div>
            ))}
          </div>
        </Collapsible>
      )}

      <h2 className="mb-2 mt-5 text-sm font-semibold">Examples ({exampleList.length})</h2>
      {exampleList.length === 0 ? (
        <EmptyState title="No saved examples for this operation" />
      ) : (
        <ul className="space-y-1 text-sm">
          {exampleList.map((ex) => (
            <li key={ex.id} className="flex flex-wrap items-center gap-2">
              <Link to={`/ui/examples/${encodeURIComponent(ex.id)}`} className="text-sky-700 underline dark:text-sky-400">
                {ex.id}
              </Link>
              {ex.verified && (
                <span className="text-xs text-emerald-600">verified{ex.verified.env ? ` (${ex.verified.env})` : ''}</span>
              )}
              <span className="text-slate-500">{ex.description}</span>
              <Link
                to={`/ui/try/${encodeURIComponent(op.id)}?example=${encodeURIComponent(ex.id)}`}
                className="text-xs text-sky-700 underline dark:text-sky-400"
              >
                Try it
              </Link>
            </li>
          ))}
        </ul>
      )}

      <h2 className="mb-2 mt-5 text-sm font-semibold">Memories ({memoryList.length})</h2>
      {memoryList.length === 0 ? (
        <p className="text-sm text-slate-400">No memories about this operation yet.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {memoryList.map((m) => (
            <li key={m.id} className="text-slate-600 dark:text-slate-400">
              <span className="font-mono text-xs">{m.id}</span> ({m.type}) &mdash; {m.text.slice(0, 200)}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
