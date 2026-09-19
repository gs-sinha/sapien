// Multi-select "move to folder" on the list pages (PLAN §34f item 6
// follow-up): selection + the bulk-move action bar + row drag onto a
// sidebar folder, exercised through FlowsPage. MemoriesPage.test.tsx and
// ExamplesPage.test.tsx each carry one smoke test confirming the same
// wiring, since the mechanics themselves (Table's selection, the action
// bar, FolderSidebar's drop targets) are shared and covered here and in
// Table.test.tsx.
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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

const sample: FlowSummary[] = [
  flow({ id: 'a' }),
  flow({ id: 'b' }),
  flow({ id: 'c' }),
  flow({ id: 'd' }),
  // Seeds the "billing" folder so the sidebar renders it as a drop target.
  flow({ id: 'already-billing', folder: 'billing' }),
];

const flowsList = vi.fn(async (): Promise<FlowSummary[]> => sample);
const foldersMoveFlow = vi.fn(async (id: string, folder: string): Promise<FlowSummary> => flow({ id, folder }));

// A minimal but stateful fake DataTransfer: setData/getData share storage
// (needed for dragstart -> drop to actually carry the payload), and
// `types` reflects what's been set, matching what a drop target's
// dragenter/dragover check against real DataTransfer.types.
function fakeDataTransfer() {
  const store: Record<string, string> = {};
  return {
    setData: (type: string, value: string) => {
      store[type] = value;
    },
    getData: (type: string) => store[type] ?? '',
    get types() {
      return Object.keys(store);
    },
    effectAllowed: 'move',
    dropEffect: 'move',
  };
}

function rowOf(id: string) {
  return screen.getByRole('link', { name: id }).closest('tr')!;
}

function checkboxFor(id: string) {
  return screen.getByRole('checkbox', { name: `select ${id}` });
}

function billingFolderRow() {
  const tree = screen.getByRole('tree');
  return within(tree).getByText('billing').closest('[role="treeitem"]')!;
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
  flowsList.mockClear().mockResolvedValue(sample);
  foldersMoveFlow.mockReset().mockImplementation(async (id: string, folder: string) => flow({ id, folder }));
  useToasts.setState({ toasts: [] });
  useRepo.setState({ status: null, started: false });
});

describe('bulk move (via FlowsPage)', () => {
  it('moves every selected id in order with one API call each, then reloads the list once', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument());
    flowsList.mockClear();

    await user.click(checkboxFor('a'));
    await user.click(checkboxFor('b'));
    expect(screen.getByText('2 selected')).toBeInTheDocument();

    const moveButton = screen.getByRole('button', { name: 'Move 2 to folder…' });
    await user.click(moveButton);
    await user.clear(screen.getByRole('textbox', { name: /folder/i }));
    await user.type(screen.getByRole('textbox', { name: /folder/i }), 'billing');
    await user.click(screen.getByRole('button', { name: 'Move' }));

    await waitFor(() => expect(foldersMoveFlow).toHaveBeenCalledTimes(2));
    expect(foldersMoveFlow.mock.calls.map((c) => c[0])).toEqual(['a', 'b']);
    expect(foldersMoveFlow).toHaveBeenNthCalledWith(1, 'a', 'billing');
    expect(foldersMoveFlow).toHaveBeenNthCalledWith(2, 'b', 'billing');

    // Reloaded once, not once per moved item.
    expect(flowsList).toHaveBeenCalledTimes(1);

    // Selection cleared and the action bar is gone after a clean move.
    await waitFor(() => expect(screen.queryByText(/^\d+ selected$/)).not.toBeInTheDocument());
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'moved 2 flows to billing')).toBe(true),
    );
  });

  it('continues past a per-item failure, keeps only the failed rows selected, and lists their errors', async () => {
    const user = userEvent.setup();
    foldersMoveFlow.mockImplementation(async (id: string, folder: string) => {
      if (id === 'b') throw new Error('Conflict: a file already exists at that path');
      return flow({ id, folder });
    });
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument());
    flowsList.mockClear();

    await user.click(checkboxFor('a'));
    await user.click(checkboxFor('b'));
    await user.click(checkboxFor('c'));

    await user.click(screen.getByRole('button', { name: 'Move 3 to folder…' }));
    await user.clear(screen.getByRole('textbox', { name: /folder/i }));
    await user.type(screen.getByRole('textbox', { name: /folder/i }), 'billing');
    await user.click(screen.getByRole('button', { name: 'Move' }));

    await waitFor(() => expect(foldersMoveFlow).toHaveBeenCalledTimes(3));
    // Every item was attempted despite the failure in the middle.
    expect(foldersMoveFlow.mock.calls.map((c) => c[0])).toEqual(['a', 'b', 'c']);
    expect(flowsList).toHaveBeenCalledTimes(1);

    // Only "b" (the failure) stays selected/checked.
    await waitFor(() => expect(checkboxFor('b')).toBeChecked());
    expect(checkboxFor('a')).not.toBeChecked();
    expect(checkboxFor('c')).not.toBeChecked();

    // The failure is reported inline in the action bar, with the id and message.
    expect(screen.getByText(/b:/)).toBeInTheDocument();
    expect(screen.getByText(/Conflict: a file already exists at that path/)).toBeInTheDocument();
  });

  it('clears the selection when the text filter changes', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument());

    await user.click(checkboxFor('a'));
    expect(screen.getByText('1 selected')).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText(/filter by id/i), 'a');

    expect(screen.queryByText(/^\d+ selected$/)).not.toBeInTheDocument();
  });

  it('clears the selection when a tier chip is toggled', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument());

    await user.click(checkboxFor('a'));
    expect(screen.getByText('1 selected')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Team' }));

    expect(screen.queryByText(/^\d+ selected$/)).not.toBeInTheDocument();
  });

  it('dropping a selected row onto a sidebar folder moves the whole selection', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument());
    flowsList.mockClear();

    await user.click(checkboxFor('a'));
    await user.click(checkboxFor('c'));

    const dt = fakeDataTransfer();
    fireEvent.dragStart(rowOf('a'), { dataTransfer: dt });

    const billingRow = billingFolderRow();
    fireEvent.dragEnter(billingRow, { dataTransfer: dt });
    fireEvent.dragOver(billingRow, { dataTransfer: dt });
    fireEvent.drop(billingRow, { dataTransfer: dt });

    await waitFor(() => expect(foldersMoveFlow).toHaveBeenCalledTimes(2));
    expect(foldersMoveFlow.mock.calls.map((c) => c[0]).sort()).toEqual(['a', 'c']);
    expect(foldersMoveFlow).toHaveBeenCalledWith('a', 'billing');
    expect(foldersMoveFlow).toHaveBeenCalledWith('c', 'billing');
    expect(flowsList).toHaveBeenCalledTimes(1);
  });

  it('dropping an unselected row moves just that row, not the selection', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <FlowsPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument());
    flowsList.mockClear();

    await user.click(checkboxFor('a'));

    const dt = fakeDataTransfer();
    fireEvent.dragStart(rowOf('d'), { dataTransfer: dt });

    const billingRow = billingFolderRow();
    fireEvent.dragEnter(billingRow, { dataTransfer: dt });
    fireEvent.dragOver(billingRow, { dataTransfer: dt });
    fireEvent.drop(billingRow, { dataTransfer: dt });

    await waitFor(() => expect(foldersMoveFlow).toHaveBeenCalledTimes(1));
    expect(foldersMoveFlow).toHaveBeenCalledWith('d', 'billing');
  });
});
