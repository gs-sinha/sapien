import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkspacePicker } from '../components/WorkspacePicker';
import { currentWorkspace, useWorkspace } from '../state/workspace';
import type { WorkspaceInfo } from '../state/workspace';

const workspacesList = vi.fn(async (): Promise<WorkspaceInfo[]> => [
  { dir: '/ws/primary', name: 'primary', primary: true, open: true, services: 3 },
  { dir: '/ws/platform', name: 'platform', open: false, services: 7 },
]);

vi.mock('../api/client', () => ({
  workspacesApi: {
    list: () => workspacesList(),
    register: vi.fn(),
    // AddWorkspaceDialog imports these through the picker; unused here but
    // the whole module is replaced, so they must exist for the import chain.
    create: vi.fn(),
    clone: vi.fn(),
  },
  fsApi: { dirs: vi.fn(async () => ({ path: '/home/dev', entries: [] })) },
  getRecentEvents: vi.fn(async () => []),
}));

const reload = vi.fn();

beforeEach(() => {
  localStorage.clear();
  useWorkspace.setState({ current: '', list: [] });
  useWorkspace.getState().select('');
  // jsdom's location.reload is not implemented; the picker calls it after a
  // switch because every page's data belongs to the previous workspace.
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { ...window.location, reload },
  });
});

afterEach(() => {
  workspacesList.mockClear();
  reload.mockClear();
});

describe('WorkspacePicker', () => {
  it('shows the current workspace and lists the alternatives', async () => {
    render(<WorkspacePicker />);

    await waitFor(() => expect(screen.getByText('primary')).toBeInTheDocument());
    // Collapsed: only the current one is shown.
    expect(screen.queryByText('platform')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /primary/ }));

    expect(screen.getByText('platform')).toBeInTheDocument();
    expect(screen.getByText('7 services')).toBeInTheDocument();
  });

  it('switching sets the workspace every later request is sent with', async () => {
    render(<WorkspacePicker />);
    await waitFor(() => expect(screen.getByText('primary')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /primary/ }));
    fireEvent.click(screen.getByText('platform'));

    // The client reads this on every request (api/client.ts), and it
    // survives a reload.
    expect(currentWorkspace()).toBe('/ws/platform');
    expect(localStorage.getItem('sapien:workspace')).toBe('/ws/platform');
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('selects the primary as "" so the tab follows the daemon, not a path', async () => {
    useWorkspace.getState().select('/ws/platform');
    render(<WorkspacePicker />);
    await waitFor(() => expect(screen.getByText('platform')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /platform/ }));
    fireEvent.click(screen.getByText('primary'));

    expect(currentWorkspace()).toBe('');
  });

  it('drops a stored selection the daemon no longer offers', async () => {
    useWorkspace.getState().select('/ws/deleted');
    render(<WorkspacePicker />);

    await waitFor(() => expect(screen.getByText('primary')).toBeInTheDocument());
    // Otherwise every request would keep naming a workspace that 404s.
    expect(currentWorkspace()).toBe('');
  });

  it('refuses to switch to a workspace that failed to load', async () => {
    workspacesList.mockResolvedValueOnce([
      { dir: '/ws/primary', name: 'primary', primary: true, open: true, services: 3 },
      { dir: '/ws/broken', name: 'broken', error: 'workspace file not found' },
    ]);
    render(<WorkspacePicker />);
    await waitFor(() => expect(screen.getByText('primary')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /primary/ }));
    fireEvent.click(screen.getByText('broken'));

    expect(currentWorkspace()).toBe('');
    expect(reload).not.toHaveBeenCalled();
  });

  it('opens the Add workspace dialog from the dropdown', async () => {
    render(<WorkspacePicker />);
    await waitFor(() => expect(screen.getByText('primary')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /primary/ }));
    fireEvent.click(screen.getByText('Add workspace…'));

    expect(screen.getByRole('dialog', { name: 'Add workspace' })).toBeInTheDocument();
  });

  it('stays quiet when the daemon cannot list workspaces', async () => {
    workspacesList.mockRejectedValueOnce(new Error('E_NETWORK'));
    render(<WorkspacePicker />);

    // No error in the nav, and nothing to switch to.
    await waitFor(() => expect(screen.getByText('workspace')).toBeInTheDocument());
    expect(screen.getByRole('button', { name: /workspace/ })).toBeDisabled();
  });
});
