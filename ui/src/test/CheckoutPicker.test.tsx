import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CheckoutPicker } from '../pages/services/CheckoutPicker';
import type { DirListing, Service } from '../api/types';

const home: DirListing = {
  path: '/home/dev',
  entries: [{ name: 'code', path: '/home/dev/code', matches: false }],
};

const codeDir: DirListing = {
  path: '/home/dev/code',
  parent: '/home/dev',
  entries: [
    {
      name: 'orders',
      path: '/home/dev/code/orders',
      matches: true,
      checkout: {
        path: '/home/dev/code/orders',
        branch: 'main',
        committed_at: new Date(Date.now() - 3600_000).toISOString(),
        ahead: 2,
        behind: 1,
        dirty: 3,
      },
    },
    {
      name: 'orders-fork',
      path: '/home/dev/code/orders-fork',
      matches: false,
      reason: 'clone of git@github.com:someoneelse/orders.git',
      checkout: { path: '/home/dev/code/orders-fork', branch: 'main' },
    },
  ],
};

const boundService: Service = {
  id: 'orders',
  name: 'orders',
  status: 'ok',
  source: { type: 'git', url: 'git@github.com:acme/orders.git' },
  package_dir: '/home/dev/code/orders/api',
  operation_count: 3,
};

const browseCheckouts = vi.fn(async (_name: string, path?: string): Promise<DirListing> => (path === '/home/dev/code' ? codeDir : home));
const bind = vi.fn(async (_name: string, _path: string, _force?: boolean): Promise<Service> => boundService);

vi.mock('../api/client', () => ({
  services: {
    browseCheckouts: (name: string, path?: string) => browseCheckouts(name, path),
    bind: (name: string, path: string, force?: boolean) => bind(name, path, force),
  },
}));

beforeEach(() => {
  browseCheckouts.mockClear();
  bind.mockClear().mockResolvedValue(boundService);
});

describe('CheckoutPicker', () => {
  it('starts at the home directory with Up disabled, and descends into a plain directory on click', async () => {
    const user = userEvent.setup();
    render(<CheckoutPicker service="orders" onBound={vi.fn()} onClose={vi.fn()} />);

    await waitFor(() => expect(screen.getByRole('button', { name: 'code' })).toBeInTheDocument());
    expect(browseCheckouts).toHaveBeenCalledWith('orders', undefined);
    expect(screen.getByRole('button', { name: 'Up' })).toBeDisabled();

    await user.click(screen.getByRole('button', { name: 'code' }));
    await waitFor(() => expect(browseCheckouts).toHaveBeenCalledWith('orders', '/home/dev/code'));
  });

  it('renders a git entry with its annotation line, a matches mark, and a clone-of reason for a non-matching repo', async () => {
    const user = userEvent.setup();
    render(<CheckoutPicker service="orders" onBound={vi.fn()} onClose={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'code' }));

    expect(await screen.findByText('branch main · last commit 1 hour ago · +2/-1 vs team · 3 uncommitted')).toBeInTheDocument();
    expect(screen.getByText('matches')).toBeInTheDocument();
    expect(screen.getByText('clone of git@github.com:someoneelse/orders.git')).toBeInTheDocument();

    // The non-matching repo has no Use button until "bind anyway" is checked.
    expect(screen.getAllByRole('button', { name: 'Use this checkout' })).toHaveLength(1);
  });

  it('Use this checkout binds the matching entry\'s path with force unchecked', async () => {
    const user = userEvent.setup();
    const onBound = vi.fn();
    render(<CheckoutPicker service="orders" onBound={onBound} onClose={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'code' }));

    await user.click(await screen.findByRole('button', { name: 'Use this checkout' }));
    await waitFor(() => expect(bind).toHaveBeenCalledWith('orders', '/home/dev/code/orders', false));
    await waitFor(() => expect(onBound).toHaveBeenCalledWith(boundService));
  });

  it('the "bind anyway" checkbox enables Use on the non-matching entry and passes force: true', async () => {
    const user = userEvent.setup();
    render(<CheckoutPicker service="orders" onBound={vi.fn()} onClose={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'code' }));

    await user.click(screen.getByRole('checkbox', { name: /bind anyway/i }));
    const useButtons = await screen.findAllByRole('button', { name: 'Use this checkout' });
    expect(useButtons).toHaveLength(2);

    await user.click(useButtons[1]);
    await waitFor(() => expect(bind).toHaveBeenCalledWith('orders', '/home/dev/code/orders-fork', true));
  });

  it('Up navigates to the parent directory', async () => {
    const user = userEvent.setup();
    render(<CheckoutPicker service="orders" onBound={vi.fn()} onClose={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'code' }));

    const upButton = await screen.findByRole('button', { name: 'Up' });
    expect(upButton).toBeEnabled();
    await user.click(upButton);
    await waitFor(() => expect(browseCheckouts).toHaveBeenCalledWith('orders', '/home/dev'));
    expect(await screen.findByRole('button', { name: 'Up' })).toBeDisabled();
  });
});
