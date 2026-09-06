import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import ServicesPage from '../pages/ServicesPage';
import type { Service } from '../api/types';

vi.mock('../api/client', () => ({
  services: {
    list: vi.fn(async (): Promise<Service[]> => [
      {
        id: 'orders',
        name: 'orders',
        status: 'ok',
        source: { type: 'local', path: '/repo/orders' },
        package_dir: '/repo/orders/api',
        operation_count: 12,
        warnings: [{ code: 'W1', message: 'missing example' }],
        accepted_warnings: [{ code: 'W2', message: 'ok as-is', reason: 'documented' }],
        last_indexed: '2026-01-01T00:00:00Z',
      },
      {
        id: 'riders',
        name: 'riders',
        status: 'error',
        source: { type: 'git', url: 'git@github.com:acme/riders.git' },
        package_dir: '/repo/riders/api',
        operation_count: 4,
      },
    ]),
    add: vi.fn(),
    sync: vi.fn(),
  },
}));

describe('ServicesPage', () => {
  it('renders the services list with warning counts', async () => {
    render(
      <MemoryRouter>
        <ServicesPage />
      </MemoryRouter>,
    );

    expect(screen.getByText(/loading/i)).toBeInTheDocument();

    await waitFor(() => expect(screen.getByRole('link', { name: 'orders' })).toBeInTheDocument());
    expect(screen.getByRole('link', { name: 'orders' })).toHaveAttribute('href', '/ui/services/orders');
    expect(screen.getByRole('link', { name: 'riders' })).toBeInTheDocument();

    // orders: 1 unaccepted warning, 1 accepted warning; riders: none of either.
    const rows = screen.getAllByRole('row');
    const ordersRow = rows.find((r) => r.textContent?.includes('orders'));
    expect(ordersRow?.textContent).toContain('12'); // operation count
    expect(ordersRow).toBeTruthy();

    expect(screen.getAllByText('1').length).toBeGreaterThanOrEqual(2); // unaccepted + accepted warning counts
    expect(screen.getByRole('button', { name: /sync all/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /add service/i })).toBeInTheDocument();
  });
});
