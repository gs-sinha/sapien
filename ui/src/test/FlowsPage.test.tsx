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
});
