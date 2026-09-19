import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ServiceRefControl } from '../pages/services/ServiceRefControl';
import type { ServiceBranches, ServiceRefOverride, SetServiceRefRequest, Source } from '../api/types';

const branchesResult: ServiceBranches = {
  current: 'main',
  default: 'main',
  branches: ['main', 'feat/allocation'],
  tags: ['v1.2.0'],
};

const branches = vi.fn(async (_id: string): Promise<ServiceBranches> => branchesResult);
const setRef = vi.fn(async (_id: string, _req: SetServiceRefRequest) => ({}));
const clearRef = vi.fn(async (_id: string) => ({}));

vi.mock('../api/client', () => ({
  serviceRefs: {
    branches: (id: string) => branches(id),
    set: (id: string, req: SetServiceRefRequest) => setRef(id, req),
    clear: (id: string) => clearRef(id),
  },
}));

const team: Source = { type: 'git', url: 'git@github.com:acme/orders.git', ref: 'main' };

beforeEach(() => {
  branches.mockReset().mockResolvedValue(branchesResult);
  setRef.mockReset().mockResolvedValue({});
  clearRef.mockReset().mockResolvedValue({});
});

describe('ServiceRefControl', () => {
  it('renders nothing for a non-git team source', () => {
    const { container } = render(
      <ServiceRefControl serviceId="orders" team={{ type: 'local', path: '/repo/orders' }} onChanged={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('shows the team ref when there is no override, and no "this machine" pill', () => {
    render(<ServiceRefControl serviceId="orders" team={team} onChanged={vi.fn()} />);
    expect(screen.getByText('main')).toBeInTheDocument();
    expect(screen.queryByText(/this machine:/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reset to team ref' })).not.toBeInTheDocument();
  });

  it('shows a "this machine" pill and reset action when a local override is active', async () => {
    const user = userEvent.setup();
    const onChanged = vi.fn();
    const override: ServiceRefOverride = { ref: 'feat/allocation', scope: 'local' };
    render(<ServiceRefControl serviceId="orders" team={team} refOverride={override} onChanged={onChanged} />);

    expect(screen.getByText('feat/allocation')).toBeInTheDocument();
    expect(screen.getByText('this machine: feat/allocation')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Reset to team ref' }));
    await waitFor(() => expect(clearRef).toHaveBeenCalledWith('orders'));
    expect(onChanged).toHaveBeenCalled();
  });

  it('does not show a reset action for a team-scope override', () => {
    const override: ServiceRefOverride = { ref: 'release/2026-09', scope: 'team' };
    render(<ServiceRefControl serviceId="orders" team={team} refOverride={override} onChanged={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Reset to team ref' })).not.toBeInTheDocument();
  });

  it('Change… opens a popover that loads branches and tags, filters them, and applies a pick', async () => {
    const user = userEvent.setup();
    const onChanged = vi.fn();
    render(<ServiceRefControl serviceId="orders" team={team} onChanged={onChanged} />);

    await user.click(screen.getByRole('button', { name: 'Change…' }));
    expect(await screen.findByRole('dialog', { name: 'Change ref' })).toBeInTheDocument();
    await waitFor(() => expect(branches).toHaveBeenCalledWith('orders'));

    expect(screen.getByRole('button', { name: /feat\/allocation/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /v1\.2\.0/ })).toBeInTheDocument();

    await user.type(screen.getByRole('textbox', { name: 'Filter branches and tags' }), 'feat');
    expect(screen.queryByRole('button', { name: /v1\.2\.0/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /feat\/allocation/ })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /feat\/allocation/ }));
    // Default scope is "Only on this machine" (local).
    expect(screen.getByLabelText('Only on this machine')).toBeChecked();
    await user.click(screen.getByRole('button', { name: 'Apply' }));

    await waitFor(() => expect(setRef).toHaveBeenCalledWith('orders', { ref: 'feat/allocation', scope: 'local' }));
    expect(onChanged).toHaveBeenCalled();
  });

  it('the "For the team" scope shows the commit-from-Changes note and applies with scope: team', async () => {
    const user = userEvent.setup();
    render(<ServiceRefControl serviceId="orders" team={team} onChanged={vi.fn()} />);

    await user.click(screen.getByRole('button', { name: 'Change…' }));
    await waitFor(() => expect(branches).toHaveBeenCalled());

    await user.click(screen.getByLabelText('For the team'));
    expect(screen.getByText(/edits sapien\.workspace\.yaml/)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /v1\.2\.0/ }));
    await user.click(screen.getByRole('button', { name: 'Apply' }));

    await waitFor(() => expect(setRef).toHaveBeenCalledWith('orders', { ref: 'v1.2.0', scope: 'team' }));
  });

  it('shows a loading state, then an error state, for the branches fetch', async () => {
    const user = userEvent.setup();
    branches.mockReset().mockRejectedValue(new Error('network unreachable'));
    render(<ServiceRefControl serviceId="orders" team={team} onChanged={vi.fn()} />);

    await user.click(screen.getByRole('button', { name: 'Change…' }));
    await waitFor(() => expect(screen.getByText('network unreachable')).toBeInTheDocument());
  });
});
