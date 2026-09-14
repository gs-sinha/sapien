import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ServiceDetailPage from '../pages/ServiceDetailPage';
import type { BindingInfo, Environment, Operation, Service, ServiceBinding } from '../api/types';

const localService: Service = {
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
  binding: { mode: 'local', local: { path: '/repo/orders', branch: 'main' }, writable: true },
};

const teamSource = { type: 'git', url: 'git@github.com:acme/orders.git', ref: 'main' } as const;
const teamBinding: ServiceBinding = { mode: 'team', team: teamSource, writable: false };
const teamService: Service = {
  ...localService,
  source: teamSource,
  package_dir: '/ws/.sapien/clones/orders/api',
  commit: 'abcdef1234567890',
  binding: teamBinding,
};
const overrideBinding: ServiceBinding = {
  mode: 'local',
  team: teamSource,
  local: { path: '/home/me/code/orders', branch: 'feat/allocation', commit: '1234567890abcdef', dirty: 2 },
  writable: true,
};
const overrideService: Service = { ...localService, package_dir: '/home/me/code/orders/api', binding: overrideBinding };

const servicesGet = vi.fn(async (_name: string): Promise<Service> => localService);
const servicesBinding = vi.fn(async (_name: string): Promise<BindingInfo> => ({ service: 'orders', binding: localService.binding! }));
const servicesBind = vi.fn(async (_name: string, _path: string): Promise<Service> => overrideService);
const servicesUnbind = vi.fn(async (_name: string): Promise<Service> => teamService);

vi.mock('../api/client', () => ({
  services: {
    get: (name: string) => servicesGet(name),
    binding: (name: string) => servicesBinding(name),
    bind: (name: string, path: string) => servicesBind(name, path),
    unbind: (name: string) => servicesUnbind(name),
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

beforeEach(() => {
  servicesGet.mockReset().mockResolvedValue(localService);
  servicesBinding.mockReset().mockResolvedValue({ service: 'orders', binding: localService.binding! });
  servicesBind.mockReset().mockResolvedValue(overrideService);
  servicesUnbind.mockReset().mockResolvedValue(teamService);
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/ui/services/orders']}>
      <Routes>
        <Route path="/ui/services/:name" element={<ServiceDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

function useTeamMode(candidates: BindingInfo['candidates'] = []) {
  servicesGet.mockResolvedValue(teamService);
  servicesBinding.mockResolvedValue({ service: 'orders', binding: teamBinding, candidates });
}

describe('ServiceDetailPage', () => {
  it('shows environments the service declares that are missing from the workspace', async () => {
    renderPage();

    await waitFor(() => expect(screen.getByRole('heading', { name: 'orders' })).toBeInTheDocument());

    // stage is declared and defined in the workspace.
    expect(screen.getByText('stage')).toBeInTheDocument();
    // prod is declared but missing from the workspace's environments.
    expect(screen.getByText('prod')).toBeInTheDocument();
    expect(screen.getByText(/not defined in this workspace/i)).toBeInTheDocument();
    expect(screen.getByText(/sapien env scaffold/i)).toBeInTheDocument();

    expect(screen.getByRole('link', { name: 'orders.createOrder' })).toHaveAttribute('href', '/ui/operations/orders.createOrder');
  });

  it('a plain local source: says what it reads and offers nothing to switch to', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Source' })).toBeInTheDocument());

    expect(screen.getByText('local · /repo/orders · branch main')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /read from/i })).not.toBeInTheDocument();
    expect(screen.queryByText(/read-only/)).not.toBeInTheDocument();
  });

  it('team mode: shows the team source, the read-only note, and binds a candidate checkout in one click', async () => {
    const user = userEvent.setup();
    useTeamMode([{ path: '/home/me/code/orders', branch: 'feat/allocation', commit: '1234567890abcdef', dirty: 2 }]);
    renderPage();
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Source' })).toBeInTheDocument());

    expect(screen.getByText('team · git@github.com:acme/orders.git @ main (abcdef1)')).toBeInTheDocument();
    expect(screen.getByText(/read-only while orders is read from the team source/)).toBeInTheDocument();

    // GET /binding's candidates are one click each, showing what git says about them.
    const candidate = await screen.findByRole('button', { name: 'local · /home/me/code/orders · branch feat/allocation · 1234567 · 2 uncommitted here' });
    expect(servicesBinding).toHaveBeenCalledWith('orders');
    await user.click(candidate);

    await waitFor(() => expect(servicesBind).toHaveBeenCalledWith('orders', '/home/me/code/orders'));
    // The page reloads so the header and the binding reflect the switch.
    await waitFor(() => expect(servicesGet.mock.calls.length).toBeGreaterThanOrEqual(2));
  });

  it('team mode: binds a typed path (a ~ path is passed through as-is)', async () => {
    const user = userEvent.setup();
    useTeamMode();
    renderPage();
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Source' })).toBeInTheDocument());

    const button = screen.getByRole('button', { name: 'Read from checkout' });
    expect(button).toBeDisabled();
    await user.type(screen.getByRole('textbox', { name: 'Local checkout path' }), '~/code/orders');
    expect(button).toBeEnabled();
    await user.click(button);

    await waitFor(() => expect(servicesBind).toHaveBeenCalledWith('orders', '~/code/orders'));
  });

  it('local override: shows the checkout, offers the team source back, and unbinds', async () => {
    const user = userEvent.setup();
    servicesGet.mockResolvedValue(overrideService);
    servicesBinding.mockResolvedValue({ service: 'orders', binding: overrideBinding });
    renderPage();
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Source' })).toBeInTheDocument());

    const section = screen.getByRole('heading', { name: 'Source' }).closest('section')!;
    expect(within(section).getByText('local · /home/me/code/orders · branch feat/allocation · 1234567 · 2 uncommitted here')).toBeInTheDocument();
    expect(within(section).getByText('team · git@github.com:acme/orders.git @ main')).toBeInTheDocument();
    expect(screen.queryByText(/read-only/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Read from checkout' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Read from team source' }));
    await waitFor(() => expect(servicesUnbind).toHaveBeenCalledWith('orders'));
  });
});
