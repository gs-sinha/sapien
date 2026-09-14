import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import FlowsPage from '../pages/FlowsPage';
import { flows } from '../api/client';
import { useToasts } from '../state/toast';
import type { FlowSummary } from '../api/types';

const sampleFlows: FlowSummary[] = [
  {
    id: 'qcom-order',
    name: 'QCOM order',
    path: 'flows/qcom-order.flow.yaml',
    owner_kind: 'workspace',
    step_count: 3,
    operations: ['qcom.createOrder', 'qcom.allocate'],
    tags: ['qcom'],
    hash: 'abc',
    updated: '2026-01-01T00:00:00Z',
  },
  {
    id: 'billing-refund',
    name: 'Billing refund',
    path: 'flows/billing-refund.flow.yaml',
    owner_kind: 'workspace',
    step_count: 2,
    operations: ['billing.refund'],
    tags: ['billing'],
    hash: 'def',
    updated: '2026-01-02T00:00:00Z',
  },
  {
    id: 'scratch-allocate',
    name: 'Scratch allocate',
    path: 'local/flows/scratch-allocate.flow.yaml',
    owner_kind: 'local',
    step_count: 1,
    operations: ['qcom.allocate'],
    hash: 'ghi',
    updated: '2026-01-03T00:00:00Z',
  },
  {
    id: 'qcom-smoke',
    name: 'QCOM smoke',
    path: '/home/me/code/qcom/api/flows/qcom-smoke.flow.yaml',
    owner_kind: 'service',
    owner_id: 'qcom',
    step_count: 1,
    operations: ['qcom.ping'],
    hash: 'jkl',
    updated: '2026-01-04T00:00:00Z',
  },
];

const flowsCommit = vi.fn(async (_id: string, _message?: string): Promise<FlowSummary> => ({
  id: 'flow-a',
  path: 'flows/flow-a.flow.yaml',
  owner_kind: 'workspace',
  step_count: 1,
  hash: 'h',
  updated: '2026-01-05T00:00:00Z',
  shipped: 'unpushed',
}));

vi.mock('../api/client', () => ({
  flows: {
    list: vi.fn(async (): Promise<FlowSummary[]> => sampleFlows),
    commit: (id: string, message?: string) => flowsCommit(id, message),
  },
}));

beforeEach(() => {
  flowsCommit.mockClear();
  useToasts.setState({ toasts: [] });
});

describe('FlowsPage', () => {
  it('renders the flow list from the API', async () => {
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );

    expect(screen.getByText(/loading/i)).toBeInTheDocument();

    await waitFor(() => expect(screen.getByText('qcom-order')).toBeInTheDocument());
    expect(screen.getByText('QCOM order')).toBeInTheDocument();
    expect(screen.getByText('billing-refund')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'qcom-order' })).toHaveAttribute('href', '/ui/flows/qcom-order');
  });

  it('filters the list by the filter box', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('qcom-order')).toBeInTheDocument());

    const filter = screen.getByPlaceholderText(/filter by id/i);
    await user.type(filter, 'billing');

    expect(screen.getByText('billing-refund')).toBeInTheDocument();
    expect(screen.queryByText('qcom-order')).not.toBeInTheDocument();
  });

  it('shows each flow\'s tier: local, team, or service:<owner>', async () => {
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('qcom-order')).toBeInTheDocument());

    const rowOf = (id: string) => screen.getByRole('link', { name: id }).closest('tr')!;
    expect(rowOf('qcom-order')).toHaveTextContent('team');
    expect(rowOf('scratch-allocate')).toHaveTextContent('local');
    expect(rowOf('qcom-smoke')).toHaveTextContent('service:qcom');
  });

  it('tier chips hide a tier and compose with the text filter', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('qcom-order')).toBeInTheDocument());

    // Every tier is on until one is toggled off.
    const team = screen.getByRole('button', { name: 'Team' });
    expect(team).toHaveAttribute('aria-pressed', 'true');
    await user.click(team);
    expect(team).toHaveAttribute('aria-pressed', 'false');

    expect(screen.queryByText('qcom-order')).not.toBeInTheDocument();
    expect(screen.queryByText('billing-refund')).not.toBeInTheDocument();
    expect(screen.getByText('scratch-allocate')).toBeInTheDocument();
    expect(screen.getByText('qcom-smoke')).toBeInTheDocument();

    // The text filter narrows what the chips left.
    await user.type(screen.getByPlaceholderText(/filter by id/i), 'smoke');
    expect(screen.getByText('qcom-smoke')).toBeInTheDocument();
    expect(screen.queryByText('scratch-allocate')).not.toBeInTheDocument();

    // Toggling the tier back on brings its flows back, still under the text filter.
    await user.click(team);
    expect(screen.queryByText('qcom-order')).not.toBeInTheDocument();
    expect(screen.getByText('qcom-smoke')).toBeInTheDocument();
  });

  it('shows a Shipped badge for each workspace-tier ship state, and none for local/service tiers or an absent field', async () => {
    vi.mocked(flows.list).mockResolvedValueOnce([
      { ...sampleFlows[0], id: 'flow-a', shipped: 'untracked' },
      { ...sampleFlows[0], id: 'flow-b', shipped: 'modified' },
      { ...sampleFlows[0], id: 'flow-c', shipped: 'unpushed' },
      { ...sampleFlows[0], id: 'flow-d', shipped: 'shipped' },
      sampleFlows[2], // local tier, no `shipped` at all
      sampleFlows[3], // service tier, no `shipped` at all
    ]);
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('flow-a')).toBeInTheDocument());

    const rowOf = (id: string) => screen.getByRole('link', { name: id }).closest('tr')!;
    expect(rowOf('flow-a')).toHaveTextContent('not committed');
    expect(rowOf('flow-b')).toHaveTextContent('modified');
    expect(rowOf('flow-c')).toHaveTextContent('committed, not pushed');
    expect(rowOf('flow-d')).toHaveTextContent('shipped');

    // Local and service tiers never carry `shipped`, so no badge text at all.
    for (const text of ['not committed', 'modified', 'committed, not pushed', 'shipped']) {
      expect(rowOf('scratch-allocate')).not.toHaveTextContent(text);
      expect(rowOf('qcom-smoke')).not.toHaveTextContent(text);
    }
  });

  it('a Commit button appears only for untracked/modified rows, and committing refreshes the row', async () => {
    const user = userEvent.setup();
    vi.mocked(flows.list).mockResolvedValueOnce([
      { ...sampleFlows[0], id: 'flow-a', shipped: 'untracked' },
      { ...sampleFlows[0], id: 'flow-c', shipped: 'unpushed' },
      { ...sampleFlows[0], id: 'flow-d', shipped: 'shipped' },
      sampleFlows[2], // local tier
    ]);
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('flow-a')).toBeInTheDocument());

    const rowOf = (id: string) => screen.getByRole('link', { name: id }).closest('tr')!;
    expect(within(rowOf('flow-a')).getByRole('button', { name: 'Commit' })).toBeInTheDocument();
    expect(within(rowOf('flow-c')).queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();
    expect(within(rowOf('flow-d')).queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();
    expect(within(rowOf('scratch-allocate')).queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();

    // Clicking Commit reloads the list; the row now reports "unpushed".
    vi.mocked(flows.list).mockResolvedValueOnce([{ ...sampleFlows[0], id: 'flow-a', shipped: 'unpushed' }]);
    await user.click(within(rowOf('flow-a')).getByRole('button', { name: 'Commit' }));

    await waitFor(() => expect(flowsCommit).toHaveBeenCalledWith('flow-a', undefined));
    await waitFor(() => expect(screen.getByText('committed, not pushed')).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();
    expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'committed flow-a; not pushed')).toBe(true);
  });

  it('shows an error toast when the commit fails', async () => {
    const user = userEvent.setup();
    vi.mocked(flows.list).mockResolvedValueOnce([{ ...sampleFlows[0], id: 'flow-a', shipped: 'modified' }]);
    flowsCommit.mockRejectedValueOnce(new Error('workspace is not a git repository'));
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('flow-a')).toBeInTheDocument());

    await user.click(screen.getByRole('button', { name: 'Commit' }));

    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'error' && t.message === 'workspace is not a git repository')).toBe(true),
    );
    // The row is untouched: still "modified", still offering Commit.
    expect(screen.getByText('modified')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Commit' })).toBeInTheDocument();
  });
});
