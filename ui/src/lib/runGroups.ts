// Groups a run's flat StepResult list into top-level display units for
// RunDetailPage (PLAN §34f.8): a plain call step, or a loop block with its
// nested, per-iteration executions folded together. Kept as pure functions
// (no React) so they're unit-testable on their own.
import type { StepResult } from '../api/types';

export type RunStepGroup = { kind: 'call'; step: StepResult } | { kind: 'block'; block: StepResult; iterations: StepResult[][] };

/**
 * `steps` is a flat array in execution order; a block's own aggregate
 * result (Kind set) appears *after* all of its nested executions (see
 * internal/runner/block.go), and every nested execution carries `parent`
 * (the block's step id) and `iteration` (0-based). This walks it once,
 * collecting nested executions by parent, then builds one group per
 * top-level result -- a nested execution never becomes its own group.
 */
export function groupRunSteps(steps: StepResult[] | undefined): RunStepGroup[] {
  if (!steps) return [];

  const nestedByParent = new Map<string, StepResult[]>();
  for (const s of steps) {
    if (!s.parent) continue;
    const list = nestedByParent.get(s.parent);
    if (list) list.push(s);
    else nestedByParent.set(s.parent, [s]);
  }

  const groups: RunStepGroup[] = [];
  for (const s of steps) {
    if (s.parent) continue;
    if (s.kind) {
      const nested = nestedByParent.get(s.step_id) || [];
      const iterations: StepResult[][] = [];
      for (const n of nested) {
        const idx = n.iteration ?? 0;
        while (iterations.length <= idx) iterations.push([]);
        iterations[idx].push(n);
      }
      groups.push({ kind: 'block', block: s, iterations });
    } else {
      groups.push({ kind: 'call', step: s });
    }
  }
  return groups;
}

function groupStatus(g: RunStepGroup): string {
  return g.kind === 'call' ? g.step.status : g.block.status;
}

/** Index of the first "bad" group (failed/errored) in `groups`, or -1. A
 * block group counts as bad when its own aggregated status is
 * failed/errored, which the runner only sets when at least one iteration
 * failed (or the block failed structurally, e.g. a too-long foreach list). */
export function firstBadGroupIndex(groups: RunStepGroup[]): number {
  return groups.findIndex((g) => {
    const status = groupStatus(g);
    return status === 'failed' || status === 'errored';
  });
}

/** `${step_id}:${iteration}` -- RunBlockCard's/RunStepCard's own React-key
 * convention for a nested execution -- for the first nested step that
 * failed/errored within `iterations`, so the run page can scroll straight
 * to it (inside its expanded iteration) rather than just the block's
 * header. Undefined when no single nested step is at fault. */
export function firstFailedNestedKey(iterations: StepResult[][]): string | undefined {
  for (let i = 0; i < iterations.length; i++) {
    const bad = iterations[i].find((s) => s.status === 'failed' || s.status === 'errored');
    if (bad) return `${bad.step_id}:${bad.iteration ?? i}`;
  }
  return undefined;
}
