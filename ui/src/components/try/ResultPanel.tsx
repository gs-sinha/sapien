// Shows the outcome of a /v1/call: status pill, latency, response headers,
// body, a link to the full run, and (on failure) the run's error plus
// diagnose hints as "Might explain it".
import { Link } from 'react-router-dom';
import { JsonView } from '../../components/JsonView';
import type { ReactNode } from 'react';
import type { ApiClientError } from '../../api/client';
import type { Run } from '../../api/types';
import type { Hint } from '../../api/types-try';

function statusClass(status: number): string {
  if (status >= 200 && status < 300) return 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300';
  if (status >= 300 && status < 400) return 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300';
  if (status >= 400) return 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300';
  return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
}

function HintRow({ hint }: { hint: Hint }) {
  const kindLabel = hint.kind === 'contract' ? 'contract' : hint.kind === 'doc' ? 'doc' : 'memory';
  let ref: ReactNode = null;
  if (hint.kind === 'memory' && hint.ref.memory_id) {
    ref = (
      <Link to={`/ui/memories/${encodeURIComponent(hint.ref.memory_id)}`} className="text-sky-700 underline dark:text-sky-400">
        {hint.ref.memory_id}
      </Link>
    );
  } else if (hint.kind === 'doc' && hint.ref.service) {
    ref = (
      <span className="font-mono text-[11px] text-slate-500">
        {hint.ref.service}/{hint.ref.path}
        {hint.ref.section ? ` # ${hint.ref.section}` : ''}
      </span>
    );
  }
  return (
    <li className="rounded border border-slate-200 p-2 text-sm dark:border-slate-800">
      <div className="mb-0.5 flex items-center gap-2">
        <span className="rounded-full bg-slate-100 px-1.5 py-0.5 text-[10px] font-medium uppercase text-slate-600 dark:bg-slate-800 dark:text-slate-300">
          {kindLabel}
        </span>
        <span>{hint.title}</span>
      </div>
      {ref && <div>{ref}</div>}
    </li>
  );
}

export function ResultPanel({
  run,
  callError,
  hints,
}: {
  run: Run | null;
  callError: ApiClientError | Error | null;
  hints: Hint[];
}) {
  if (!run && !callError) return null;

  if (callError) {
    return (
      <div className="rounded border border-red-300 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
        <div className="font-medium">{'code' in callError ? callError.code : callError.name}</div>
        <div>{callError.message}</div>
      </div>
    );
  }

  if (!run) return null;
  const step = run.steps?.[0];
  const response = step?.response;
  const failed = run.status !== 'passed';

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        {response && (
          <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${statusClass(response.status)}`}>
            {response.status}
          </span>
        )}
        {step?.timings && <span className="text-xs text-slate-500">{Math.round(step.timings.total_ms)} ms</span>}
        <Link to={`/ui/runs/${encodeURIComponent(run.id)}`} className="text-xs text-sky-700 underline dark:text-sky-400">
          View run {run.id}
        </Link>
      </div>

      {(run.error || step?.error) && (
        <div className="rounded border border-red-300 bg-red-50 p-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
          {(run.error || step?.error)?.code}: {(run.error || step?.error)?.message}
        </div>
      )}

      {response && (
        <>
          <div>
            <h3 className="mb-1 text-xs font-semibold uppercase text-slate-500">Response headers</h3>
            <JsonView data={response.headers || {}} />
          </div>
          <div>
            <h3 className="mb-1 text-xs font-semibold uppercase text-slate-500">Response body</h3>
            <JsonView data={response.body ?? null} />
          </div>
        </>
      )}

      {failed && hints.length > 0 && (
        <div>
          <h3 className="mb-1 text-xs font-semibold uppercase text-slate-500">Might explain it</h3>
          <ul className="space-y-1.5">
            {hints.map((h, i) => (
              <HintRow key={i} hint={h} />
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
