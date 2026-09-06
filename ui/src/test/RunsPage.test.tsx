import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import RunsPage from '../pages/RunsPage';
import type { Run } from '../api/types';

function makeRun(overrides: Partial<Run>): Run {
  return {
    id: 'run_1',
    environment: 'stage',
    status: 'passed',
    started: '2026-01-01T00:00:00Z',
    summary: { steps_total: 2, steps_passed: 2, steps_failed: 0, steps_errored: 0, steps_skipped: 0, assertions: 2, assertions_failed: 0 },
    ...overrides,
  };
}

const sampleRuns: Run[] = [
  makeRun({ id: 'run_1', flow_id: 'flowA', status: 'passed', duration_ms: 120 }),
  makeRun({ id: 'run_2', flow_id: 'flowB', status: 'failed', duration_ms: 340 }),
];

vi.mock('../api/client', () => ({
  runs: {
    list: vi.fn(async (): Promise<Run[]> => sampleRuns),
  },
}));

describe('RunsPage', () => {
  it('renders runs with status, flow, and duration', async () => {
    render(
      <MemoryRouter>
        <RunsPage />
      </MemoryRouter>,
    );

    // Row links carry the flow id in their accessible name; "flowA" alone
    // is ambiguous because it also appears as a filter <option>.
    await waitFor(() => expect(screen.getByRole('link', { name: /flowA/ })).toBeInTheDocument());
    expect(screen.getByRole('link', { name: /flowB/ })).toBeInTheDocument();
    expect(screen.getByText('120 ms')).toBeInTheDocument();
    expect(screen.getByText('340 ms')).toBeInTheDocument();
  });

  it('filters by status', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <RunsPage />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByRole('link', { name: /flowA/ })).toBeInTheDocument());

    await user.selectOptions(screen.getByDisplayValue('All statuses'), 'failed');

    expect(screen.queryByRole('link', { name: /flowA/ })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /flowB/ })).toBeInTheDocument();
  });
});
