import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { StatusBar } from '../components/StatusBar';
import { useRepo } from '../state/repo';
import { useToasts } from '../state/toast';
import type { RepoStatus, Workspace } from '../api/types';

const getWorkspace = vi.fn(async (): Promise<Workspace> => ({ version: 1, name: 'acme', dir: '/ws', file: 'sapien.workspace.yaml' }));
const repoPull = vi.fn(
  async (): Promise<RepoStatus> => ({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pulled: true, pulled_count: 1 }),
);
const repoPush = vi.fn(
  async (): Promise<RepoStatus> => ({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pushed: true, pushed_count: 4 }),
);

// Only getWorkspace, repo.pull, and repo.push are overridden: StatusBar
// calls nothing else on api/client (the repo status shown here comes
// straight from the store, seeded elsewhere by App.tsx).
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client');
  return {
    ...actual,
    getWorkspace: () => getWorkspace(),
    repo: { ...actual.repo, pull: () => repoPull(), push: () => repoPush() },
  };
});

beforeEach(() => {
  getWorkspace.mockClear();
  repoPull.mockClear().mockResolvedValue({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pulled: true, pulled_count: 1 });
  repoPush.mockClear().mockResolvedValue({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pushed: true, pushed_count: 4 });
  useRepo.setState({ status: null, started: false });
  useToasts.setState({ toasts: [] });
});

async function renderBar() {
  const result = render(
    <MemoryRouter>
      <StatusBar />
    </MemoryRouter>,
  );
  // Lets StatusBar's own GET /v1/workspace mount effect settle before the
  // test's assertions run, so it can't resolve later and trip React's
  // "update not wrapped in act" warning after the test has moved on.
  await waitFor(() => expect(getWorkspace).toHaveBeenCalled());
  return result;
}

describe('StatusBar repo segment', () => {
  it('renders nothing when the workspace is not a git repository', async () => {
    useRepo.setState({ status: { in_git: false, behind: 0, ahead: 0, dirty: 0 } });
    const { container } = await renderBar();

    expect(container.textContent).not.toContain('team ·');
    expect(screen.queryByRole('button', { name: 'Pull' })).not.toBeInTheDocument();
  });

  it('renders nothing while the store has no status yet', async () => {
    const { container } = await renderBar();
    expect(container.textContent).not.toContain('team ·');
  });

  it('behind with a clean tree shows a Pull button, which pulls and updates the store', async () => {
    const user = userEvent.setup();
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 3, ahead: 0, dirty: 0 } });
    const { container } = await renderBar();

    expect(container.textContent).toContain('team · main');
    expect(container.textContent).toContain('↓3 new');
    const pullButton = screen.getByRole('button', { name: 'Pull' });

    await user.click(pullButton);

    await waitFor(() => expect(repoPull).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'pulled 1 commits')).toBe(true));
    expect(useRepo.getState().status).toEqual({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pulled: true, pulled_count: 1 });
  });

  it('behind with a dirty tree blocks the pull with a message instead of a button', async () => {
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 2, ahead: 0, dirty: 5 } });
    const { container } = await renderBar();

    expect(container.textContent).toContain('↓2 new');
    expect(container.textContent).toContain('5 uncommitted');
    expect(container.textContent).toContain('pull blocked: 5 uncommitted');
    expect(screen.queryByRole('button', { name: 'Pull' })).not.toBeInTheDocument();
  });

  it('the "N uncommitted" text links to the Changes page (PLAN §34f item 1)', async () => {
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 5 } });
    await renderBar();

    const link = screen.getByRole('link', { name: '5 uncommitted' });
    expect(link).toHaveAttribute('href', '/ui/changes');
  });

  it('ahead shows unpushed, with no Pull button since nothing is behind', async () => {
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 0, ahead: 4, dirty: 0 } });
    const { container } = await renderBar();

    expect(container.textContent).toContain('↑4 unpushed');
    expect(screen.queryByRole('button', { name: 'Pull' })).not.toBeInTheDocument();
  });

  it('ahead with nothing behind shows a Push button, which pushes and updates the store', async () => {
    const user = userEvent.setup();
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 0, ahead: 4, dirty: 0 } });
    const { container } = await renderBar();

    expect(container.textContent).toContain('↑4 unpushed');
    const pushButton = screen.getByRole('button', { name: /Push 4 commits/ });
    expect(pushButton).toHaveAttribute('title', 'pushes every unpushed commit in the workspace repository');

    await user.click(pushButton);

    await waitFor(() => expect(repoPush).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'pushed 4 commits')).toBe(true));
    expect(useRepo.getState().status).toEqual({ in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, pushed: true, pushed_count: 4 });
  });

  it('ahead while also behind blocks the push with "pull first" instead of a button', async () => {
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 2, ahead: 3, dirty: 0 } });
    const { container } = await renderBar();

    expect(container.textContent).toContain('↓2 new');
    expect(container.textContent).toContain('↑3 unpushed');
    expect(container.textContent).toContain('pull first');
    expect(screen.queryByRole('button', { name: /Push/ })).not.toBeInTheDocument();
  });

  it('shows "fetch failed" with the error as a title, alongside any other flags', async () => {
    useRepo.setState({ status: { in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, fetch_error: 'connection refused' } });
    await renderBar();

    expect(screen.getByTitle('connection refused')).toHaveTextContent('fetch failed');
  });

  it('falls back to a relative "fetched <time>" once nothing else applies', async () => {
    useRepo.setState({
      status: { in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, fetched_at: new Date(Date.now() - 5 * 60_000).toISOString() },
    });
    const { container } = await renderBar();

    expect(container.textContent).toContain('fetched 5 minutes ago');
  });
});
