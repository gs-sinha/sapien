import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AddWorkspaceDialog } from '../components/AddWorkspaceDialog';
import { useToasts } from '../state/toast';
import type { FSDirListing } from '../api/types';
import type { WorkspaceInfo } from '../state/workspace';

function ws(dir: string): WorkspaceInfo {
  return { dir, name: dir.split('/').pop() || dir };
}

const create = vi.fn(async (dir: string, _name: string, _gitInit: boolean): Promise<WorkspaceInfo> => ws(dir));
const clone = vi.fn(async (_url: string, dir: string, _name: string): Promise<WorkspaceInfo> => ws(dir));
const register = vi.fn(async (dir: string): Promise<WorkspaceInfo> => ws(dir));

// Home holds one child; descending into it opens /home/dev/code, whose own
// listing has a parent so Up can be exercised.
const dirs = vi.fn(async (path?: string): Promise<FSDirListing> =>
  path ? { path, parent: '/home/dev', entries: [] } : { path: '/home/dev', entries: [{ name: 'code', path: '/home/dev/code' }] },
);

vi.mock('../api/client', () => ({
  workspacesApi: {
    create: (dir: string, name: string, gitInit: boolean) => create(dir, name, gitInit),
    clone: (url: string, dir: string, name: string) => clone(url, dir, name),
    register: (dir: string) => register(dir),
  },
  fsApi: { dirs: (path?: string) => dirs(path) },
  getRecentEvents: vi.fn(async () => []),
}));

beforeEach(() => {
  create.mockClear();
  clone.mockClear();
  register.mockClear();
  dirs.mockClear();
  useToasts.setState({ toasts: [] });
});

// Descend into the home listing's "code" child and select it as the open
// directory, the way a caller picks a parent folder or an existing checkout.
async function pickCode(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'Browse…' }));
  await user.click(await screen.findByRole('button', { name: 'code' }));
  await user.click(await screen.findByRole('button', { name: 'Select this folder' }));
}

describe('AddWorkspaceDialog', () => {
  it('creates a new workspace at the chosen parent with the given name', async () => {
    const user = userEvent.setup();
    const onAdded = vi.fn();
    render(<AddWorkspaceDialog onClose={vi.fn()} onAdded={onAdded} />);

    await pickCode(user);
    await user.type(screen.getByLabelText('Name'), 'orders');
    await user.click(screen.getByRole('checkbox', { name: /initialize a git repository/i }));
    await user.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() => expect(create).toHaveBeenCalledWith('/home/dev/code/orders', 'orders', true));
    expect(onAdded).toHaveBeenCalledWith('/home/dev/code/orders');
  });

  it('clones a repository, deriving the name from the URL when blank', async () => {
    const user = userEvent.setup();
    const onAdded = vi.fn();
    render(<AddWorkspaceDialog onClose={vi.fn()} onAdded={onAdded} />);

    await user.click(screen.getByRole('tab', { name: 'Clone' }));
    await user.type(screen.getByLabelText('Repository URL'), 'https://github.com/acme/orders.git');
    await pickCode(user);
    await user.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() => expect(clone).toHaveBeenCalledWith('https://github.com/acme/orders.git', '/home/dev/code/orders', ''));
    expect(onAdded).toHaveBeenCalledWith('/home/dev/code/orders');
  });

  it('registers an existing folder', async () => {
    const user = userEvent.setup();
    const onAdded = vi.fn();
    render(<AddWorkspaceDialog onClose={vi.fn()} onAdded={onAdded} />);

    await user.click(screen.getByRole('tab', { name: 'Existing' }));
    await pickCode(user);
    await user.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() => expect(register).toHaveBeenCalledWith('/home/dev/code'));
    expect(onAdded).toHaveBeenCalledWith('/home/dev/code');
  });

  it('surfaces a failed call as an error toast', async () => {
    const user = userEvent.setup();
    create.mockRejectedValueOnce(new Error('directory already exists'));
    render(<AddWorkspaceDialog onClose={vi.fn()} onAdded={vi.fn()} />);

    await pickCode(user);
    await user.type(screen.getByLabelText('Name'), 'orders');
    await user.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'error' && t.message === 'directory already exists')).toBe(true),
    );
  });
});
