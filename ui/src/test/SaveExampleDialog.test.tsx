import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { SaveExampleDialog } from '../components/run/SaveExampleDialog';
import type { StepResult } from '../api/types';

const fromRun = vi.fn(async (..._args: unknown[]) => ({
  version: 1,
  id: 'allocate-example',
  operation: 'qcom.allocate',
  scope: 'workspace',
  service: 'qcom',
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
}));

vi.mock('../api/client', () => ({
  examples: {
    fromRun: (...args: unknown[]) => fromRun(...args),
  },
}));

const step: StepResult = {
  step_id: 'allocate',
  index: 1,
  operation: 'qcom.allocate',
  status: 'passed',
};

describe('SaveExampleDialog', () => {
  it('posts the run id, step id, id, scope, description, and tags', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();

    render(<SaveExampleDialog runId="run_1" step={step} onClose={vi.fn()} onSaved={onSaved} />);

    const idInput = screen.getByLabelText(/id/i);
    await user.clear(idInput);
    await user.type(idInput, 'allocate-example');
    await user.type(screen.getByLabelText(/description/i), 'A working allocate call');
    await user.type(screen.getByLabelText(/tags/i), 'qcom, allocate');

    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(fromRun).toHaveBeenCalledWith({
      run_id: 'run_1',
      step_id: 'allocate',
      id: 'allocate-example',
      description: 'A working allocate call',
      scope: 'workspace',
      tags: ['qcom', 'allocate'],
    });
    expect(onSaved).toHaveBeenCalledWith('allocate-example');
  });
});
