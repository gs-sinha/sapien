import { useEffect, useState } from 'react';
import type { Ref } from 'react';
import { StatusPill } from '../StatusPill';
import { RunStepCard } from './RunStepCard';
import type { StepResult } from '../../api/types';

function iterationFailed(steps: StepResult[]): boolean {
  return steps.some((s) => s.status === 'failed' || s.status === 'errored' || s.status === 'cancelled');
}

/** `${step_id}:${iteration}`, matching RunDetailPage's/RunStepCard's own React-key convention (PLAN §34f.8: keys must stay unique). */
function nestedKey(s: StepResult, fallbackIteration: number): string {
  return `${s.step_id}:${s.iteration ?? fallbackIteration}`;
}

/**
 * One loop block's group card in the run detail list (PLAN §34f item 9):
 * a header (kind, count, aggregated status) plus a collapsible section per
 * iteration ("iteration 3 · failed") -- open by default only when that
 * iteration failed, passed ones collapsed to one line. A block skipped by
 * its own `when` shows the same message RunStepCard shows for a skipped
 * call step, with no iteration groups.
 */
export function RunBlockCard({
  block,
  iterations,
  runId,
  env,
  scrollRef,
  scrollToKey,
  openStepId,
}: {
  block: StepResult;
  /** One entry per iteration that ran, in execution order (index === StepResult.iteration). */
  iterations: StepResult[][];
  runId: string;
  env: string;
  /** Attached to this card's own root when it -- not a specific nested step -- is the "scroll to first failed" target. */
  scrollRef?: Ref<HTMLDivElement>;
  /** The specific nested step's key (see `nestedKey`) to scroll to and open instead, when the first failure is inside a known iteration. */
  scrollToKey?: string;
  /** Selecting a node on the flow chart (PLAN §34f item 9) names a step id to open; forwarded to whichever nested card matches. */
  openStepId?: string;
}) {
  const [open, setOpen] = useState(true);
  const [openIterations, setOpenIterations] = useState<Record<number, boolean>>(() => {
    const initial: Record<number, boolean> = {};
    iterations.forEach((steps, i) => {
      if (iterationFailed(steps)) initial[i] = true;
    });
    return initial;
  });

  useEffect(() => {
    if (openStepId === block.step_id) setOpen(true);
  }, [openStepId, block.step_id]);

  // The iteration holding `scrollToKey`/`openStepId` must be expanded for
  // its card to be reachable at all.
  useEffect(() => {
    if (!scrollToKey && !openStepId) return;
    iterations.forEach((steps, i) => {
      if (steps.some((s) => nestedKey(s, i) === scrollToKey || s.step_id === openStepId)) {
        setOpenIterations((prev) => (prev[i] ? prev : { ...prev, [i]: true }));
      }
    });
  }, [scrollToKey, openStepId, iterations]);

  const isSkipped = block.status === 'skipped';
  const kindLabel = block.kind === 'repeat' ? 'repeat' : 'foreach';

  return (
    <div ref={scrollToKey ? undefined : scrollRef} className="border-b border-slate-100 dark:border-slate-900" data-step-card-id={block.step_id}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 bg-slate-50/60 px-3 py-2 text-left text-sm hover:bg-slate-100 dark:bg-slate-900/40 dark:hover:bg-slate-900"
      >
        <span className="w-4 text-slate-400">{open ? '▾' : '▸'}</span>
        <span className="font-mono text-xs font-semibold">{block.step_id}</span>
        <StatusPill status={block.status} />
        {!isSkipped && (
          <span className="text-xs text-slate-400">
            {kindLabel} &middot; &times;{block.count ?? iterations.length}
          </span>
        )}
      </button>
      {open && isSkipped && (
        <div className="border-t border-slate-100 bg-slate-50/50 p-3 text-sm text-slate-500 dark:border-slate-900 dark:bg-slate-900/40 dark:text-slate-400">
          {block.skip_reason === 'when' ? 'skipped: when was false' : 'skipped'}
        </div>
      )}
      {open && !isSkipped && (
        <div>
          {iterations.map((steps, i) => {
            const failed = iterationFailed(steps);
            const isOpen = openIterations[i] ?? false;
            return (
              <div key={i} className="border-t border-slate-100 pl-4 dark:border-slate-900">
                <button
                  type="button"
                  onClick={() => setOpenIterations((prev) => ({ ...prev, [i]: !isOpen }))}
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-slate-50 dark:hover:bg-slate-900"
                >
                  <span className="w-4 text-slate-400">{isOpen ? '▾' : '▸'}</span>
                  <span>iteration {i + 1}</span>
                  <StatusPill status={failed ? 'failed' : 'passed'} />
                  {!isOpen && <span className="text-slate-400">{steps.length} steps</span>}
                </button>
                {isOpen && (
                  <div>
                    {steps.map((s) => {
                      const key = nestedKey(s, i);
                      return (
                        <RunStepCard
                          key={key}
                          step={s}
                          runId={runId}
                          env={env}
                          defaultOpen={key === scrollToKey}
                          scrollRef={key === scrollToKey ? scrollRef : undefined}
                          openStepId={openStepId}
                        />
                      );
                    })}
                  </div>
                )}
              </div>
            );
          })}
          {iterations.length === 0 && <div className="p-3 text-xs text-slate-400">No iterations ran.</div>}
        </div>
      )}
    </div>
  );
}
