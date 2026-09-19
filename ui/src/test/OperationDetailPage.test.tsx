import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import OperationDetailPage from '../pages/OperationDetailPage';
import { buildContext, docs } from '../api/client';
import type { Field, Memory, Operation, SavedExample } from '../api/types';

const op: Operation = {
  id: 'orders.createOrder',
  service_id: 'orders',
  protocol: 'http',
  http: { method: 'POST', path: '/v1/orders' },
  summary: 'Create an order',
  description: 'Creates a new order for a customer.',
  tags: ['orders'],
  params: [{ name: 'idempotencyKey', in: 'header', required: true, description: 'Dedup key', schema: { kind: 'string' } }],
  request_body: { content_type: 'application/json', required: true },
  source: { file: 'contract.yaml' },
  hash: 'h1',
};

const fields: Field[] = [
  { operation_id: op.id, path: 'request.body.customerId', type: 'string', required: true },
  { operation_id: op.id, path: 'response.200.body.orderId', type: 'string' },
];

vi.mock('../api/client', () => ({
  operations: {
    get: vi.fn(async (): Promise<Operation> => op),
    fields: vi.fn(async (): Promise<Field[]> => fields),
  },
  examples: {
    list: vi.fn(async (): Promise<SavedExample[]> => []),
  },
  memories: {
    list: vi.fn(async (): Promise<Memory[]> => []),
  },
  docs: {
    get: vi.fn(),
  },
  buildContext: vi.fn(),
}));

describe('OperationDetailPage', () => {
  afterEach(() => {
    vi.mocked(buildContext).mockReset();
    vi.mocked(docs.get).mockReset();
  });

  it('renders parameters, request/response fields, and a Try it link', async () => {
    vi.mocked(buildContext).mockResolvedValue({
      intent: op.id,
      operations: [],
      docs: [],
      memories: [],
      flows: [],
      runs: [],
      estimated_tokens: 0,
    });

    render(
      <MemoryRouter initialEntries={['/ui/operations/orders.createOrder']}>
        <Routes>
          <Route path="/ui/operations/:id" element={<OperationDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByRole('heading', { name: 'orders.createOrder' })).toBeInTheDocument());

    expect(screen.getByText('idempotencyKey')).toBeInTheDocument();
    expect(screen.getByText('customerId', { exact: false })).toBeInTheDocument();
    expect(screen.getByText('orderId', { exact: false })).toBeInTheDocument();

    const tryIt = screen.getAllByRole('link', { name: /try it/i })[0];
    expect(tryIt).toHaveAttribute('href', '/ui/try/orders.createOrder');
  });

  it('falls back to the context bundle snippet, with a muted note, when the full-text doc fetch fails', async () => {
    vi.mocked(buildContext).mockResolvedValue({
      intent: op.id,
      operations: [],
      docs: [
        {
          tier: 'documentation',
          service: 'orders',
          path: 'contract#tag:Orders',
          heading: 'Creating an order',
          body: 'Short token-budget-truncated snippet about creating an order.',
          uri: 'sapien://services/orders/docs/contract#tag:Orders#creating-an-order',
        },
      ],
      memories: [],
      flows: [],
      runs: [],
      estimated_tokens: 0,
    });
    // The full-text fetch 404s (a contract-embedded doc path that doesn't
    // resolve on its own), so the page must fall back to the bundle's own
    // snippet instead of throwing or showing an error line.
    vi.mocked(docs.get).mockRejectedValue(new Error('not found'));

    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/ui/operations/orders.createOrder']}>
        <Routes>
          <Route path="/ui/operations/:id" element={<OperationDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    // The Docs section is a Collapsible, default collapsed (PLAN §34f item
    // 4), with the doc's own heading shown truncated in its closed summary.
    const docsToggle = await screen.findByRole('button', { name: 'Docs (1)' });
    expect(docsToggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('Creating an order')).toBeInTheDocument();
    await user.click(docsToggle);

    await waitFor(() => expect(screen.getByText(/Short token-budget-truncated snippet/)).toBeInTheDocument());
    expect(screen.getByText(/Full text unavailable/)).toBeInTheDocument();
  });
});
