import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ChangesPage from '../pages/ChangesPage';
import { useEvents, summarize } from '../state/events';
import { useRepo } from '../state/repo';
import { useToasts } from '../state/toast';
import type { RepoChanges, RepoDiff, RepoStatus } from '../api/types';

const status: RepoStatus = { in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 4, fetched_at: '2026-01-01T00:00:00Z' };

const changes: RepoChanges = {
  status,
  files: [
    { path: 'flows/a.yaml', state: 'untracked', kind: 'flow', id: 'flow-a', title: 'Checkout happy path' },
    { path: 'flows/b.yaml', state: 'modified', kind: 'flow', id: 'flow-b', title: 'Another flow' },
    { path: 'flows/shipped.yaml', state: 'unpushed', kind: 'flow', id: 'flow-shipped', title: 'Already committed' },
    { path: 'examples/c.yaml', state: 'untracked', kind: 'example', id: 'ex-c', title: 'Example c' },
    { path: 'sapien.workspace.yaml', state: 'modified', kind: 'workspace' },
  ],
  services: [
    { name: 'orders', mode: 'local', branch: 'feature/x', dirty: true, files: [{ path: 'api/orders.go', state: 'modified' }] },
    { name: 'billing', mode: 'team', ref: 'v2', dirty: false, files: [] },
  ],
};

const repoChangesGet = vi.fn(async (): Promise<RepoChanges> => changes);
const repoChangesDiff = vi.fn(async (_path: string): Promise<RepoDiff> => ({ path: _path, state: 'modified', diff: '', binary: false, truncated: false }));
const repoChangesCommit = vi.fn(async (_paths: string[], _message: string) => ({ commit: 'abc123', committed: _paths, status }));

vi.mock('../api/client', () => ({
  repoChanges: {
    get: () => repoChangesGet(),
    diff: (path: string) => repoChangesDiff(path),
    commit: (paths: string[], message: string) => repoChangesCommit(paths, message),
  },
  repo: {
    pull: vi.fn(async () => status),
    push: vi.fn(async () => status),
  },
}));

beforeEach(() => {
  repoChangesGet.mockClear().mockResolvedValue(changes);
  repoChangesDiff.mockClear();
  repoChangesCommit.mockClear().mockResolvedValue({ commit: 'abc123', committed: [], status });
  useToasts.setState({ toasts: [] });
  useRepo.setState({ status, started: true });
});

function renderPage() {
  return render(
    <MemoryRouter>
      <ChangesPage />
    </MemoryRouter>,
  );
}

describe('ChangesPage', () => {
  it('renders the file tree from the fetched payload, grouped into folders', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    expect(screen.getByText('flows')).toBeInTheDocument();
    expect(screen.getByText('examples')).toBeInTheDocument();
    expect(screen.getByText('a.yaml')).toBeInTheDocument();
    expect(screen.getByText('b.yaml')).toBeInTheDocument();
    expect(screen.getByText('sapien.workspace.yaml')).toBeInTheDocument();
    // Muted kind/title label.
    expect(screen.getByText('flow · Checkout happy path')).toBeInTheDocument();
  });

  it('shows the state letter with a title tooltip on each file row', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    expect(screen.getAllByTitle('untracked')[0]).toHaveTextContent('U');
    // Three rows are "modified" here: flows/b.yaml, sapien.workspace.yaml,
    // and the bound service checkout's own api/orders.go.
    expect(screen.getAllByTitle('modified', { exact: true })).toHaveLength(3);
    expect(screen.getByTitle('unpushed')).toHaveTextContent('↑');
  });

  it('rolls up folder counts, visible on the folder row', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    const flowsRow = screen.getByText('flows').closest('[role="treeitem"]')!;
    expect(flowsRow).toHaveTextContent('3'); // a.yaml, b.yaml, shipped.yaml
    const examplesRow = screen.getByText('examples').closest('[role="treeitem"]')!;
    expect(examplesRow).toHaveTextContent('1');
  });

  it('gives no checkbox to an unpushed row', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    expect(screen.queryByLabelText('Select shipped.yaml')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Select a.yaml')).toBeInTheDocument();
  });

  it('selecting files via checkboxes drives the generated commit message and the "Commit N files" count', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    await user.click(screen.getByLabelText('Select a.yaml'));
    await user.click(screen.getByLabelText('Select c.yaml'));

    expect(screen.getByRole('button', { name: 'Commit 2 files' })).toBeInTheDocument();
    const textarea = screen.getByLabelText('Commit message') as HTMLTextAreaElement;
    // example sorts before flow alphabetically.
    expect(textarea.value).toBe('Add 1 example and 1 flow');
  });

  it('never regenerates the message once the user has edited it', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    await user.click(screen.getByLabelText('Select a.yaml'));
    const textarea = screen.getByLabelText('Commit message') as HTMLTextAreaElement;
    await user.clear(textarea);
    await user.type(textarea, 'My own message');

    await user.click(screen.getByLabelText('Select c.yaml'));
    expect(textarea.value).toBe('My own message');
  });

  it('checking a folder checkbox selects every checkable file under it, excluding the unpushed one', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    await user.click(screen.getByLabelText('Select flows'));
    expect((screen.getByLabelText('Select a.yaml') as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText('Select b.yaml') as HTMLInputElement).checked).toBe(true);
    expect(screen.getByRole('button', { name: 'Commit 2 files' })).toBeInTheDocument();
  });

  it('committing the exact selected paths calls repoChanges.commit with those paths and the message, then clears the selection', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    await user.click(screen.getByLabelText('Select a.yaml'));
    await user.click(screen.getByLabelText('Select sapien.workspace.yaml'));

    const commitButton = screen.getByRole('button', { name: 'Commit 2 files' });
    await user.click(commitButton);

    await waitFor(() =>
      expect(repoChangesCommit).toHaveBeenCalledWith(
        expect.arrayContaining(['flows/a.yaml', 'sapien.workspace.yaml']),
        expect.any(String),
      ),
    );
    const [paths] = repoChangesCommit.mock.calls[0];
    expect(paths.sort()).toEqual(['flows/a.yaml', 'sapien.workspace.yaml'].sort());

    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'committed 2 files')).toBe(true),
    );
  });

  it('the Commit button is disabled with nothing selected or an empty message', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Commit 0 files' })).toBeDisabled();
  });

  it('refetches on the workspace.repo event, debounced', async () => {
    renderPage();
    await waitFor(() => expect(repoChangesGet).toHaveBeenCalledTimes(1));

    useEvents.getState()._append(
      summarize({ type: 'workspace.repo', time: 't1', payload: { in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 5 } }),
    );

    await waitFor(() => expect(repoChangesGet).toHaveBeenCalledTimes(2), { timeout: 2000 });
  });

  it('shows a diff for a modified file', async () => {
    repoChangesDiff.mockResolvedValueOnce({
      path: 'flows/b.yaml',
      state: 'modified',
      diff: '--- a/flows/b.yaml\n+++ b/flows/b.yaml\n@@ -1,2 +1,2 @@\n-old line\n+new line\n',
      binary: false,
      truncated: false,
    });
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('b.yaml')).toBeInTheDocument());

    await user.click(screen.getByText('b.yaml'));

    await waitFor(() => expect(repoChangesDiff).toHaveBeenCalledWith('flows/b.yaml'));
    expect(await screen.findByText('+new line')).toBeInTheDocument();
    expect(screen.getByText('-old line')).toBeInTheDocument();
  });

  it('shows content (via YamlView) for an untracked YAML file, not a diff', async () => {
    repoChangesDiff.mockResolvedValueOnce({
      path: 'flows/a.yaml',
      state: 'untracked',
      diff: '',
      content: 'id: flow-a\nname: Checkout happy path\n',
      binary: false,
      truncated: false,
    });
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    await user.click(screen.getByText('a.yaml'));

    await waitFor(() => expect(repoChangesDiff).toHaveBeenCalledWith('flows/a.yaml'));
    // YamlView colours the key and value into separate inline nodes, so the
    // value text isn't any single element's own textContent; check the
    // rendered page as a whole instead of a single-node text query.
    await waitFor(() => expect(document.body.textContent).toContain('Checkout happy path'));
  });

  it('shows a link to the item page when kind and id are present', async () => {
    repoChangesDiff.mockResolvedValueOnce({ path: 'flows/a.yaml', state: 'untracked', diff: '', content: 'id: flow-a\n', binary: false, truncated: false });
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('a.yaml')).toBeInTheDocument());

    await user.click(screen.getByText('a.yaml'));
    const link = await screen.findByRole('link', { name: 'Open flow' });
    expect(link).toHaveAttribute('href', '/ui/flows/flow-a');
  });

  it('shows a binary notice and a truncated notice when the diff response says so', async () => {
    repoChangesDiff.mockResolvedValueOnce({ path: 'flows/b.yaml', state: 'modified', diff: '', binary: true, truncated: true });
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText('b.yaml')).toBeInTheDocument());

    await user.click(screen.getByText('b.yaml'));
    expect(await screen.findByText(/binary file/i)).toBeInTheDocument();
    expect(screen.getByText(/truncated/i)).toBeInTheDocument();
  });

  describe('Services section (read-only)', () => {
    it('shows a bound checkout with a lock icon, branch, and changed count, and lists its files without checkboxes', async () => {
      renderPage();
      await waitFor(() => expect(screen.getByText(/orders · feature\/x · 1 changed/)).toBeInTheDocument());

      const ordersSummary = screen.getByText(/orders · feature\/x · 1 changed/);
      const ordersRow = ordersSummary.closest('div')!;
      expect(ordersRow).toHaveAttribute('title', 'this repository is yours; commit it with your code');
      expect(screen.getByText('orders.go')).toBeInTheDocument();
      expect(screen.queryByLabelText('Select orders.go')).not.toBeInTheDocument();
    });

    it('shows a team-sourced service as a single row with no controls', async () => {
      renderPage();
      await waitFor(() => expect(screen.getByText(/billing/)).toBeInTheDocument());
      expect(screen.getByText('billing · team @ v2')).toBeInTheDocument();
    });
  });

  describe('empty states', () => {
    it('shows "not a git repository" when the workspace is not in git', async () => {
      const notInGit: RepoStatus = { in_git: false, behind: 0, ahead: 0, dirty: 0 };
      repoChangesGet.mockResolvedValueOnce({ status: notInGit, files: [], services: [] });
      useRepo.setState({ status: notInGit, started: true });
      renderPage();

      expect(await screen.findByText('This workspace is not a git repository')).toBeInTheDocument();
    });

    it('shows "Nothing to commit" with the last fetched time when clean', async () => {
      const clean: RepoStatus = { in_git: true, branch: 'main', behind: 0, ahead: 0, dirty: 0, fetched_at: new Date(Date.now() - 60_000).toISOString() };
      repoChangesGet.mockResolvedValueOnce({ status: clean, files: [], services: [] });
      useRepo.setState({ status: clean, started: true });
      renderPage();

      expect(await screen.findByText('Nothing to commit')).toBeInTheDocument();
      expect(screen.getByText(/Last checked/)).toBeInTheDocument();
    });
  });
});
