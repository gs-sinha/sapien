import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ExamplesPage from '../pages/ExamplesPage';
import type { SavedExample } from '../api/types';

const examplesList = vi.fn();

vi.mock('../api/client', () => ({
  examples: {
    list: (...a: unknown[]) => examplesList(...a),
  },
}));

const orderExample: SavedExample = {
  version: 1,
  id: 'order-happy-path',
  operation: 'orders.create',
  scope: 'workspace',
  service: 'orders',
  description: 'A basic successful order',
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
};

const billingExample: SavedExample = {
  version: 1,
  id: 'billing-refund-partial',
  operation: 'billing.refund',
  scope: 'service',
  service: 'billing',
  description: 'Partial refund',
  verified: { at: '2026-01-01T00:00:00Z', env: 'stage' },
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
};

beforeEach(() => {
  examplesList.mockReset().mockResolvedValue([orderExample, billingExample]);
});

describe('ExamplesPage', () => {
  it('lists saved examples', async () => {
    render(
      <MemoryRouter>
        <ExamplesPage />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('order-happy-path')).toBeInTheDocument());
    expect(screen.getByText('billing-refund-partial')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'order-happy-path' })).toHaveAttribute('href', '/ui/examples/order-happy-path');
  });

  it('refetches with the service filter applied', async () => {
    render(
      <MemoryRouter>
        <ExamplesPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(examplesList).toHaveBeenCalledTimes(1));
    expect(examplesList).toHaveBeenLastCalledWith({ service: undefined, operation: undefined, tag: undefined, text: undefined });

    examplesList.mockResolvedValueOnce([billingExample]);
    fireEvent.change(screen.getByPlaceholderText('service'), { target: { value: 'billing' } });

    await waitFor(() => expect(examplesList).toHaveBeenLastCalledWith({ service: 'billing', operation: undefined, tag: undefined, text: undefined }));
    await waitFor(() => expect(screen.queryByText('order-happy-path')).not.toBeInTheDocument());
    expect(screen.getByText('billing-refund-partial')).toBeInTheDocument();
  });

  it('refetches with the operation, tag, and text filters applied', async () => {
    render(
      <MemoryRouter>
        <ExamplesPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(examplesList).toHaveBeenCalledTimes(1));

    fireEvent.change(screen.getByPlaceholderText('operation'), { target: { value: 'orders.create' } });
    await waitFor(() => expect(examplesList).toHaveBeenLastCalledWith({ service: undefined, operation: 'orders.create', tag: undefined, text: undefined }));

    fireEvent.change(screen.getByPlaceholderText('tag'), { target: { value: 'smoke' } });
    await waitFor(() =>
      expect(examplesList).toHaveBeenLastCalledWith({ service: undefined, operation: 'orders.create', tag: 'smoke', text: undefined }),
    );

    fireEvent.change(screen.getByPlaceholderText('search text'), { target: { value: 'refund' } });
    await waitFor(() =>
      expect(examplesList).toHaveBeenLastCalledWith({ service: undefined, operation: 'orders.create', tag: 'smoke', text: 'refund' }),
    );
  });

  it('shows a filtered empty state when nothing matches', async () => {
    render(
      <MemoryRouter>
        <ExamplesPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('order-happy-path')).toBeInTheDocument());

    examplesList.mockResolvedValueOnce([]);
    fireEvent.change(screen.getByPlaceholderText('search text'), { target: { value: 'nothing-matches-this' } });

    await waitFor(() => expect(screen.getByText(/no examples match these filters/i)).toBeInTheDocument());
  });
});
