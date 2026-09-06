import { act, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import RunDetailPage from '../pages/RunDetailPage';
import { useEvents } from '../state/events';
import type { Run } from '../api/types';
import type { Hint } from '../api/types-runs';

const runningRun: Run = {
  id: 'run_1',
  flow_id: 'qcom-order',
  environment: 'stage',
  status: 'running',
  started: '2026-01-01T00:00:00Z',
  summary: { steps_total: 2, steps_passed: 0, steps_failed: 0, steps_errored: 0, steps_skipped: 0, assertions: 0, assertions_failed: 0 },
  steps: [{ step_id: 'create', index: 0, operation: 'qcom.createOrder', status: 'requesting' }],
};

const finishedRun: Run = {
  ...runningRun,
  status: 'failed',
  finished: '2026-01-01T00:00:05Z',
  duration_ms: 5000,
  summary: { steps_total: 2, steps_passed: 1, steps_failed: 1, steps_errored: 0, steps_skipped: 0, assertions: 2, assertions_failed: 1 },
  steps: [
    { step_id: 'create', index: 0, operation: 'qcom.createOrder', status: 'passed' },
    {
      step_id: 'allocate',
      index: 1,
      operation: 'qcom.allocate',
      status: 'failed',
      request: { method: 'POST', url: 'https://api.example.com/allocate' },
      response: { status: 409, body: { code: 'E_INSUFFICIENT_FUNDS' }, size: 40 },
      assertions: [{ expr: 'status == 200', passed: false }],
    },
  ],
};

const sampleHints: Hint[] = [
  { kind: 'memory', step_id: 'allocate', title: 'mem_1: riders need a funded wallet first', score: 0.9, ref: { memory_id: 'mem_1' } },
];

const getRun = vi.fn();

vi.mock('../api/client', () => ({
  runs: {
    get: (...args: unknown[]) => getRun(...args),
    pin: vi.fn(async () => undefined),
  },
}));

vi.mock('../api/flowsExtra', () => ({
  getRunHints: vi.fn(async (): Promise<Hint[]> => sampleHints),
  rerunStepAlone: vi.fn(),
}));

beforeEach(() => {
  getRun.mockReset();
  // jsdom has no scrollIntoView implementation at all.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('RunDetailPage', () => {
  it('renders steps, expands the failed one by default, shows hints, and refetches on run.step', async () => {
    getRun.mockResolvedValueOnce(runningRun).mockResolvedValueOnce(finishedRun);

    render(
      <MemoryRouter initialEntries={['/ui/runs/run_1']}>
        <Routes>
          <Route path="/ui/runs/:id" element={<RunDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('qcom.createOrder')).toBeInTheDocument());
    expect(getRun).toHaveBeenCalledTimes(1);

    act(() => {
      useEvents.getState()._append({
        type: 'run.step',
        time: '2026-01-01T00:00:01Z',
        summary: '',
        ids: { run_id: 'run_1', step_id: 'allocate' },
      });
    });

    await waitFor(() => expect(getRun).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByText('qcom.allocate')).toBeInTheDocument());

    // The failed step is expanded by default: its response body is visible
    // without needing to click it open.
    expect(screen.getByText(/E_INSUFFICIENT_FUNDS/)).toBeInTheDocument();

    // The "might explain it" hints panel shows the memory hint with a link
    // to the memory detail route.
    await waitFor(() => expect(screen.getByText(/riders need a funded wallet first/)).toBeInTheDocument());
    expect(screen.getByRole('link', { name: /riders need a funded wallet first/ })).toHaveAttribute('href', '/ui/memories/mem_1');
  });
});
