import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AddServiceForm } from '../pages/services/AddServiceForm';
import { ApiClientError } from '../api/client';
import type { Service } from '../api/types';

const addedService: Service = {
  id: 'orders',
  name: 'orders',
  status: 'ok',
  source: { type: 'local', path: '/repo/orders' },
  package_dir: '/repo/orders/api',
  operation_count: 1,
};

const add = vi.fn(async (..._args: unknown[]): Promise<Service> => addedService);
const addFromCheckout = vi.fn(async (..._args: unknown[]): Promise<Service> => addedService);

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client');
  return {
    ApiClientError: actual.ApiClientError,
    services: {
      add: (...args: unknown[]) => add(...args),
      addFromCheckout: (...args: unknown[]) => addFromCheckout(...args),
    },
  };
});

beforeEach(() => {
  add.mockClear().mockResolvedValue(addedService);
  addFromCheckout.mockClear().mockResolvedValue(addedService);
});

async function openForm(user: ReturnType<typeof userEvent.setup>) {
  render(<AddServiceForm onAdded={vi.fn()} />);
  await user.click(screen.getByRole('button', { name: 'Add service' }));
}

describe('AddServiceForm', () => {
  it('a local path defaults to "also commit as the team source" and submits via addFromCheckout', async () => {
    const user = userEvent.setup();
    await openForm(user);

    await user.type(screen.getByPlaceholderText('local path or git URL'), '/home/dev/code/orders');
    expect(screen.getByRole('checkbox', { name: /also commit as the team's git source/i })).toBeChecked();

    await user.click(screen.getByRole('button', { name: 'Add' }));
    await waitFor(() => expect(addFromCheckout).toHaveBeenCalledWith({ name: undefined, path: '/home/dev/code/orders', ref: undefined }));
    expect(add).not.toHaveBeenCalled();
  });

  it('unchecking the team-source checkbox submits via the plain local add instead', async () => {
    const user = userEvent.setup();
    await openForm(user);

    await user.type(screen.getByPlaceholderText('local path or git URL'), '/home/dev/code/orders');
    await user.click(screen.getByRole('checkbox', { name: /also commit as the team's git source/i }));
    await user.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() =>
      expect(add).toHaveBeenCalledWith({ name: '', source: { type: 'local', path: '/home/dev/code/orders', contract: undefined } }),
    );
    expect(addFromCheckout).not.toHaveBeenCalled();
  });

  it('a git URL never shows the team-source checkbox and adds directly', async () => {
    const user = userEvent.setup();
    await openForm(user);

    await user.type(screen.getByPlaceholderText('local path or git URL'), 'https://github.com/acme/orders.git');
    expect(screen.queryByRole('checkbox', { name: /also commit as the team's git source/i })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Add' }));
    await waitFor(() => expect(add).toHaveBeenCalled());
    expect(addFromCheckout).not.toHaveBeenCalled();
  });

  it('a "not a git checkout" refusal (details.local_add) offers only "Add as local path"', async () => {
    const user = userEvent.setup();
    addFromCheckout.mockRejectedValueOnce(
      new ApiClientError('E_INVALID', '/home/dev/code/orders is not a git checkout with an origin.', { details: { local_add: true } }),
    );
    await openForm(user);

    await user.type(screen.getByPlaceholderText('local path or git URL'), '/home/dev/code/orders');
    await user.click(screen.getByRole('button', { name: 'Add' }));

    expect(await screen.findByText('/home/dev/code/orders is not a git checkout with an origin.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add as local path' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Commit repository with subdir' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Add as local path' }));
    await waitFor(() =>
      expect(add).toHaveBeenCalledWith({ name: '', source: { type: 'local', path: '/home/dev/code/orders', contract: undefined } }),
    );
  });

  it('a subdirectory refusal (details.local_add) offers both follow-ups, and the subdir retry passes allow_subdir', async () => {
    const user = userEvent.setup();
    addFromCheckout.mockRejectedValueOnce(
      new ApiClientError('E_INVALID', 'This path is inside a repository but not its root.', { details: { local_add: true } }),
    );
    await openForm(user);

    await user.type(screen.getByPlaceholderText('local path or git URL'), '/home/dev/monorepo/api');
    await user.click(screen.getByRole('button', { name: 'Add' }));

    expect(await screen.findByText('This path is inside a repository but not its root.')).toBeInTheDocument();
    const subdirButton = screen.getByRole('button', { name: 'Commit repository with subdir' });
    expect(screen.getByRole('button', { name: 'Add as local path' })).toBeInTheDocument();

    await user.click(subdirButton);
    await waitFor(() =>
      expect(addFromCheckout).toHaveBeenCalledWith({ name: undefined, path: '/home/dev/monorepo/api', ref: undefined, allow_subdir: true }),
    );
  });
});
