import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ExamplesPage from '../pages/ExamplesPage';
import { useRepo } from '../state/repo';
import { useToasts } from '../state/toast';
import type { RepoStatus, SavedExample } from '../api/types';

const examplesList = vi.fn();
const examplesMove = vi.fn(async (id: string, tier: 'local' | 'workspace'): Promise<SavedExample> => ({
  ...orderExample,
  id,
  tier,
}));
const examplesCommit = vi.fn(async (id: string, _message?: string): Promise<SavedExample> => ({
  ...orderExample,
  id,
  tier: 'workspace',
  shipped: 'unpushed',
}));
const repoPush = vi.fn(
  async (): Promise<RepoStatus> => ({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pushed: true, pushed_count: 1 }),
);

vi.mock('../api/client', () => ({
  examples: {
    list: (...a: unknown[]) => examplesList(...a),
    move: (id: string, tier: 'local' | 'workspace') => examplesMove(id, tier),
    commit: (id: string, message?: string) => examplesCommit(id, message),
  },
  repo: {
    push: () => repoPush(),
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
  examplesMove.mockClear();
  examplesCommit.mockClear();
  repoPush.mockClear();
  useToasts.setState({ toasts: [] });
  useRepo.setState({ status: null, started: false });
});

const rowOf = (id: string) => screen.getByRole('link', { name: id }).closest('tr')!;

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

  it('shows the tier column: blank when absent, badges + Commit + move for a workspace-tier row', async () => {
    const user = userEvent.setup();
    examplesList.mockResolvedValueOnce([
      orderExample, // no tier at all
      { ...billingExample, id: 'ex-local', tier: 'local' },
      { ...billingExample, id: 'ex-untracked', tier: 'workspace', shipped: 'untracked' },
    ]);
    render(
      <MemoryRouter>
        <ExamplesPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('ex-local')).toBeInTheDocument());

    expect(rowOf('order-happy-path')).not.toHaveTextContent('local');
    expect(within(rowOf('order-happy-path')).queryByRole('button', { name: /Move to/ })).not.toBeInTheDocument();

    expect(rowOf('ex-local')).toHaveTextContent('local');
    expect(within(rowOf('ex-local')).getByRole('button', { name: 'Move to team' })).toBeInTheDocument();

    expect(rowOf('ex-untracked')).toHaveTextContent('team');
    expect(rowOf('ex-untracked')).toHaveTextContent('not committed');
    const commitButton = within(rowOf('ex-untracked')).getByRole('button', { name: 'Commit' });

    examplesList.mockResolvedValueOnce([{ ...billingExample, id: 'ex-untracked', tier: 'workspace', shipped: 'unpushed' }]);
    await user.click(commitButton);

    await waitFor(() => expect(examplesCommit).toHaveBeenCalledWith('ex-untracked', undefined));
    await waitFor(() => expect(screen.getByText('committed, not pushed')).toBeInTheDocument());
  });

  it('shows a Push button only for an unpushed row, and moving calls examples.move', async () => {
    const user = userEvent.setup();
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 0, ahead: 1, dirty: 0 } });
    examplesList.mockResolvedValueOnce([
      { ...billingExample, id: 'ex-unpushed', tier: 'workspace', shipped: 'unpushed' },
      { ...billingExample, id: 'ex-shipped', tier: 'workspace', shipped: 'shipped' },
    ]);
    render(
      <MemoryRouter>
        <ExamplesPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('ex-unpushed')).toBeInTheDocument());

    const pushButton = within(rowOf('ex-unpushed')).getByRole('button', { name: /Push 1 commits/ });
    expect(within(rowOf('ex-shipped')).queryByRole('button', { name: /Push/ })).not.toBeInTheDocument();

    // Pushing reloads the list (onPushed); it comes back with ex-unpushed
    // now shipped too, so the next assertions still find both rows.
    examplesList.mockResolvedValueOnce([
      { ...billingExample, id: 'ex-unpushed', tier: 'workspace', shipped: 'shipped' },
      { ...billingExample, id: 'ex-shipped', tier: 'workspace', shipped: 'shipped' },
    ]);
    await user.click(pushButton);
    await waitFor(() => expect(repoPush).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'pushed 1 commits')).toBe(true),
    );

    await user.click(within(rowOf('ex-shipped')).getByRole('button', { name: 'Move to local' }));
    await waitFor(() => expect(examplesMove).toHaveBeenCalledWith('ex-shipped', 'local'));
  });
});
