// Turns a run's flat StepResult list into the status overlay FlowChart.tsx
// draws on top of layout.ts's (run-independent) geometry. Kept separate
// from layout.ts so the layout stays a pure function of the flow
// definition alone (PLAN §34f item 9).
import type { StepResult } from '../../api/types';

export interface StepStatusInfo {
  status: string;
  skipReason?: string;
}

/** One loop block's run info: its own aggregate result, plus each iteration's nested statuses (index-aligned, 0-based). */
export interface BlockRunInfo extends StepStatusInfo {
  kind?: string; // 'foreach' | 'repeat'
  count?: number;
  iterations: Array<Record<string, StepStatusInfo>>;
}

export interface FlowChartStatuses {
  /** Top-level (and nested-inside-a-block, keyed by step id -- see note below) step statuses. */
  steps: Record<string, StepStatusInfo>;
  blocks: Record<string, BlockRunInfo>;
}

/** Builds a FlowChartStatuses from one run's StepResult list. A nested
 * execution (parent + iteration set) is folded into its block's
 * `iterations[iteration]`; the block's own aggregate result (kind set)
 * becomes `blocks[step_id]`. Every other result is a plain top-level step. */
export function runToChartStatuses(steps: StepResult[] | undefined): FlowChartStatuses {
  const out: FlowChartStatuses = { steps: {}, blocks: {} };
  if (!steps) return out;

  for (const s of steps) {
    if (s.parent) {
      const block = (out.blocks[s.parent] ||= { status: 'pending', iterations: [] });
      const idx = s.iteration ?? 0;
      while (block.iterations.length <= idx) block.iterations.push({});
      block.iterations[idx][s.step_id] = { status: s.status, skipReason: s.skip_reason };
      continue;
    }
    if (s.kind) {
      const block = (out.blocks[s.step_id] ||= { status: 'pending', iterations: [] });
      block.status = s.status;
      block.kind = s.kind;
      block.count = s.count;
      block.skipReason = s.skip_reason;
      continue;
    }
    out.steps[s.step_id] = { status: s.status, skipReason: s.skip_reason };
  }

  return out;
}

/** Whether an iteration (a nested-status map) "failed": any of its steps
 * did. Used for default iteration-picker position and summary counts. */
export function iterationFailed(iter: Record<string, StepStatusInfo>): boolean {
  return Object.values(iter).some((s) => s.status === 'failed' || s.status === 'errored' || s.status === 'cancelled');
}
