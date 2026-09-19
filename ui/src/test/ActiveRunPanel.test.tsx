import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { ActiveRunPanel, iterKey } from '../pages/flows/ActiveRunPanel';
import type { ActiveRun } from '../pages/flows/ActiveRunPanel';

function renderPanel(active: ActiveRun, totalSteps: number) {
  return render(
    <MemoryRouter>
      <ActiveRunPanel active={active} totalSteps={totalSteps} onDismiss={() => {}} />
    </MemoryRouter>,
  );
}

describe('ActiveRunPanel', () => {
  it("keys live statuses by step id + iteration, so a loop block's repeated nested step id does not collide", () => {
    const active: ActiveRun = {
      phase: 'running',
      startedAt: Date.now(),
      steps: {
        [iterKey('each')]: { stepId: 'each', status: 'requesting' },
        [iterKey('create', 0)]: { stepId: 'create', status: 'passed', iteration: 0, parent: 'each' },
        [iterKey('create', 1)]: { stepId: 'create', status: 'requesting', iteration: 1, parent: 'each' },
      },
    };
    renderPanel(active, 1);

    // Both iterations of "create" are tracked as distinct entries: iteration
    // 0's "passed" was not overwritten by iteration 1's "requesting".
    expect(Object.keys(active.steps)).toHaveLength(3);
    expect(active.steps[iterKey('create', 0)].status).toBe('passed');
    expect(active.steps[iterKey('create', 1)].status).toBe('requesting');
  });

  it('shows "step · iteration N" (1-based) for the current in-flight nested step', () => {
    const active: ActiveRun = {
      phase: 'running',
      startedAt: Date.now(),
      steps: {
        [iterKey('each')]: { stepId: 'each', status: 'requesting' },
        [iterKey('create', 3)]: { stepId: 'create', status: 'requesting', iteration: 3, parent: 'each' },
      },
    };
    renderPanel(active, 1);
    expect(screen.getByText(/create · iteration 4/)).toBeInTheDocument();
  });

  it("counts only top-level (non-nested) steps toward done/total, so a loop's iterations never produce a bogus percentage", () => {
    const active: ActiveRun = {
      phase: 'running',
      startedAt: Date.now(),
      steps: {
        a: { stepId: 'a', status: 'passed' },
        [iterKey('each')]: { stepId: 'each', status: 'requesting' },
        [iterKey('create', 0)]: { stepId: 'create', status: 'passed', iteration: 0, parent: 'each' },
        [iterKey('create', 1)]: { stepId: 'create', status: 'passed', iteration: 1, parent: 'each' },
        [iterKey('create', 2)]: { stepId: 'create', status: 'requesting', iteration: 2, parent: 'each' },
      },
    };
    // 2 top-level steps ("a", "each"); "a" passed, "each" still running --
    // 1/2, not inflated by the 2 already-passed nested iterations.
    const { container } = renderPanel(active, 2);
    expect(screen.getByText(/1\/2 steps/)).toBeInTheDocument();

    // The progress bar reflects that same 1/2 (50%), not something derived
    // from the 3 nested entries (which would overshoot 100%).
    const bar = container.querySelector('.bg-emerald-500') as HTMLElement;
    expect(bar.style.width).toBe('50%');
  });

  it('a run entirely inside setup/steps with no loop blocks still reports plainly (no parent/iteration anywhere)', () => {
    const active: ActiveRun = {
      phase: 'running',
      startedAt: Date.now(),
      steps: {
        a: { stepId: 'a', status: 'passed' },
        b: { stepId: 'b', status: 'requesting' },
      },
    };
    renderPanel(active, 2);
    expect(screen.getByText(/1\/2 steps/)).toHaveTextContent('b');
  });
});
