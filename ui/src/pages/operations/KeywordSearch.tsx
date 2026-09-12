// Ranked operation search for OperationsPage, used for keywords, paths, and
// natural-language intents when no ?intent= is set:
// GET /v1/operations?q=&service=&method=.
import { Link } from 'react-router-dom';
import { EmptyState } from '../../components/EmptyState';
import { VirtualList } from '../../components/VirtualList';
import { operations } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import type { Operation, SearchResult } from '../../api/types';
import { timed } from './timing';

function operationOf(item: SearchResult | Operation): Operation {
  return 'operation' in item ? item.operation : item;
}

interface FetchResult {
  kind: 'search' | 'list';
  items: Array<SearchResult | Operation>;
}

async function fetchOps(query: string, service: string, method: string): Promise<FetchResult> {
  if (query.trim()) {
    const res = (await operations.search({ query, service: service || undefined, method: method || undefined })) as SearchResult[];
    return { kind: 'search', items: res };
  }
  const res = (await operations.search({ service: service || undefined, method: method || undefined })) as Operation[];
  return { kind: 'list', items: res };
}

function OperationRow({ item }: { item: SearchResult | Operation }) {
  const op = operationOf(item);
  const score = 'score' in item ? item.score : undefined;
  const matchedOn = 'matched_on' in item ? item.matched_on : undefined;
  return (
    <Link
      to={`/ui/operations/${encodeURIComponent(op.id)}`}
      className="flex items-center gap-3 border-b border-slate-100 px-3 py-2 text-sm hover:bg-slate-50 dark:border-slate-900 dark:hover:bg-slate-900"
    >
      <span className="w-16 shrink-0 text-xs font-medium text-slate-400">{op.http?.method}</span>
      <span className="w-64 shrink-0 truncate font-mono text-xs">{op.id}</span>
      <span className="flex-1 truncate text-slate-500">{op.summary}</span>
      {matchedOn && matchedOn.length > 0 && (
        <span className="hidden shrink-0 gap-1 sm:flex">
          {matchedOn.slice(0, 3).map((m) => {
            const task = m.startsWith('task:');
            return (
              <span
                key={m}
                title={task ? `Matched task ${m.slice('task:'.length)}` : undefined}
                className={
                  task
                    ? 'rounded bg-violet-100 px-1.5 py-0.5 text-[11px] text-violet-700 dark:bg-violet-950 dark:text-violet-300'
                    : 'rounded bg-slate-100 px-1.5 py-0.5 text-[11px] text-slate-500 dark:bg-slate-800 dark:text-slate-400'
                }
              >
                {task ? `task: ${m.slice('task:'.length)}` : m}
              </span>
            );
          })}
        </span>
      )}
      {score !== undefined && <span className="w-14 shrink-0 text-right text-xs text-slate-400">{score.toFixed(2)}</span>}
    </Link>
  );
}

export function KeywordSearch({ query, service, method }: { query: string; service: string; method: string }) {
  const { data, error, loading } = useAsync(() => timed(() => fetchOps(query, service, method)), [query, service, method]);

  if (loading) return <div className="text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="text-sm text-red-600">{error.message}</div>;
  if (!data) return null;
  const { items } = data.data;
  const taskMatches = items.flatMap((item) => {
    if (!('operation' in item) || !item.tasks) return [];
    return item.tasks.map((task) => ({ ...task, operation: item.operation }));
  });

  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      <p className="mb-2 text-xs text-slate-400">
        {items.length} result{items.length === 1 ? '' : 's'} &middot; {data.latencyMs} ms
      </p>
      {taskMatches.length > 0 && (
        <div className="mb-3 grid gap-2 sm:grid-cols-2">
          {taskMatches.map((task) => (
            <Link
              key={`${task.id}:${task.operation.id}`}
              to={`/ui/operations/${encodeURIComponent(task.operation.id)}`}
              className="rounded border border-violet-200 bg-violet-50 p-3 text-sm hover:border-violet-300 dark:border-violet-900 dark:bg-violet-950/30"
            >
              <div className="font-medium text-violet-800 dark:text-violet-300">{task.phrase || task.id}</div>
              <div className="mt-1 font-mono text-xs text-slate-600 dark:text-slate-400">{task.operation.id}</div>
              {task.when && <div className="mt-1 text-xs text-slate-500">When: {task.when}</div>}
            </Link>
          ))}
        </div>
      )}
      {items.length === 0 ? (
        <EmptyState title="No operations found" />
      ) : (
        <div className="flex flex-1 flex-col overflow-hidden rounded border border-slate-200 dark:border-slate-800">
          <VirtualList
            items={items}
            itemHeight={40}
            height={Math.min(640, Math.max(200, items.length * 40))}
            renderItem={(item) => <OperationRow item={item} />}
          />
        </div>
      )}
    </div>
  );
}
