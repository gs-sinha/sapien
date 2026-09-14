import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ServicesPage from '../pages/ServicesPage';
import { services } from '../api/client';
import { useRepo } from '../state/repo';
import { useToasts } from '../state/toast';
import type { RepoStatus, Service } from '../api/types';

const repoSync = vi.fn(async (): Promise<RepoStatus> => ({ in_git: false, behind: 0, ahead: 0, dirty: 0 }));

beforeEach(() => {
  repoSync.mockClear();
  useRepo.setState({ status: null, started: false });
  useToasts.setState({ toasts: [] });
});

vi.mock('../api/client', () => ({
  repo: {
    sync: () => repoSync(),
  },
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
        binding: { mode: 'local', local: { path: '/repo/orders', branch: 'main', commit: 'abc1234567' }, writable: true },
      },
      {
        id: 'riders',
        name: 'riders',
        status: 'error',
        source: { type: 'git', url: 'git@github.com:acme/riders.git', ref: 'v2' },
        package_dir: '/repo/riders/api',
        operation_count: 4,
        binding: { mode: 'team', team: { type: 'git', url: 'git@github.com:acme/riders.git', ref: 'v2' }, writable: false },
      },
      // A daemon from before bindings: no `binding`, so the label comes from the source kind alone.
      {
        id: 'billing',
        name: 'billing',
        status: 'ok',
        source: { type: 'git', url: 'git@github.com:acme/billing.git' },
        package_dir: '/repo/billing/api',
        operation_count: 2,
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

  it('shows which source each service is read from, next to its name', async () => {
    render(
      <MemoryRouter>
        <ServicesPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByRole('link', { name: 'orders' })).toBeInTheDocument());

    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent);
    expect(headers.indexOf('Reads from')).toBe(headers.indexOf('Name') + 1);

    const rowOf = (name: string) => screen.getByRole('link', { name }).closest('tr')!;
    // Bound to a checkout: the branch this machine reads.
    expect(rowOf('orders')).toHaveTextContent('local · main');
    // The committed git source: the ref every other machine reads.
    expect(rowOf('riders')).toHaveTextContent('team · v2');
    // No binding reported: the source kind alone.
    expect(rowOf('billing')).toHaveTextContent('team');
    expect(rowOf('billing')).not.toHaveTextContent('team ·');
  });

  it('appends +A/-B to the pill when a local binding is ahead or behind the team ref', async () => {
    vi.mocked(services.list).mockResolvedValueOnce([
      {
        id: 'orders',
        name: 'orders',
        status: 'ok',
        source: { type: 'local', path: '/repo/orders' },
        package_dir: '/repo/orders/api',
        operation_count: 12,
        binding: { mode: 'local', local: { path: '/repo/orders', branch: 'feat/x', ahead: 2, behind: 3 }, writable: true },
      },
    ]);
    render(
      <MemoryRouter>
        <ServicesPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByRole('link', { name: 'orders' })).toBeInTheDocument());

    const row = screen.getByRole('link', { name: 'orders' }).closest('tr')!;
    expect(row).toHaveTextContent('local · feat/x · +2/-3');
  });

  describe('Sync all', () => {
    async function syncAllAndGetToast(status: RepoStatus): Promise<string | undefined> {
      repoSync.mockResolvedValueOnce(status);
      const user = userEvent.setup();
      render(
        <MemoryRouter>
          <ServicesPage />
        </MemoryRouter>,
      );
      await waitFor(() => expect(screen.getByRole('link', { name: 'orders' })).toBeInTheDocument());

      await user.click(screen.getByRole('button', { name: /sync all/i }));
      await waitFor(() => expect(repoSync).toHaveBeenCalledTimes(1));
      await waitFor(() => expect(useToasts.getState().toasts.length).toBeGreaterThan(0));
      return useToasts.getState().toasts.at(-1)?.message;
    }

    it('adds "pulled N commits" when the repo sync pulled', async () => {
      const message = await syncAllAndGetToast({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pulled: true, pulled_count: 3 });
      expect(message).toBe('Sync started for every service; team repo: pulled 3 commits.');
      expect(useRepo.getState().status?.pulled_count).toBe(3);
    });

    it('adds "already current" when nothing needed doing', async () => {
      const message = await syncAllAndGetToast({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0 });
      expect(message).toBe('Sync started for every service; team repo: already current.');
    });

    it('adds "not pulled (<skipped>)" when the daemon declined to pull', async () => {
      const message = await syncAllAndGetToast({
        in_git: true,
        branch: 'main',
        behind: 2,
        ahead: 0,
        dirty: 1,
        skipped: 'uncommitted changes',
      });
      expect(message).toBe('Sync started for every service; team repo: not pulled (uncommitted changes).');
    });

    it('adds "fetch failed (<fetch_error>)" when the fetch itself failed', async () => {
      const message = await syncAllAndGetToast({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, fetch_error: 'connection refused' });
      expect(message).toBe('Sync started for every service; team repo: fetch failed (connection refused).');
    });

    it('adds no repo clause when the workspace is not a git repository', async () => {
      const message = await syncAllAndGetToast({ in_git: false, behind: 0, ahead: 0, dirty: 0 });
      expect(message).toBe('Sync started for every service.');
    });
  });
});
