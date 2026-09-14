import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import MemoriesPage from '../pages/MemoriesPage';
import { useRepo } from '../state/repo';
import { useToasts } from '../state/toast';
import type { Memory, RepoStatus } from '../api/types';

function memory(overrides: Partial<Memory>): Memory {
  return {
    id: 'mem_local',
    type: 'note',
    scope: 'workspace',
    subject: { service: 'qcom' },
    source: { kind: 'agent' },
    status: 'active',
    created: '2026-01-01T00:00:00Z',
    updated: '2026-01-02T00:00:00Z',
    text: 'first line\nsecond line',
    ...overrides,
  };
}

const sampleMemories: Memory[] = [
  memory({ id: 'mem_personal', scope: 'personal', tier: undefined }),
  memory({ id: 'mem_local', tier: 'local' }),
  memory({ id: 'mem_untracked', tier: 'workspace', shipped: 'untracked' }),
  memory({ id: 'mem_unpushed', tier: 'workspace', shipped: 'unpushed' }),
  memory({ id: 'mem_shipped', tier: 'workspace', shipped: 'shipped' }),
  memory({ id: 'mem_service', tier: 'service' }),
];

const memoriesList = vi.fn(async (_params?: unknown): Promise<Memory[]> => sampleMemories);
const memoriesMove = vi.fn(async (id: string, tier: 'local' | 'workspace'): Promise<Memory> => memory({ id, tier }));
const memoriesCommit = vi.fn(async (id: string, _message?: string): Promise<Memory> => memory({ id, tier: 'workspace', shipped: 'unpushed' }));
const repoPush = vi.fn(
  async (): Promise<RepoStatus> => ({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pushed: true, pushed_count: 1 }),
);

vi.mock('../api/client', () => ({
  memories: {
    list: (params?: unknown) => memoriesList(params),
    search: vi.fn(),
    move: (id: string, tier: 'local' | 'workspace') => memoriesMove(id, tier),
    commit: (id: string, message?: string) => memoriesCommit(id, message),
  },
  repo: {
    push: () => repoPush(),
  },
}));

beforeEach(() => {
  memoriesList.mockClear().mockResolvedValue(sampleMemories);
  memoriesMove.mockClear();
  memoriesCommit.mockClear();
  repoPush.mockClear();
  useToasts.setState({ toasts: [] });
  useRepo.setState({ status: null, started: false });
});

function renderPage() {
  return render(
    <MemoryRouter>
      <MemoriesPage />
    </MemoryRouter>,
  );
}

const rowOf = (id: string) => screen.getByRole('link', { name: id }).closest('tr')!;

describe('MemoriesPage tier column', () => {
  it('shows local/team/service tiers and leaves a personal memory blank', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('mem_local')).toBeInTheDocument());

    expect(rowOf('mem_local')).toHaveTextContent('local');
    expect(rowOf('mem_untracked')).toHaveTextContent('team');
    expect(rowOf('mem_service')).toHaveTextContent('service');
    // Personal memory: no tier text (local/team/service) anywhere in its row.
    for (const text of ['local', 'team', 'service']) {
      expect(rowOf('mem_personal')).not.toHaveTextContent(text);
    }
  });

  it('shows the Shipped badge and Commit only for workspace-tier rows', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('mem_local')).toBeInTheDocument());

    expect(rowOf('mem_untracked')).toHaveTextContent('not committed');
    expect(within(rowOf('mem_untracked')).getByRole('button', { name: 'Commit' })).toBeInTheDocument();
    expect(rowOf('mem_unpushed')).toHaveTextContent('committed, not pushed');
    expect(within(rowOf('mem_unpushed')).queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();
    expect(rowOf('mem_shipped')).toHaveTextContent('shipped');

    // Local, service, and personal rows never carry a shipped badge at all.
    for (const id of ['mem_local', 'mem_service', 'mem_personal']) {
      for (const text of ['not committed', 'modified', 'committed, not pushed', 'shipped']) {
        expect(rowOf(id)).not.toHaveTextContent(text);
      }
    }
  });

  it('shows a Push button only for the unpushed row, and it pushes via repo.push', async () => {
    const user = userEvent.setup();
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 0, ahead: 1, dirty: 0 } });
    renderPage();
    await waitFor(() => expect(screen.getByText('mem_local')).toBeInTheDocument());

    expect(within(rowOf('mem_unpushed')).getByRole('button', { name: /Push 1 commits/ })).toBeInTheDocument();
    expect(within(rowOf('mem_untracked')).queryByRole('button', { name: /Push/ })).not.toBeInTheDocument();
    expect(within(rowOf('mem_shipped')).queryByRole('button', { name: /Push/ })).not.toBeInTheDocument();

    await user.click(within(rowOf('mem_unpushed')).getByRole('button', { name: /Push 1 commits/ }));
    await waitFor(() => expect(repoPush).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'pushed 1 commits')).toBe(true),
    );
  });

  it('committing an untracked row calls memories.commit and reloads the list', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('mem_untracked')).toBeInTheDocument());

    memoriesList.mockResolvedValueOnce(sampleMemories.map((m) => (m.id === 'mem_untracked' ? { ...m, shipped: 'unpushed' } : m)));
    await user.click(within(rowOf('mem_untracked')).getByRole('button', { name: 'Commit' }));

    await waitFor(() => expect(memoriesCommit).toHaveBeenCalledWith('mem_untracked', undefined));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'committed mem_untracked; not pushed')).toBe(
        true,
      ),
    );
  });

  it('the move control offers "Move to team" for local and "Move to local" for workspace, and calls memories.move', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('mem_local')).toBeInTheDocument());

    expect(within(rowOf('mem_local')).getByRole('button', { name: 'Move to team' })).toBeInTheDocument();
    expect(within(rowOf('mem_unpushed')).getByRole('button', { name: 'Move to local' })).toBeInTheDocument();
    // A personal memory and a service-tier one offer no move control at all.
    expect(within(rowOf('mem_personal')).queryByRole('button', { name: /Move to/ })).not.toBeInTheDocument();
    expect(within(rowOf('mem_service')).queryByRole('button', { name: /Move to/ })).not.toBeInTheDocument();

    await user.click(within(rowOf('mem_local')).getByRole('button', { name: 'Move to team' }));
    await waitFor(() => expect(memoriesMove).toHaveBeenCalledWith('mem_local', 'workspace'));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'moved to team')).toBe(true),
    );
  });

  it('shows an error toast when a move fails', async () => {
    const user = userEvent.setup();
    memoriesMove.mockRejectedValueOnce(new Error('workspace is not a git repository'));
    renderPage();
    await waitFor(() => expect(screen.getByText('mem_local')).toBeInTheDocument());

    await user.click(within(rowOf('mem_local')).getByRole('button', { name: 'Move to team' }));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'error' && t.message === 'workspace is not a git repository')).toBe(
        true,
      ),
    );
  });
});
