// Small helpers shared by the flow chart (components/flowchart/layout.ts),
// FlowStepCard, and RunStepCard/RunBlockCard for reading a domain.Step's
// `when`/loop-block shape (PLAN §34f items 7/8). Kept dependency-free and
// tiny on purpose: the chart chunk imports this module too, and it must
// stay a lazy chunk within the bundle budget.
import type { Step } from '../api/types';

/** A step with `steps` set and no `call`/`example` is a loop block (see
 * docs/flows.md's "Loop blocks"): it runs its nested `steps` repeatedly
 * instead of calling an operation itself. */
export function isBlockStep(step: Step): boolean {
  return !!(step.steps && step.steps.length > 0);
}

/** Truncates an expression for inline display, keeping the full text
 * available separately (e.g. in a title/tooltip). */
export function truncateExpr(text: string, max = 40): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max - 1)}…`;
}

/** The block header text ("foreach <expr>" / "repeat until <expr> · max N"),
 * truncated for inline display (`short`) and untruncated (`full`, for a
 * tooltip/title). Falls back to the step id for a malformed block (neither
 * `foreach` nor `repeat` set) so the chart and cards never render nothing. */
export function blockHeaderText(step: Step): { short: string; full: string } {
  let full: string;
  if (step.foreach) {
    full = `foreach ${step.foreach}`;
  } else if (step.repeat) {
    const cond = step.repeat.until ? `until ${step.repeat.until}` : step.repeat.while ? `while ${step.repeat.while}` : '';
    full = `repeat ${cond} · max ${step.repeat.max}`.replace(/\s+·/, ' ·').trim();
  } else {
    full = step.id;
  }
  return { short: truncateExpr(full, 40), full };
}

/** "foreach" | "repeat" | undefined, mirroring domain.StepResult.Kind. */
export function blockKind(step: Step): 'foreach' | 'repeat' | undefined {
  if (step.foreach) return 'foreach';
  if (step.repeat) return 'repeat';
  return undefined;
}

/** Whether `id` is `step.id` itself or one of its nested steps' ids (recursively). */
export function containsStepId(step: Step, id: string): boolean {
  if (step.id === id) return true;
  return (step.steps || []).some((s) => containsStepId(s, id));
}
