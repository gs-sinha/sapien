import { describe, expect, it } from 'vitest';
import { firstBadGroupIndex, firstFailedNestedKey, groupRunSteps } from '../lib/runGroups';
import type { StepResult } from '../api/types';

function sr(overrides: Partial<StepResult>): StepResult {
  return { step_id: 'x', index: 0, status: 'passed', ...overrides };
}

describe('groupRunSteps', () => {
  it('groups a plain (blockless) run one-to-one, in order', () => {
    const steps = [sr({ step_id: 'a', index: 0 }), sr({ step_id: 'b', index: 1, status: 'failed' })];
    const groups = groupRunSteps(steps);
    expect(groups).toEqual([
      { kind: 'call', step: steps[0] },
      { kind: 'call', step: steps[1] },
    ]);
  });

  it("folds a block's nested executions into per-iteration arrays, keyed by StepResult.iteration, and never lists them as their own top-level group", () => {
    // Execution order matches internal/runner/block.go: nested results are
    // appended before the block's own aggregate result.
    const steps = [
      sr({ step_id: 'create', index: 0, parent: 'each', iteration: 0, status: 'passed' }),
      sr({ step_id: 'create', index: 1, parent: 'each', iteration: 1, status: 'failed' }),
      sr({ step_id: 'each', index: 2, kind: 'foreach', count: 2, status: 'failed' }),
    ];
    const groups = groupRunSteps(steps);

    expect(groups).toHaveLength(1);
    expect(groups[0].kind).toBe('block');
    if (groups[0].kind !== 'block') throw new Error('unreachable');
    expect(groups[0].block.step_id).toBe('each');
    expect(groups[0].iterations).toHaveLength(2);
    expect(groups[0].iterations[0]).toEqual([steps[0]]);
    expect(groups[0].iterations[1]).toEqual([steps[1]]);
  });

  it('keeps a call step before and after a block in their original positions', () => {
    const steps = [
      sr({ step_id: 'before', index: 0 }),
      sr({ step_id: 'create', index: 1, parent: 'each', iteration: 0 }),
      sr({ step_id: 'each', index: 2, kind: 'foreach', count: 1 }),
      sr({ step_id: 'after', index: 3 }),
    ];
    const groups = groupRunSteps(steps);
    expect(groups.map((g) => (g.kind === 'call' ? g.step.step_id : g.block.step_id))).toEqual(['before', 'each', 'after']);
  });

  it('is empty for no steps', () => {
    expect(groupRunSteps(undefined)).toEqual([]);
    expect(groupRunSteps([])).toEqual([]);
  });
});

describe('firstBadGroupIndex', () => {
  it('finds a failed/errored call step', () => {
    const groups = groupRunSteps([sr({ step_id: 'a' }), sr({ step_id: 'b', status: 'errored' })]);
    expect(firstBadGroupIndex(groups)).toBe(1);
  });

  it("finds a block group whose own aggregated status is bad, even though the block's own row isn't a call step", () => {
    const groups = groupRunSteps([
      sr({ step_id: 'create', parent: 'each', iteration: 0, status: 'failed' }),
      sr({ step_id: 'each', kind: 'foreach', count: 1, status: 'failed' }),
    ]);
    expect(firstBadGroupIndex(groups)).toBe(0);
  });

  it('is -1 when nothing failed', () => {
    const groups = groupRunSteps([sr({ step_id: 'a' })]);
    expect(firstBadGroupIndex(groups)).toBe(-1);
  });
});

describe('firstFailedNestedKey', () => {
  it('finds the first failed nested step across iterations, keyed by step_id:iteration', () => {
    const iterations = [[sr({ step_id: 'create', iteration: 0, status: 'passed' })], [sr({ step_id: 'create', iteration: 1, status: 'failed' })]];
    expect(firstFailedNestedKey(iterations)).toBe('create:1');
  });

  it('is undefined when no iteration has a failed step', () => {
    const iterations = [[sr({ step_id: 'create', iteration: 0, status: 'passed' })]];
    expect(firstFailedNestedKey(iterations)).toBeUndefined();
  });
});
