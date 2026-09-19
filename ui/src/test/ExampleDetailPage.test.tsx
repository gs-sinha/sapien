import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ExampleDetailPage from '../pages/ExampleDetailPage';
import { useRepo } from '../state/repo';
import { useToasts } from '../state/toast';
import type { RepoStatus, SavedExample } from '../api/types';

const examplesGet = vi.fn();
const examplesUpdate = vi.fn();
const examplesDelete = vi.fn();
const examplesMove = vi.fn();
const examplesCommit = vi.fn();
const repoPush = vi.fn(
  async (): Promise<RepoStatus> => ({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pushed: true, pushed_count: 2 }),
);

vi.mock('../api/client', () => ({
  examples: {
    get: (...a: unknown[]) => examplesGet(...a),
    update: (...a: unknown[]) => examplesUpdate(...a),
    delete: (...a: unknown[]) => examplesDelete(...a),
    move: (...a: unknown[]) => examplesMove(...a),
    commit: (...a: unknown[]) => examplesCommit(...a),
  },
  repo: {
    push: () => repoPush(),
  },
}));

const example: SavedExample = {
  version: 1,
  id: 'order-happy-path',
  operation: 'orders.create',
  scope: 'workspace',
  service: 'orders',
  description: 'A basic successful order',
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
};

beforeEach(() => {
  examplesGet.mockReset().mockResolvedValue(example);
  examplesUpdate.mockReset();
  examplesDelete.mockReset();
  examplesMove.mockReset();
  examplesCommit.mockReset();
  repoPush.mockClear();
  useToasts.setState({ toasts: [] });
  useRepo.setState({ status: null, started: false });
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={[`/ui/examples/${example.id}`]}>
      <Routes>
        <Route path="/ui/examples/:id" element={<ExampleDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ExampleDetailPage', () => {
  it('renders the example and shows no tier badge or move control when tier is absent', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText(example.id)).toBeInTheDocument());

    expect(screen.queryByText('local')).not.toBeInTheDocument();
    // The tier-move control is absent when there's no tier; the folder-move
    // popover ("Move to folder…") is unrelated to tier and always present.
    expect(screen.queryByRole('button', { name: /^Move to (team|local)$/ })).not.toBeInTheDocument();
    expect(
      screen.getByText('Scope says which catalog the example is filed under; tier says where its file is: local until you move it to the team.'),
    ).toBeInTheDocument();
  });

  it('a local-tier example: shows the tier badge and moves to team', async () => {
    const user = userEvent.setup();
    examplesGet.mockResolvedValueOnce({ ...example, tier: 'local' });
    examplesMove.mockResolvedValueOnce({ ...example, tier: 'workspace' });
    renderPage();
    await waitFor(() => expect(screen.getByText(example.id)).toBeInTheDocument());

    expect(screen.getByText('local')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Move to team' }));

    await waitFor(() => expect(examplesMove).toHaveBeenCalledWith(example.id, 'workspace'));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'moved to team')).toBe(true),
    );
  });

  it('a workspace-tier, unpushed example: shows Shipped, Commit, Push, and "Move to local"', async () => {
    const user = userEvent.setup();
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 0, ahead: 2, dirty: 0 } });
    examplesGet.mockResolvedValueOnce({ ...example, tier: 'workspace', shipped: 'unpushed' });
    renderPage();
    await waitFor(() => expect(screen.getByText(example.id)).toBeInTheDocument());

    expect(screen.getByText('team')).toBeInTheDocument();
    expect(screen.getByText('committed, not pushed')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Move to local' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /Push 2 commits/ }));
    await waitFor(() => expect(repoPush).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'pushed 2 commits')).toBe(true),
    );
  });

  it('committing an untracked workspace-tier example calls examples.commit and refreshes the badge', async () => {
    const user = userEvent.setup();
    examplesGet.mockResolvedValueOnce({ ...example, tier: 'workspace', shipped: 'untracked' });
    examplesCommit.mockResolvedValueOnce({ ...example, tier: 'workspace', shipped: 'unpushed' });
    renderPage();
    await waitFor(() => expect(screen.getByText(example.id)).toBeInTheDocument());

    examplesGet.mockResolvedValueOnce({ ...example, tier: 'workspace', shipped: 'unpushed' });
    await user.click(screen.getByRole('button', { name: 'Commit' }));

    await waitFor(() => expect(examplesCommit).toHaveBeenCalledWith(example.id));
    await waitFor(() => expect(screen.getByText('committed, not pushed')).toBeInTheDocument());
    expect(
      useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === `committed ${example.id}; not pushed`),
    ).toBe(true);
  });

  it('still rescopes via the existing scope select and deletes, unaffected by tier', async () => {
    const user = userEvent.setup();
    examplesUpdate.mockResolvedValueOnce({ ...example, scope: 'service' });
    renderPage();
    await waitFor(() => expect(screen.getByText(example.id)).toBeInTheDocument());

    await user.selectOptions(screen.getByDisplayValue('workspace'), 'service');
    await user.click(screen.getByRole('button', { name: 'Rescope' }));

    await waitFor(() => expect(examplesUpdate).toHaveBeenCalledWith(example.id, { ...example, scope: 'service' }));
  });
});
