// Folder sidebar + breadcrumb + URL sync + "Move to folder…" (PLAN §34f
// item 6), exercised through FlowsPage: MemoriesPage and ExamplesPage wire
// the exact same shared components (components/FolderSidebar.tsx,
// components/FolderBreadcrumb.tsx, lib/useFolderParam.ts,
// components/FolderMovePopover.tsx), so one page is enough to cover the
// shared behavior thoroughly rather than repeating it three times.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import FlowsPage from '../pages/FlowsPage';
import { useRepo } from '../state/repo';
import { useToasts } from '../state/toast';
import type { FlowSummary } from '../api/types';

function flow(overrides: Partial<FlowSummary>): FlowSummary {
  return {
    id: overrides.id || 'flow',
    path: 'flows/x.yaml',
    owner_kind: 'workspace',
    step_count: 1,
    hash: 'h',
    updated: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

const noFolders: FlowSummary[] = [flow({ id: 'a' }), flow({ id: 'b' })];

const foldered: FlowSummary[] = [
  flow({ id: 'root-flow', folder: '' }),
  flow({ id: 'a-flow', folder: 'a' }),
  flow({ id: 'a-b-flow', folder: 'a/b' }),
  flow({ id: 'other-flow', folder: 'other' }),
];

const flowsList = vi.fn(async (): Promise<FlowSummary[]> => noFolders);
const foldersMoveFlow = vi.fn(async (id: string, folder: string): Promise<FlowSummary> => flow({ id, folder }));

// The folder sidebar's own tree, distinct from the "a"/"other" folder
// values the Table's Folder column also happens to print as plain text.
function clickFolderRow(name: string) {
  const tree = screen.getByRole('tree');
  return within(tree).getByText(name).closest('[role="treeitem"]')!;
}

// The sidebar's own "All" button, distinct from the breadcrumb's "All"
// crumb (both render the same accessible name).
function sidebarAllButton() {
  const tree = screen.getByRole('tree');
  return within(tree.parentElement as HTMLElement).getByRole('button', { name: 'All' });
}

vi.mock('../api/client', () => ({
  flows: {
    list: () => flowsList(),
    commit: vi.fn(),
  },
  folders: {
    moveFlow: (id: string, folder: string) => foldersMoveFlow(id, folder),
  },
  repo: {
    push: vi.fn(),
  },
}));

beforeEach(() => {
  flowsList.mockClear().mockResolvedValue(noFolders);
  foldersMoveFlow.mockClear();
  useToasts.setState({ toasts: [] });
  useRepo.setState({ status: null, started: false });
});

describe('folder sidebar (via FlowsPage)', () => {
  it('renders no sidebar at all when no item has a folder', async () => {
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: 'All' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tree')).not.toBeInTheDocument();
  });

  it('shows the sidebar, filters the list to a selected folder and everything below it, and keeps an unrelated folder out', async () => {
    flowsList.mockResolvedValue(foldered);
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('root-flow')).toBeInTheDocument());

    expect(sidebarAllButton()).toBeInTheDocument();
    // Every row shows regardless of folder until a folder is selected.
    expect(screen.getByText('root-flow')).toBeInTheDocument();
    expect(screen.getByText('a-flow')).toBeInTheDocument();
    expect(screen.getByText('a-b-flow')).toBeInTheDocument();
    expect(screen.getByText('other-flow')).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(clickFolderRow('a'));

    expect(screen.getByText('a-flow')).toBeInTheDocument();
    expect(screen.getByText('a-b-flow')).toBeInTheDocument();
    expect(screen.queryByText('root-flow')).not.toBeInTheDocument();
    expect(screen.queryByText('other-flow')).not.toBeInTheDocument();
  });

  it('keeps the folder selection in the ?folder= query string', async () => {
    flowsList.mockResolvedValue(foldered);
    render(
      <MemoryRouter initialEntries={['/ui/flows']}>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('root-flow')).toBeInTheDocument());

    const user = userEvent.setup();
    await user.click(clickFolderRow('a'));

    // The breadcrumb reflects the selection, proving the URL state round-tripped.
    const breadcrumb = screen.getByRole('navigation', { name: 'Folder' });
    expect(within(breadcrumb).getByText('a')).toBeInTheDocument();
  });

  it('restores the folder filter from an existing ?folder= query on load', async () => {
    flowsList.mockResolvedValue(foldered);
    render(
      <MemoryRouter initialEntries={['/ui/flows?folder=a']}>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a-flow')).toBeInTheDocument());
    expect(screen.queryByText('root-flow')).not.toBeInTheDocument();
    expect(screen.queryByText('other-flow')).not.toBeInTheDocument();
  });

  it('the breadcrumb "All" link clears the folder filter', async () => {
    flowsList.mockResolvedValue(foldered);
    render(
      <MemoryRouter initialEntries={['/ui/flows?folder=a']}>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a-flow')).toBeInTheDocument());
    expect(screen.queryByText('root-flow')).not.toBeInTheDocument();

    const user = userEvent.setup();
    const breadcrumb = screen.getByRole('navigation', { name: 'Folder' });
    await user.click(within(breadcrumb).getByRole('button', { name: 'All' }));

    expect(screen.getByText('root-flow')).toBeInTheDocument();
    expect(screen.getByText('other-flow')).toBeInTheDocument();
  });
});

describe('"Move to folder…" (via FlowsPage)', () => {
  const rowOf = (id: string) => screen.getByRole('link', { name: id }).closest('tr')!;

  it('typing a new folder and submitting calls folders.moveFlow with the normalized path', async () => {
    flowsList.mockResolvedValue(foldered);
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('root-flow')).toBeInTheDocument());

    await user.click(within(rowOf('root-flow')).getByRole('button', { name: 'Move to folder…' }));
    const input = screen.getByRole('textbox', { name: /folder/i });
    await user.clear(input);
    await user.type(input, 'brand-new');
    await user.click(screen.getByRole('button', { name: 'Move' }));

    await waitFor(() => expect(foldersMoveFlow).toHaveBeenCalledWith('root-flow', 'brand-new'));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'moved to brand-new')).toBe(true),
    );
  });

  it('"/" moves the item back to the root', async () => {
    flowsList.mockResolvedValue(foldered);
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a-flow')).toBeInTheDocument());

    await user.click(within(rowOf('a-flow')).getByRole('button', { name: 'Move to folder…' }));
    const input = screen.getByRole('textbox', { name: /folder/i });
    await user.clear(input);
    await user.type(input, '/');
    await user.click(screen.getByRole('button', { name: 'Move' }));

    await waitFor(() => expect(foldersMoveFlow).toHaveBeenCalledWith('a-flow', ''));
  });

  it('lists every existing folder of that kind, and one click on one moves the item there', async () => {
    flowsList.mockResolvedValue(foldered);
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('root-flow')).toBeInTheDocument());

    await user.click(within(rowOf('root-flow')).getByRole('button', { name: 'Move to folder…' }));
    const list = screen.getByRole('list', { name: 'Existing folders' });
    const offered = within(list).getAllByRole('button').map((b) => b.textContent);
    expect(offered.sort()).toEqual(['a', 'a/b', 'other'].sort());

    await user.click(within(list).getByRole('button', { name: 'other' }));
    await waitFor(() => expect(foldersMoveFlow).toHaveBeenCalledWith('root-flow', 'other'));
  });

  it('Enter on the name of an existing folder moves the item there', async () => {
    flowsList.mockResolvedValue(foldered);
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('root-flow')).toBeInTheDocument());

    await user.click(within(rowOf('root-flow')).getByRole('button', { name: 'Move to folder…' }));
    await user.type(screen.getByRole('textbox', { name: /folder/i }), 'other{Enter}');
    await waitFor(() => expect(foldersMoveFlow).toHaveBeenCalledWith('root-flow', 'other'));
  });

  it('a failed move surfaces the error inline in the popover', async () => {
    flowsList.mockResolvedValue(foldered);
    foldersMoveFlow.mockRejectedValueOnce(new Error('Conflict: a file already exists at that path'));
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('root-flow')).toBeInTheDocument());

    await user.click(within(rowOf('root-flow')).getByRole('button', { name: 'Move to folder…' }));
    const input = screen.getByRole('textbox', { name: /folder/i });
    await user.clear(input);
    await user.type(input, 'a');
    await user.click(screen.getByRole('button', { name: 'Move' }));

    expect(await screen.findByText('Conflict: a file already exists at that path')).toBeInTheDocument();
    // The popover stays open on failure so the user can retry.
    expect(screen.getByRole('button', { name: 'Move' })).toBeInTheDocument();
  });
});
