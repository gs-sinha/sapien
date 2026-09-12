// Renders a POST /v1/context bundle in tiers (PLAN §14 / PRD §43): contract
// (operations), documentation, examples, memories, then flows and runs if
// the bundle carries any. Used by OperationsPage after the explicit
// "Build context" action, represented by ?intent= in the shareable URL.
import { Link } from 'react-router-dom';
import { buildContext } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import { timed } from './timing';

export function IntentBundle({ intent }: { intent: string }) {
  const { data, error, loading } = useAsync(() => timed(() => buildContext({ intent, budget_tokens: 6000 })), [intent]);

  if (loading) return <div className="text-sm text-slate-400">Building context…</div>;
  if (error) return <div className="text-sm text-red-600">{error.message}</div>;
  if (!data) return null;
  const raw = data.data;
  // Go marshals empty slices as null; treat every tier as an array.
  const bundle = {
    ...raw,
    operations: raw.operations ?? [],
    docs: raw.docs ?? [],
    examples: raw.examples ?? [],
    memories: raw.memories ?? [],
    flows: raw.flows ?? [],
    runs: raw.runs ?? [],
  };

  return (
    <div className="space-y-5">
      <p className="text-xs text-slate-400">
        {bundle.estimated_tokens} estimated tokens &middot; {data.latencyMs} ms
      </p>

      <div>
        <h2 className="mb-2 text-sm font-semibold">Operations ({bundle.operations.length})</h2>
        <div className="rounded border border-slate-200 dark:border-slate-800">
          {bundle.operations.map((o) => (
            <Link
              key={o.id}
              to={`/ui/operations/${encodeURIComponent(o.id)}`}
              className="flex items-center gap-3 border-b border-slate-100 px-3 py-2 text-sm last:border-b-0 hover:bg-slate-50 dark:border-slate-900 dark:hover:bg-slate-900"
            >
              <span className="w-16 shrink-0 text-xs font-medium text-slate-400">{o.method}</span>
              <span className="w-64 shrink-0 truncate font-mono text-xs">{o.id}</span>
              <span className="flex-1 truncate text-slate-500">{o.summary}</span>
              {o.score !== undefined && <span className="w-14 shrink-0 text-right text-xs text-slate-400">{o.score.toFixed(2)}</span>}
            </Link>
          ))}
          {bundle.operations.length === 0 && <div className="p-3 text-sm text-slate-400">No matching operations.</div>}
        </div>
      </div>

      {bundle.docs.length > 0 && (
        <div>
          <h2 className="mb-2 text-sm font-semibold">Docs ({bundle.docs.length})</h2>
          <ul className="space-y-2 text-sm">
            {bundle.docs.map((d, i) => (
              <li key={i} className="text-slate-600 dark:text-slate-400">
                <Link to={`/ui/services/${encodeURIComponent(d.service)}?doc=${encodeURIComponent(d.path)}&section=${encodeURIComponent(d.heading)}`} className="text-sky-700 underline dark:text-sky-400">
                  {d.service}/{d.path}#{d.heading}
                </Link>
                <div className="text-xs text-slate-400">{d.body.slice(0, 200)}</div>
              </li>
            ))}
          </ul>
        </div>
      )}

      {bundle.examples.length > 0 && (
        <div>
          <h2 className="mb-2 text-sm font-semibold">Examples ({bundle.examples.length})</h2>
          <ul className="space-y-1 text-sm">
            {bundle.examples.map((ex) => (
              <li key={ex.id}>
                <Link to={`/ui/try/${encodeURIComponent(ex.operation)}?example=${encodeURIComponent(ex.id)}`} className="text-sky-700 underline dark:text-sky-400">
                  {ex.id}
                </Link>{' '}
                <span className="text-slate-500">{ex.operation}</span>
                {ex.verified && <span className="ml-1 text-xs text-emerald-600">verified{ex.env ? ` (${ex.env})` : ''}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}

      {bundle.memories.length > 0 && (
        <div>
          <h2 className="mb-2 text-sm font-semibold">Memories ({bundle.memories.length})</h2>
          <ul className="space-y-1 text-sm">
            {bundle.memories.map((m) => (
              <li key={m.id} className="text-slate-600 dark:text-slate-400">
                <span className="font-mono text-xs">{m.id}</span> ({m.type}) &mdash; {m.text.split('\n')[0].slice(0, 140)}
              </li>
            ))}
          </ul>
        </div>
      )}

      {bundle.flows.length > 0 && (
        <div>
          <h2 className="mb-2 text-sm font-semibold">Flows ({bundle.flows.length})</h2>
          <ul className="space-y-1 text-sm">
            {bundle.flows.map((f) => (
              <li key={f.id}>
                <Link to={`/ui/flows/${encodeURIComponent(f.id)}`} className="text-sky-700 underline dark:text-sky-400">
                  {f.name || f.id}
                </Link>{' '}
                <span className="text-slate-400">({(f.steps ?? []).length} steps)</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {bundle.runs.length > 0 && (
        <div>
          <h2 className="mb-2 text-sm font-semibold">Runs ({bundle.runs.length})</h2>
          <ul className="space-y-1 text-sm">
            {bundle.runs.map((r) => (
              <li key={r.id}>
                <Link to={`/ui/runs/${encodeURIComponent(r.id)}`} className="text-sky-700 underline dark:text-sky-400">
                  {r.id}
                </Link>{' '}
                <span className="text-slate-500">{r.status}</span> &mdash; {r.summary}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
