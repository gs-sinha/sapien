import { useEffect, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { runs } from '../api/client';
import { getRunHints } from '../api/flowsExtra';
import { EditAndRerunPanel } from '../components/run/EditAndRerunPanel';
import { HintsPanel } from '../components/run/HintsPanel';
import { RunStepCard } from '../components/run/RunStepCard';
import { SummaryBar } from '../components/run/SummaryBar';
import { useAsync } from '../lib/useAsync';
import { subscribe } from '../state/events';
import type { Hint } from '../api/types-runs';

const finishedStatuses = new Set(['passed', 'failed', 'errored', 'cancelled']);

// Fetches the run on mount and drops it on unmount: no cross-page cache, so
// a 50-step run's request/response bodies never linger after navigating
// away (PLAN 34c performance budget).
export default function RunDetailPage() {
  const { id = '' } = useParams();
  const { data: run, error, loading, reload } = useAsync(() => runs.get(id), [id]);
  const [editOpen, setEditOpen] = useState(false);
  const [hints, setHints] = useState<Hint[]>([]);

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

  if (loading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="p-4 text-sm text-red-600">{error.message}</div>;
  if (!run) return null;

  const firstBadIndex = (run.steps || []).findIndex((s) => s.status === 'failed' || s.status === 'errored');

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
        <h2 className="mb-2 text-sm font-semibold">Steps</h2>
        <div className="rounded border border-slate-200 dark:border-slate-800">
          {(run.steps || []).map((s, i) => (
            <RunStepCard
              // A loop block's nested step id (PLAN §34f.8) repeats once per
              // iteration, so step_id alone is not a stable/unique React key.
              key={s.iteration != null ? `${s.step_id}:${s.iteration}` : s.step_id}
              step={s}
              runId={run.id}
              env={run.environment}
              defaultOpen={i === firstBadIndex}
              scrollRef={i === firstBadIndex ? failedElRef : undefined}
            />
          ))}
          {(!run.steps || run.steps.length === 0) && <div className="p-3 text-sm text-slate-400">No steps recorded.</div>}
        </div>
      </div>
    </div>
  );
}
