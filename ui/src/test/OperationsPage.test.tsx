import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import OperationsPage from '../pages/OperationsPage';
import type { ContextBundle, Operation, SearchResult } from '../api/types';

const listOp: Operation = {
  id: 'orders.listOrders',
  service_id: 'orders',
  protocol: 'http',
  http: { method: 'GET', path: '/v1/orders' },
  summary: 'List orders',
  source: { file: 'contract.yaml' },
  hash: 'h1',
};

const searchHit: SearchResult = {
  operation: {
    id: 'orders.createOrder',
    service_id: 'orders',
    protocol: 'http',
    http: { method: 'POST', path: '/v1/orders' },
    summary: 'Create an order',
    source: { file: 'contract.yaml' },
    hash: 'h2',
  },
  score: 0.87,
  matched_on: ['summary'],
};

const bundle: ContextBundle = {
  intent: 'allocate a rider to this order',
  operations: [
    {
      tier: 'contract',
      id: 'orders.createOrder',
      method: 'POST',
      path: '/v1/orders',
      summary: 'Create an order',
      score: 0.9,
    },
  ],
  docs: [{ tier: 'documentation', service: 'orders', path: 'docs/allocation.md', heading: 'Allocation', body: 'Allocate riders here.', uri: 'sapien://x' }],
  examples: [{ id: 'ex1', operation: 'orders.createOrder', verified: true, env: 'stage' }],
  memories: [{ tier: 'memory', id: 'mem_1', type: 'gotcha', scope: 'workspace', source: 'agent', subject: {}, text: 'Watch out for timeouts.' }],
  flows: [],
  runs: [],
  estimated_tokens: 123,
};

vi.mock('../api/client', () => ({
  operations: {
    search: vi.fn(async (params: { query?: string }): Promise<SearchResult[] | Operation[]> => {
      if (params.query) return [searchHit];
      return [listOp];
    }),
  },
  buildContext: vi.fn(async (): Promise<ContextBundle> => bundle),
}));

describe('OperationsPage', () => {
  it('defaults to keyword/list mode', async () => {
    render(
      <MemoryRouter initialEntries={['/ui/operations']}>
        <OperationsPage />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('orders.listOrders')).toBeInTheDocument());
    expect(screen.getByText(/keyword mode/i)).toBeInTheDocument();
  });

  it('switches to intent mode and renders bundle tiers when the search text looks like an intent', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/ui/operations']}>
        <OperationsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('orders.listOrders')).toBeInTheDocument());

    const input = screen.getByPlaceholderText(/keyword, or an intent/i);
    await user.clear(input);
    await user.type(input, 'allocate a rider to this order{Enter}');

    await waitFor(() => expect(screen.getByText(/intent mode/i)).toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole('link', { name: /orders\.createOrder/ })).toBeInTheDocument());
    expect(screen.getByText(/allocation/i)).toBeInTheDocument();
    expect(screen.getByText('ex1')).toBeInTheDocument();
    expect(screen.getByText(/watch out for timeouts/i)).toBeInTheDocument();
  });
});
