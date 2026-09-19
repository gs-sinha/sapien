import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { runs } from '../api/client';
import { getRunHints } from '../api/flowsExtra';
import { runToChartStatuses } from '../components/flowchart/statuses';
import { EditAndRerunPanel } from '../components/run/EditAndRerunPanel';
import { HintsPanel } from '../components/run/HintsPanel';
import { RunBlockCard } from '../components/run/RunBlockCard';
import { RunStepCard } from '../components/run/RunStepCard';
import { SummaryBar } from '../components/run/SummaryBar';
import { useAsync } from '../lib/useAsync';
import { useViewMode } from '../lib/viewMode';
import { firstBadGroupIndex, firstFailedNestedKey, groupRunSteps } from '../lib/runGroups';
import { subscribe } from '../state/events';
import type { ChartFlow } from '../components/flowchart/layout';
import type { Hint } from '../api/types-runs';

// PLAN §34f item 9: the read-only flow chart is a dependency-free,
// hand-rolled SVG component kept in its own lazy chunk -- see
// FlowDetailPage.tsx's identical reasoning.
const FlowChart = lazy(() => import('../components/flowchart/FlowChart'));

const finishedStatuses = new Set(['passed', 'failed', 'errored', 'cancelled']);

// A minimal, dependency-free CSS.escape for the attribute-selector values
// this page's chart-node clicks look up (step ids are simple identifiers,
// but a quote/backslash is escaped rather than assumed away).
function cssEscape(value: string): string {
  return value.replace(/["\\]/g, '\\$&');
}

/** Parses a run's `flow_snapshot` (the flow's YAML at run start) into the
 * flow chart's input shape. Best effort: an older run, one triggered
 * without a stored flow, or a snapshot that doesn't parse just means no
 * chart, not a page error. */
async function parseFlowSnapshot(yamlText: string | undefined): Promise<ChartFlow | null> {
  if (!yamlText) return null;
  try {
    const yaml = await import('js-yaml');
    const doc = yaml.load(yamlText) as { setup?: ChartFlow['setup']; steps?: ChartFlow['steps']; teardown?: ChartFlow['teardown'] } | undefined;
    if (!doc || !Array.isArray(doc.steps)) return null;
    return { setup: doc.setup, steps: doc.steps, teardown: doc.teardown };
  } catch {
    return null;
  }
}

// Fetches the run on mount and drops it on unmount: no cross-page cache, so
// a 50-step run's request/response bodies never linger after navigating
// away (PLAN 34c performance budget).
export default function RunDetailPage() {
  const { id = '' } = useParams();
  const { data: run, error, loading, reload } = useAsync(() => runs.get(id), [id]);
  const [editOpen, setEditOpen] = useState(false);
  const [hints, setHints] = useState<Hint[]>([]);
  // PLAN §34f item 9: List|Chart toggle, remembered per browser.
  const [viewMode, setViewMode] = useViewMode('run');
  const [chartFlow, setChartFlow] = useState<ChartFlow | null>(null);
  // The step a chart node click most recently named, so the matching card
  // opens and scrolls to it.
  const [openStepId, setOpenStepId] = useState<string | undefined>(undefined);

  useEffect(() => {
    let cancelled = false;
    parseFlowSnapshot(run?.flow_snapshot).then((f) => {
      if (!cancelled) setChartFlow(f);
    });
    return () => {
      cancelled = true;
    };
  }, [run?.flow_snapshot]);

  const chartStatuses = useMemo(() => runToChartStatuses(run?.steps), [run?.steps]);

  useEffect(() => {
    const matches = (runId?: string) => runId === id;
    const un1 = subscribe('run.step', (e) => matches(e.ids.run_id) && reload());
    const un2 = subscribe('run.finished', (e) => matches(e.ids.run_id) && reload());
    return () => {
      un1();
      un2();
    };
  }, [id, reload]);

  useEffect(() => {
    if (!run || !finishedStatuses.has(run.status)) {
      setHints([]);
      return;
    }
    let cancelled = false;
    getRunHints(run.id).then((h) => {
      if (!cancelled) setHints(h);
    });
    return () => {
      cancelled = true;
    };
  }, [run?.id, run?.status]);

  const scrolledFor = useRef<string | null>(null);
  const failedElRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (run && failedElRef.current && scrolledFor.current !== run.id) {
      failedElRef.current.scrollIntoView({ block: 'center', behavior: 'smooth' });
      scrolledFor.current = run.id;
    }
  }, [run]);

  const onChartSelect = (stepId: string) => {
    setOpenStepId(stepId);
    const el = document.querySelector(`[data-step-card-id="${cssEscape(stepId)}"]`);
    el?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  };

  if (loading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="p-4 text-sm text-red-600">{error.message}</div>;
  if (!run) return null;

  // Groups a block's nested/per-iteration executions under it (PLAN
  // §34f.8) rather than showing them as their own top-level rows: the
  // "scroll to first failed" behaviour below has to look inside a block's
  // iterations, not just at its own aggregated status, to find the exact
  // step that failed.
  const groups = groupRunSteps(run.steps);
  const firstBad = firstBadGroupIndex(groups);

  return (
    <div className="space-y-4 p-4">
      <div className="flex items-center gap-3">
        <h1 className="text-lg font-semibold">{run.id}</h1>
        <button
          type="button"
          onClick={() => setEditOpen((o) => !o)}
          className="ml-auto rounded border border-slate-300 px-3 py-1 text-sm dark:border-slate-700"
        >
          {editOpen ? 'Close editor' : 'Edit and rerun'}
        </button>
      </div>

      <SummaryBar run={run} onPinChange={reload} />

      {run.error && (
        <div className="rounded border border-red-300 bg-red-50 p-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
          {run.error.code}: {run.error.message}
        </div>
      )}

      {editOpen && <EditAndRerunPanel run={run} onClose={() => setEditOpen(false)} />}

      <HintsPanel hints={hints} />

      <div>
        <div className="mb-2 flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold">Steps</h2>
          {chartFlow && (
            <div className="flex overflow-hidden rounded border border-slate-300 text-xs dark:border-slate-700">
              {(['list', 'chart'] as const).map((mode) => (
                <button
                  key={mode}
                  type="button"
                  aria-pressed={viewMode === mode}
                  onClick={() => setViewMode(mode)}
                  className={`px-2 py-1 capitalize ${
                    viewMode === mode
                      ? 'bg-sky-600 text-white dark:bg-sky-500'
                      : 'text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800'
                  }`}
                >
                  {mode}
                </button>
              ))}
            </div>
          )}
        </div>
        <div className={viewMode === 'chart' && chartFlow ? 'flex flex-col gap-4 md:flex-row md:items-start' : undefined}>
          {viewMode === 'chart' && chartFlow && (
            <div className="md:sticky md:top-0 md:w-[45%] md:shrink-0">
              <Suspense fallback={<div className="p-3 text-sm text-slate-400">Loading chart…</div>}>
                <FlowChart flow={chartFlow} statuses={chartStatuses} selectedId={openStepId} onSelect={onChartSelect} />
              </Suspense>
            </div>
          )}
          <div className="min-w-0 flex-1 rounded border border-slate-200 dark:border-slate-800">
            {groups.map((g, i) => {
              const isFirstBad = i === firstBad;
              if (g.kind === 'call') {
                const key = g.step.iteration != null ? `${g.step.step_id}:${g.step.iteration}` : g.step.step_id;
                return (
                  <RunStepCard
                    key={key}
                    step={g.step}
                    runId={run.id}
                    env={run.environment}
                    defaultOpen={isFirstBad}
                    scrollRef={isFirstBad ? failedElRef : undefined}
                    openStepId={openStepId}
                  />
                );
              }
              const nestedKey = isFirstBad ? firstFailedNestedKey(g.iterations) : undefined;
              return (
                <RunBlockCard
                  key={g.block.step_id}
                  block={g.block}
                  iterations={g.iterations}
                  runId={run.id}
                  env={run.environment}
                  scrollRef={isFirstBad ? failedElRef : undefined}
                  scrollToKey={nestedKey}
                  openStepId={openStepId}
                />
              );
            })}
            {groups.length === 0 && <div className="p-3 text-sm text-slate-400">No steps recorded.</div>}
          </div>
        </div>
      </div>
    </div>
  );
}
