import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
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
  matched_on: ['task:allocate-rider'],
  tasks: [{ id: 'allocate-rider', phrase: 'allocate a rider', when: 'the order is ready for dispatch' }],
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

const { searchOperations, buildContext } = vi.hoisted(() => ({
  searchOperations: vi.fn(),
  buildContext: vi.fn(),
}));

vi.mock('../api/client', () => ({
  operations: {
    search: searchOperations,
  },
  buildContext,
}));

describe('OperationsPage', () => {
  beforeEach(() => {
    searchOperations.mockReset();
    searchOperations.mockImplementation(async (params: { query?: string }): Promise<SearchResult[] | Operation[]> => {
      if (params.query) return [searchHit];
      return [listOp];
    });
    buildContext.mockReset();
    buildContext.mockImplementation(async (): Promise<ContextBundle> => bundle);
  });

  it('defaults to keyword/list mode', async () => {
    render(
      <MemoryRouter initialEntries={['/ui/operations']}>
        <OperationsPage />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('orders.listOrders')).toBeInTheDocument());
    expect(screen.getByText(/search mode/i)).toBeInTheDocument();
  });

  it('uses ranked operation search for natural-language intent text', async () => {
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

    await waitFor(() => expect(screen.getAllByRole('link', { name: /orders\.createOrder/ })).not.toHaveLength(0));
    expect(searchOperations).toHaveBeenLastCalledWith({ query: 'allocate a rider to this order', service: undefined, method: undefined });
    expect(buildContext).not.toHaveBeenCalled();
    expect(screen.getByText('task: allocate-rider')).toBeInTheDocument();
    expect(screen.getByText('allocate a rider')).toBeInTheDocument();
    expect(screen.getByText('When: the order is ready for dispatch')).toBeInTheDocument();
    const results = screen.getByRole('region', { name: 'Operation search results' });
    expect(results).toHaveClass('overflow-y-auto');
    expect(results).toContainElement(screen.getByText('allocate a rider'));
    expect(results).toContainElement(screen.getByText('Create an order'));
  });

  it('shows only the top 10 task matches', async () => {
    searchOperations.mockResolvedValueOnce([
      {
        ...searchHit,
        tasks: Array.from({ length: 12 }, (_, index) => ({
          id: `task-${index + 1}`,
          phrase: `Task phrase ${index + 1}`,
        })),
      },
    ]);

    render(
      <MemoryRouter initialEntries={['/ui/operations?q=dispatch']}>
        <OperationsPage />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('Task phrase 10')).toBeInTheDocument());
    expect(screen.queryByText('Task phrase 11')).not.toBeInTheDocument();
    expect(screen.queryByText('Task phrase 12')).not.toBeInTheDocument();
  });

  it('builds the broader context bundle only after the explicit action', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/ui/operations?q=allocate+a+rider+to+this+order']}>
        <OperationsPage />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getAllByRole('link', { name: /orders\.createOrder/ })).not.toHaveLength(0));
    await user.click(screen.getByRole('button', { name: 'Build context' }));

    await waitFor(() => expect(screen.getByText(/context mode/i)).toBeInTheDocument());
    await waitFor(() => expect(buildContext).toHaveBeenCalledWith({ intent: 'allocate a rider to this order', budget_tokens: 6000 }));
    expect(screen.getByText(/allocation/i)).toBeInTheDocument();
    expect(screen.getByText('ex1')).toBeInTheDocument();
    expect(screen.getByText(/watch out for timeouts/i)).toBeInTheDocument();
  });
});
