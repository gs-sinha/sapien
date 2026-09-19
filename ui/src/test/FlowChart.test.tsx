import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { FlowChart } from '../components/flowchart/FlowChart';
import { runToChartStatuses } from '../components/flowchart/statuses';
import type { ChartFlow } from '../components/flowchart/layout';
import type { Step, StepResult } from '../api/types';

function call(id: string, extra: Partial<Step> = {}): Step {
  return { id, call: `svc.${id}`, ...extra };
}

describe('FlowChart', () => {
  it('renders a rounded rect node per call step, focusable with an aria-label naming the step', () => {
    const flow: ChartFlow = { steps: [call('create'), call('allocate')] };
    render(<FlowChart flow={flow} />);

    const node = screen.getByRole('button', { name: /step create/i });
    expect(node).toHaveAttribute('tabindex', '0');
    expect(screen.getByText('create')).toBeInTheDocument();
    expect(screen.getByText('svc.create')).toBeInTheDocument();
  });

  it('renders a diamond for a `when` step, with the full condition in a <title>', () => {
    const flow: ChartFlow = { steps: [call('release', { when: 'inputs.releaseNow' })] };
    const { container } = render(<FlowChart flow={flow} />);

    const diamond = container.querySelector('[data-node-kind="when"]');
    expect(diamond).toBeTruthy();
    expect(diamond!.querySelector('title')?.textContent).toBe('inputs.releaseNow');
  });

  it('renders a dashed container for a loop block, with its header text', () => {
    const block: Step = { id: 'each', call: '', foreach: 'inputs.ids', steps: [call('create')] };
    const { container } = render(<FlowChart flow={{ steps: [block] }} />);

    const group = container.querySelector('[data-node-kind="block"]');
    expect(group).toBeTruthy();
    expect(group!.querySelector('rect')).toHaveAttribute('stroke-dasharray', '6 4');
    expect(group!.querySelector('text')?.textContent).toContain('foreach inputs.ids');
  });

  it('draws a bypass edge (an elbow path, not a straight line) for a `when` step', () => {
    const flow: ChartFlow = { steps: [call('a'), call('release', { when: 'inputs.x' }), call('b')] };
    const { container } = render(<FlowChart flow={flow} />);

    const paths = Array.from(container.querySelectorAll('path'));
    // The bypass is the only path with two "Q" (rounded-corner) commands.
    const bypass = paths.find((p) => (p.getAttribute('d') || '').includes('Q'));
    expect(bypass).toBeDefined();
  });

  it('calls onSelect with the step id when a node is clicked or Enter is pressed', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const flow: ChartFlow = { steps: [call('create')] };
    render(<FlowChart flow={flow} onSelect={onSelect} />);

    const node = screen.getByRole('button', { name: /step create/i });
    await user.click(node);
    expect(onSelect).toHaveBeenCalledWith('create');

    node.focus();
    await user.keyboard('{Enter}');
    expect(onSelect).toHaveBeenCalledTimes(2);
  });

  it("dims a step skipped by `when` and highlights the bypass edge it actually took", () => {
    const flow: ChartFlow = { steps: [call('a'), call('release', { when: 'inputs.x' }), call('b')] };
    const steps: StepResult[] = [
      { step_id: 'a', index: 0, status: 'passed' },
      { step_id: 'release', index: 1, status: 'skipped', skip_reason: 'when' },
      { step_id: 'b', index: 2, status: 'passed' },
    ];
    const statuses = runToChartStatuses(steps);
    render(<FlowChart flow={flow} statuses={statuses} />);

    const node = screen.getByRole('button', { name: /step release, skipped/i });
    expect(node.getAttribute('class')).toContain('opacity-50');
  });

  it('shows a loop block run summary and an iteration picker that switches which iteration the nested node reflects', async () => {
    const user = userEvent.setup();
    const block: Step = { id: 'each', call: '', foreach: 'inputs.ids', steps: [call('create')] };
    const steps: StepResult[] = [
      { step_id: 'create', index: 0, status: 'passed', parent: 'each', iteration: 0 },
      { step_id: 'create', index: 1, status: 'failed', parent: 'each', iteration: 1 },
      { step_id: 'each', index: 2, status: 'failed', kind: 'foreach', count: 2 },
    ];
    const statuses = runToChartStatuses(steps);
    render(<FlowChart flow={{ steps: [block] }} statuses={statuses} />);

    // Defaults to the first failed iteration (iteration 2 of 2, 1-based).
    expect(screen.getByText('iteration 2 of 2')).toBeInTheDocument();
    expect(screen.getByText(/1 passed/)).toBeInTheDocument();
    expect(screen.getByText(/1 failed/)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'previous iteration' }));
    expect(screen.getByText('iteration 1 of 2')).toBeInTheDocument();
  });
});
