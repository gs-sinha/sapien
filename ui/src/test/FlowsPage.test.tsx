import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import FlowsPage from '../pages/FlowsPage';
import type { FlowSummary } from '../api/types';

const sampleFlows: FlowSummary[] = [
  {
    id: 'qcom-order',
    name: 'QCOM order',
    path: 'flows/qcom-order.flow.yaml',
    owner_kind: 'workspace',
    step_count: 3,
    operations: ['qcom.createOrder', 'qcom.allocate'],
    tags: ['qcom'],
    hash: 'abc',
    updated: '2026-01-01T00:00:00Z',
  },
  {
    id: 'billing-refund',
    name: 'Billing refund',
    path: 'flows/billing-refund.flow.yaml',
    owner_kind: 'workspace',
    step_count: 2,
    operations: ['billing.refund'],
    tags: ['billing'],
    hash: 'def',
    updated: '2026-01-02T00:00:00Z',
  },
  {
    id: 'scratch-allocate',
    name: 'Scratch allocate',
    path: 'local/flows/scratch-allocate.flow.yaml',
    owner_kind: 'local',
    step_count: 1,
    operations: ['qcom.allocate'],
    hash: 'ghi',
    updated: '2026-01-03T00:00:00Z',
  },
  {
    id: 'qcom-smoke',
    name: 'QCOM smoke',
    path: '/home/me/code/qcom/api/flows/qcom-smoke.flow.yaml',
    owner_kind: 'service',
    owner_id: 'qcom',
    step_count: 1,
    operations: ['qcom.ping'],
    hash: 'jkl',
    updated: '2026-01-04T00:00:00Z',
  },
];

vi.mock('../api/client', () => ({
  flows: {
    list: vi.fn(async (): Promise<FlowSummary[]> => sampleFlows),
  },
}));

describe('FlowsPage', () => {
  it('renders the flow list from the API', async () => {
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );

    expect(screen.getByText(/loading/i)).toBeInTheDocument();

    await waitFor(() => expect(screen.getByText('qcom-order')).toBeInTheDocument());
    expect(screen.getByText('QCOM order')).toBeInTheDocument();
    expect(screen.getByText('billing-refund')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'qcom-order' })).toHaveAttribute('href', '/ui/flows/qcom-order');
  });

  it('filters the list by the filter box', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('qcom-order')).toBeInTheDocument());

    const filter = screen.getByPlaceholderText(/filter by id/i);
    await user.type(filter, 'billing');

    expect(screen.getByText('billing-refund')).toBeInTheDocument();
    expect(screen.queryByText('qcom-order')).not.toBeInTheDocument();
  });

  it('shows each flow\'s tier: local, team, or service:<owner>', async () => {
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('qcom-order')).toBeInTheDocument());

    const rowOf = (id: string) => screen.getByRole('link', { name: id }).closest('tr')!;
    expect(rowOf('qcom-order')).toHaveTextContent('team');
    expect(rowOf('scratch-allocate')).toHaveTextContent('local');
    expect(rowOf('qcom-smoke')).toHaveTextContent('service:qcom');
  });

  it('tier chips hide a tier and compose with the text filter', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('qcom-order')).toBeInTheDocument());

    // Every tier is on until one is toggled off.
    const team = screen.getByRole('button', { name: 'Team' });
    expect(team).toHaveAttribute('aria-pressed', 'true');
    await user.click(team);
    expect(team).toHaveAttribute('aria-pressed', 'false');

    expect(screen.queryByText('qcom-order')).not.toBeInTheDocument();
    expect(screen.queryByText('billing-refund')).not.toBeInTheDocument();
    expect(screen.getByText('scratch-allocate')).toBeInTheDocument();
    expect(screen.getByText('qcom-smoke')).toBeInTheDocument();

    // The text filter narrows what the chips left.
    await user.type(screen.getByPlaceholderText(/filter by id/i), 'smoke');
    expect(screen.getByText('qcom-smoke')).toBeInTheDocument();
    expect(screen.queryByText('scratch-allocate')).not.toBeInTheDocument();

    // Toggling the tier back on brings its flows back, still under the text filter.
    await user.click(team);
    expect(screen.queryByText('qcom-order')).not.toBeInTheDocument();
    expect(screen.getByText('qcom-smoke')).toBeInTheDocument();
  });
});
