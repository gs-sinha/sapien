import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import MemoryDetailPage from '../pages/MemoryDetailPage';
import { useRepo } from '../state/repo';
import { useToasts } from '../state/toast';
import type { Memory, PromotionTarget, RepoStatus } from '../api/types';

const memoriesGet = vi.fn();
const memoriesPromotion = vi.fn();
const memoriesPatch = vi.fn();
const memoriesMove = vi.fn();
const memoriesCommit = vi.fn();
const repoPush = vi.fn(
  async (): Promise<RepoStatus> => ({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pushed: true, pushed_count: 1 }),
);

vi.mock('../api/client', () => ({
  memories: {
    get: (...a: unknown[]) => memoriesGet(...a),
    promotion: (...a: unknown[]) => memoriesPromotion(...a),
    patch: (...a: unknown[]) => memoriesPatch(...a),
    move: (...a: unknown[]) => memoriesMove(...a),
    commit: (...a: unknown[]) => memoriesCommit(...a),
  },
  repo: {
    push: () => repoPush(),
  },
}));

const memory: Memory = {
  id: 'mem_01H0000000000000000000',
  type: 'gotcha',
  scope: 'workspace',
  subject: { operation: 'orders.allocate', service: 'orders' },
  source: { kind: 'agent', client: 'claude-code' },
  status: 'active',
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-02T00:00:00Z',
  text: 'A 409 from orders.allocate means no rider was online, not a real conflict.',
  file_path: 'memories/orders.allocate.md',
};

const target: PromotionTarget = {
  kind: 'doc',
  file: 'services/orders/docs/allocation.md',
  line: 42,
  section: 'Error codes',
  current: 'Returns 409 when the request conflicts with another update.',
  memory,
  suggested: 'Returns 409 when no rider is currently online (not a real conflict).',
};

beforeEach(() => {
  memoriesGet.mockReset().mockResolvedValue(memory);
  memoriesPromotion.mockReset().mockResolvedValue(target);
  memoriesPatch.mockReset().mockResolvedValue(memory);
  memoriesMove.mockReset();
  memoriesCommit.mockReset();
  repoPush.mockClear();
  useToasts.setState({ toasts: [] });
  useRepo.setState({ status: null, started: false });
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={[`/ui/memories/${memory.id}`]}>
      <Routes>
        <Route path="/ui/memories/:id" element={<MemoryDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('MemoryDetailPage', () => {
  it('renders the memory text and subject', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText(memory.id)).toBeInTheDocument());
    expect(screen.getByText(/no rider was online/)).toBeInTheDocument();
    expect(screen.getByText('orders.allocate')).toBeInTheDocument();
  });

  it('fetches and renders the promotion target on demand', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText(memory.id)).toBeInTheDocument());

    expect(memoriesPromotion).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: /where does this belong/i }));

    await waitFor(() => expect(memoriesPromotion).toHaveBeenCalledWith(memory.id));
    expect(await screen.findByText('services/orders/docs/allocation.md')).toBeInTheDocument();
    expect(screen.getByText(/:42/)).toBeInTheDocument();
    expect(screen.getByText('Error codes')).toBeInTheDocument();
    expect(screen.getByText(target.current!)).toBeInTheDocument();
    expect(screen.getByText(target.suggested!)).toBeInTheDocument();
  });

  it('shows no tier badge and no move control for a memory with no tier (personal scope)', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText(memory.id)).toBeInTheDocument());

    // "service" also appears as the Subject's "service" field label, so
    // only the tier-specific labels are checked here.
    for (const text of ['local', 'team']) {
      expect(screen.queryByText(text)).not.toBeInTheDocument();
    }
    // The tier-move control is absent when there's no tier; the folder-move
    // popover ("Move to folder…") is unrelated to tier and always present.
    expect(screen.queryByRole('button', { name: /^Move to (team|local)$/ })).not.toBeInTheDocument();
    expect(
      screen.getByText('Scope says who the memory is about; tier says where its file is: local until you move it to the team.'),
    ).toBeInTheDocument();
  });

  it('a local-tier memory: shows the tier badge and a "Move to team" button', async () => {
    const user = userEvent.setup();
    memoriesGet.mockResolvedValueOnce({ ...memory, tier: 'local' });
    memoriesMove.mockResolvedValueOnce({ ...memory, tier: 'workspace' });
    renderPage();
    await waitFor(() => expect(screen.getByText(memory.id)).toBeInTheDocument());

    expect(screen.getByText('local')).toBeInTheDocument();
    const moveButton = screen.getByRole('button', { name: 'Move to team' });

    await user.click(moveButton);
    await waitFor(() => expect(memoriesMove).toHaveBeenCalledWith(memory.id, 'workspace'));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'moved to team')).toBe(true),
    );
  });

  it('a workspace-tier memory: shows Shipped/Commit/Push and "Move to local"', async () => {
    const user = userEvent.setup();
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 0, ahead: 1, dirty: 0 } });
    memoriesGet.mockResolvedValueOnce({ ...memory, tier: 'workspace', shipped: 'unpushed' });
    renderPage();
    await waitFor(() => expect(screen.getByText(memory.id)).toBeInTheDocument());

    expect(screen.getByText('team')).toBeInTheDocument();
    expect(screen.getByText('committed, not pushed')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Move to local' })).toBeInTheDocument();

    const pushButton = screen.getByRole('button', { name: /Push 1 commit/ });
    await user.click(pushButton);
    await waitFor(() => expect(repoPush).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'pushed 1 commit')).toBe(true),
    );
  });

  it('committing an untracked workspace-tier memory calls memories.commit and refreshes the badge', async () => {
    const user = userEvent.setup();
    memoriesGet.mockResolvedValueOnce({ ...memory, tier: 'workspace', shipped: 'untracked' });
    memoriesCommit.mockResolvedValueOnce({ ...memory, tier: 'workspace', shipped: 'unpushed' });
    renderPage();
    await waitFor(() => expect(screen.getByText(memory.id)).toBeInTheDocument());

    memoriesGet.mockResolvedValueOnce({ ...memory, tier: 'workspace', shipped: 'unpushed' });
    await user.click(screen.getByRole('button', { name: 'Commit' }));

    await waitFor(() => expect(memoriesCommit).toHaveBeenCalledWith(memory.id));
    await waitFor(() => expect(screen.getByText('committed, not pushed')).toBeInTheDocument());
    expect(
      useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === `committed ${memory.id}; not pushed`),
    ).toBe(true);
  });
});
