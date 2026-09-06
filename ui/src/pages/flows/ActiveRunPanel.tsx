import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { StatusPill } from '../../components/StatusPill';
import { useEvents } from '../../state/events';
import type { Run } from '../../api/types';

// One run started from this page, tracked until it finishes.
//
// `steps` is fed by run.step events off the WebSocket while the (synchronous)
// POST is still in flight, so progress shows up here and as per-step badges in
// the step list without waiting for the response. `runId` is learned from the
// run.started event -- the POST only tells us the id once it's over.
export interface ActiveRun {
  phase: 'running' | 'done' | 'error';
  startedAt: number;
  steps: Record<string, string>;
  runId?: string;
  run?: Run;
  message?: string;
}

const terminal = new Set(['passed', 'failed', 'errored', 'skipped', 'cancelled']);

export function stepStatuses(active: ActiveRun): Record<string, string> {
  if (!active.run?.steps) return active.steps;
  // Once the run is over its own step results are authoritative (they also
  // cover steps whose events were missed while the socket was reconnecting).
  const merged = { ...active.steps };
  for (const s of active.run.steps) merged[s.step_id] = s.status;
  return merged;
}

function useElapsed(active: ActiveRun): number {
  const [, tick] = useState(0);
  const live = active.phase === 'running';
  useEffect(() => {
    if (!live) return;
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [live]);
  if (active.run?.duration_ms !== undefined) return active.run.duration_ms;
  return Date.now() - active.startedAt;
}

function formatMs(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

export function ActiveRunPanel({
  active,
  totalSteps,
  onDismiss,
}: {
  active: ActiveRun;
  totalSteps: number;
  onDismiss: () => void;
}) {
  const elapsed = useElapsed(active);
  const connection = useEvents((s) => s.status);

  const statuses = stepStatuses(active);
  const done = Object.values(statuses).filter((s) => terminal.has(s)).length;
  const current = Object.entries(statuses).find(([, s]) => !terminal.has(s))?.[0];
  const total = active.run?.summary.steps_total || totalSteps;
  const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;

  return (
    <div className="rounded border border-slate-200 p-3 dark:border-slate-800">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
        <StatusPill status={active.phase === 'running' ? 'running' : active.run?.status || 'errored'} />
        {active.phase === 'error' ? (
          <span className="text-red-600 dark:text-red-400">{active.message || 'run failed to start'}</span>
        ) : (
          <>
            <span className="text-slate-500">
              {done}/{total || '?'} steps
              {active.phase === 'running' && current ? ` · ${current}` : ''}
            </span>
            {active.run && (
              <span className="text-slate-500">
                {active.run.summary.assertions - active.run.summary.assertions_failed}/{active.run.summary.assertions} assertions
              </span>
            )}
            <span className="text-slate-400">{formatMs(elapsed)}</span>
          </>
        )}
        <span className="ml-auto flex items-center gap-3">
          {active.runId && (
            <Link to={`/ui/runs/${encodeURIComponent(active.runId)}`} className="text-xs text-sky-700 hover:underline dark:text-sky-400">
              Open run details →
            </Link>
          )}
          <button type="button" onClick={onDismiss} className="text-xs text-slate-400 hover:text-slate-700 dark:hover:text-slate-200">
            Dismiss
          </button>
        </span>
      </div>

      {active.phase !== 'error' && (
        <div className="mt-2 h-1 w-full overflow-hidden rounded bg-slate-100 dark:bg-slate-800">
          <div
            className={`h-full transition-[width] duration-300 ${
              active.run && active.run.status !== 'passed' ? 'bg-red-500' : 'bg-emerald-500'
            }`}
            style={{ width: `${active.phase === 'running' && pct === 0 ? 4 : pct}%` }}
          />
        </div>
      )}

      {active.run?.error && (
        <div className="mt-2 text-xs text-red-600 dark:text-red-400">
          {active.run.error.code}: {active.run.error.message}
        </div>
      )}

      {active.phase === 'running' && connection !== 'open' && (
        <div className="mt-2 text-xs text-slate-400">
          Live updates are unavailable (event stream {connection}); the result appears when the run finishes.
        </div>
      )}
    </div>
  );
}
