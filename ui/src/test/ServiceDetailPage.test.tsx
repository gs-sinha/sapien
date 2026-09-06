import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import ServiceDetailPage from '../pages/ServiceDetailPage';
import type { Environment, Operation, Service } from '../api/types';

vi.mock('../api/client', () => ({
  services: {
    get: vi.fn(async (): Promise<Service> => ({
      id: 'orders',
      name: 'orders',
      description: 'Order management',
      owners: ['team-orders'],
      status: 'ok',
      source: { type: 'local', path: '/repo/orders' },
      package_dir: '/repo/orders/api',
      operation_count: 1,
      environments: {
        stage: { base_url: 'https://stage.orders.internal' },
        prod: { base_url: 'https://orders.internal' },
      },
    })),
    sync: vi.fn(),
    remove: vi.fn(),
  },
  operations: {
    search: vi.fn(async (): Promise<Operation[]> => [
      {
        id: 'orders.createOrder',
        service_id: 'orders',
        protocol: 'http',
        http: { method: 'POST', path: '/v1/orders' },
        summary: 'Create an order',
        source: { file: 'contract.yaml' },
        hash: 'h1',
      },
    ]),
  },
  environments: {
    list: vi.fn(async (): Promise<Environment[]> => [{ version: 1, name: 'stage', production: false }]),
  },
  docs: {
    list: vi.fn(async () => []),
    get: vi.fn(),
  },
  findDocSection: vi.fn(),
}));

describe('ServiceDetailPage', () => {
  it('shows environments the service declares that are missing from the workspace', async () => {
    render(
      <MemoryRouter initialEntries={['/ui/services/orders']}>
        <Routes>
          <Route path="/ui/services/:name" element={<ServiceDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByRole('heading', { name: 'orders' })).toBeInTheDocument());

    // stage is declared and defined in the workspace.
    expect(screen.getByText('stage')).toBeInTheDocument();
    // prod is declared but missing from the workspace's environments.
    expect(screen.getByText('prod')).toBeInTheDocument();
    expect(screen.getByText(/not defined in this workspace/i)).toBeInTheDocument();
    expect(screen.getByText(/sapien env scaffold/i)).toBeInTheDocument();

    expect(screen.getByRole('link', { name: 'orders.createOrder' })).toHaveAttribute('href', '/ui/operations/orders.createOrder');
  });
});
