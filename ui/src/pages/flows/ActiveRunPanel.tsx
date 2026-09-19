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
  // Keyed by iterKey(step_id, iteration) (PLAN §34f.8): a loop block's
  // nested step id repeats once per iteration, so step_id alone would
  // collide as a key and make progress/"current step" tracking wrong once
  // any flow has a loop.
  steps: Record<string, StepLiveStatus>;
  runId?: string;
  run?: Run;
  message?: string;
}

export interface StepLiveStatus {
  stepId: string;
  status: string;
  // Set for a nested execution inside a loop block, mirroring the run.step
  // event payload (internal/runner.emitStep) and StepResult.Iteration/Parent.
  iteration?: number;
  parent?: string;
}

/** `${stepId}` normally, `${stepId}:${iteration}` inside a loop block --
 * the same convention RunDetailPage's React keys use, so both pages agree
 * on how a repeated nested step id is disambiguated. */
export function iterKey(stepId: string, iteration?: number): string {
  return iteration != null ? `${stepId}:${iteration}` : stepId;
}

const terminal = new Set(['passed', 'failed', 'errored', 'skipped', 'cancelled']);

/** Bare step id -> latest status seen for it, across every iteration
 * (last write wins, same "steps.<id> is always the latest execution"
 * mental model docs/flows.md uses for the DSL itself) -- what FlowStepCard's
 * per-card overlay wants; it doesn't care which iteration a status came
 * from, just the most recent one. */
export function stepStatuses(active: ActiveRun): Record<string, string> {
  const merged: Record<string, string> = {};
  for (const key of Object.keys(active.steps)) merged[active.steps[key].stepId] = active.steps[key].status;
  // Once the run is over its own step results are authoritative (they also
  // cover steps whose events were missed while the socket was reconnecting).
  if (active.run?.steps) for (const s of active.run.steps) merged[s.step_id] = s.status;
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

  const entries = Object.values(active.steps);
  // "Known" leaf steps are this flow's top-level steps: a loop block counts
  // as one of them (its own completion is knowable up front), but its
  // nested, per-iteration executions are not -- a foreach/repeat's true
  // iteration count multiplies at runtime, so counting those toward the
  // denominator (or the numerator) would make the percentage bogus while a
  // run is inside a block (PLAN §34f item 9's ActiveRunPanel bullet).
  const topLevel = entries.filter((e) => !e.parent);
  const done = topLevel.filter((e) => terminal.has(e.status)).length;
  // A loop block's own entry stays non-terminal for its whole duration, so
  // preferring *a* non-terminal entry in plain insertion order would show
  // the block itself instead of whichever nested step is actually running
  // right now; a non-terminal nested entry is always the more specific,
  // more useful one to show when one exists.
  const nonTerminal = entries.filter((e) => !terminal.has(e.status));
  const current = nonTerminal.find((e) => e.parent) ?? nonTerminal[0];
  const total = active.run?.summary.steps_total || totalSteps;
  const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;
  const currentLabel = current ? (current.parent && current.iteration != null ? `${current.stepId} · iteration ${current.iteration + 1}` : current.stepId) : undefined;

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
              {active.phase === 'running' && currentLabel ? ` · ${currentLabel}` : ''}
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
