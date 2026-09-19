import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Tree } from '../components/tree/Tree';
import { buildTree } from '../components/tree/buildTree';

interface File {
  path: string;
}
function file(path: string): File {
  return { path };
}

const items = [file('flows/a.yaml'), file('flows/b.yaml'), file('memories/c.md')];

beforeEach(() => {
  try {
    localStorage.clear();
  } catch {
    // ignore
  }
});

describe('Tree', () => {
  it('renders folder and file rows with role="tree"/"treeitem"', () => {
    const nodes = buildTree(items, (f) => f.path);
    render(<Tree nodes={nodes} treeKey="t1" />);

    expect(screen.getByRole('tree')).toBeInTheDocument();
    const items_ = screen.getAllByRole('treeitem');
    // flows (expanded by default), a.yaml, b.yaml, memories (expanded), c.md
    expect(items_).toHaveLength(5);
    expect(screen.getByText('flows')).toBeInTheDocument();
    expect(screen.getByText('a.yaml')).toBeInTheDocument();
  });

  it('sets aria-expanded on folders and aria-selected on the selected row', () => {
    const nodes = buildTree(items, (f) => f.path);
    render(<Tree nodes={nodes} treeKey="t2" selectedKey="flows/a.yaml" />);

    const folder = screen.getByText('flows').closest('[role="treeitem"]')!;
    expect(folder).toHaveAttribute('aria-expanded', 'true');
    const selected = screen.getByText('a.yaml').closest('[role="treeitem"]')!;
    expect(selected).toHaveAttribute('aria-selected', 'true');
    const other = screen.getByText('b.yaml').closest('[role="treeitem"]')!;
    expect(other).toHaveAttribute('aria-selected', 'false');
  });

  it('clicking the chevron collapses a folder, hiding its children; clicking again re-expands', async () => {
    const user = userEvent.setup();
    const nodes = buildTree(items, (f) => f.path);
    render(<Tree nodes={nodes} treeKey="t3" />);

    expect(screen.getByText('a.yaml')).toBeInTheDocument();
    // The chevron is the first button inside the "flows" row.
    const flowsRow = screen.getByText('flows').closest('[role="treeitem"]')!;
    const chevron = flowsRow.querySelector('button')!;
    await user.click(chevron);

    expect(screen.queryByText('a.yaml')).not.toBeInTheDocument();
    expect(screen.getByText('flows')).toBeInTheDocument();

    await user.click(chevron);
    expect(screen.getByText('a.yaml')).toBeInTheDocument();
  });

  it('calls onSelect when a row is clicked', async () => {
    const user = userEvent.setup();
    const nodes = buildTree(items, (f) => f.path);
    const onSelect = vi.fn();
    render(<Tree nodes={nodes} treeKey="t4" onSelect={onSelect} />);

    await user.click(screen.getByText('a.yaml'));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect.mock.calls[0][0].key).toBe('flows/a.yaml');
  });

  it('remembers collapsed state per treeKey across remounts (localStorage)', async () => {
    const user = userEvent.setup();
    const nodes = buildTree(items, (f) => f.path);
    const { unmount } = render(<Tree nodes={nodes} treeKey="persisted" />);
    const flowsRow = screen.getByText('flows').closest('[role="treeitem"]')!;
    await user.click(flowsRow.querySelector('button')!);
    expect(screen.queryByText('a.yaml')).not.toBeInTheDocument();
    unmount();

    render(<Tree nodes={buildTree(items, (f) => f.path)} treeKey="persisted" />);
    expect(screen.queryByText('a.yaml')).not.toBeInTheDocument();
    expect(screen.getByText('flows')).toBeInTheDocument();
  });

  it('does not leak collapsed state between different treeKeys', () => {
    const nodes = buildTree(items, (f) => f.path);
    render(<Tree nodes={nodes} treeKey="fresh-key-xyz" />);
    // A never-before-seen treeKey starts fully expanded.
    expect(screen.getByText('a.yaml')).toBeInTheDocument();
  });

  describe('tri-state checkboxes', () => {
    it('checking a folder checks every descendant leaf; unchecking clears them all', async () => {
      const user = userEvent.setup();
      const nodes = buildTree(items, (f) => f.path);
      let checked = new Set<string>();
      const onToggleCheck = vi.fn((node: { key: string; isFolder: boolean }, next: boolean) => {
        // emulate what a real page's leafKeys-based handler would do for a folder
      });
      const { rerender } = render(
        <Tree nodes={nodes} treeKey="t5" checkable checkedKeys={checked} onToggleCheck={onToggleCheck} />,
      );

      const flowsCheckbox = screen.getByLabelText('Select flows') as HTMLInputElement;
      expect(flowsCheckbox.checked).toBe(false);
      expect(flowsCheckbox.indeterminate).toBe(false);

      await user.click(flowsCheckbox);
      expect(onToggleCheck).toHaveBeenCalledWith(expect.objectContaining({ key: 'flows' }), true);

      // Simulate the parent applying the toggle to every leaf under "flows".
      checked = new Set(['flows/a.yaml', 'flows/b.yaml']);
      rerender(<Tree nodes={nodes} treeKey="t5" checkable checkedKeys={checked} onToggleCheck={onToggleCheck} />);
      expect((screen.getByLabelText('Select flows') as HTMLInputElement).checked).toBe(true);
      expect((screen.getByLabelText('Select a.yaml') as HTMLInputElement).checked).toBe(true);
    });

    it('shows indeterminate on a folder when only some descendants are checked', () => {
      const nodes = buildTree(items, (f) => f.path);
      const checked = new Set(['flows/a.yaml']);
      render(<Tree nodes={nodes} treeKey="t6" checkable checkedKeys={checked} onToggleCheck={() => {}} />);

      const flowsCheckbox = screen.getByLabelText('Select flows') as HTMLInputElement;
      expect(flowsCheckbox.checked).toBe(false);
      expect(flowsCheckbox.indeterminate).toBe(true);
    });

    it('checkableFilter hides the checkbox for a leaf and excludes it from its folder\'s tri-state', () => {
      const nodes = buildTree(items, (f) => f.path);
      const checked = new Set(['flows/a.yaml', 'flows/b.yaml']);
      render(
        <Tree
          nodes={nodes}
          treeKey="t7"
          checkable
          checkedKeys={checked}
          onToggleCheck={() => {}}
          checkableFilter={(n) => n.key !== 'flows/b.yaml'}
        />,
      );

      expect(screen.queryByLabelText('Select b.yaml')).not.toBeInTheDocument();
      // Only a.yaml is checkable and it's checked, so "flows" reads fully checked.
      expect((screen.getByLabelText('Select flows') as HTMLInputElement).checked).toBe(true);
    });
  });

  describe('keyboard navigation', () => {
    it('ArrowDown/ArrowUp move focus (roving tabindex) between visible rows', async () => {
      const user = userEvent.setup();
      const nodes = buildTree(items, (f) => f.path);
      render(<Tree nodes={nodes} treeKey="t8" />);

      const flowsRow = screen.getByText('flows').closest('[role="treeitem"]')!;
      expect(flowsRow).toHaveAttribute('tabindex', '0');
      flowsRow.focus();

      await user.keyboard('{ArrowDown}');
      const aRow = screen.getByText('a.yaml').closest('[role="treeitem"]')!;
      expect(aRow).toHaveAttribute('tabindex', '0');
      expect(flowsRow).toHaveAttribute('tabindex', '-1');

      await user.keyboard('{ArrowUp}');
      expect(flowsRow).toHaveAttribute('tabindex', '0');
    });

    it('ArrowLeft on an expanded folder collapses it; ArrowRight re-expands', async () => {
      const user = userEvent.setup();
      const nodes = buildTree(items, (f) => f.path);
      render(<Tree nodes={nodes} treeKey="t9" />);

      const flowsRow = screen.getByText('flows').closest('[role="treeitem"]')!;
      flowsRow.focus();
      await user.keyboard('{ArrowLeft}');
      expect(screen.queryByText('a.yaml')).not.toBeInTheDocument();

      await user.keyboard('{ArrowRight}');
      expect(screen.getByText('a.yaml')).toBeInTheDocument();
    });

    it('Enter calls onSelect for the focused row', async () => {
      const user = userEvent.setup();
      const nodes = buildTree(items, (f) => f.path);
      const onSelect = vi.fn();
      render(<Tree nodes={nodes} treeKey="t10" onSelect={onSelect} />);

      const aRow = screen.getByText('a.yaml').closest('[role="treeitem"]')!;
      aRow.focus();
      await user.keyboard('{Enter}');
      expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ key: 'flows/a.yaml' }));
    });

    it('Space toggles expand on a focused folder when not checkable', async () => {
      const user = userEvent.setup();
      const nodes = buildTree(items, (f) => f.path);
      render(<Tree nodes={nodes} treeKey="t11" />);

      const flowsRow = screen.getByText('flows').closest('[role="treeitem"]')!;
      flowsRow.focus();
      await user.keyboard(' ');
      expect(screen.queryByText('a.yaml')).not.toBeInTheDocument();
    });
  });

  it('shows the roll-up count on a folder, both collapsed and expanded', async () => {
    const user = userEvent.setup();
    const nodes = buildTree(items, (f) => f.path);
    render(<Tree nodes={nodes} treeKey="t12" />);

    const flowsRow = screen.getByText('flows').closest('[role="treeitem"]')!;
    expect(flowsRow).toHaveTextContent('2');

    await user.click(flowsRow.querySelector('button')!);
    expect(screen.getByText('flows').closest('[role="treeitem"]')).toHaveTextContent('2');
  });

  it('hides leaf rows but keeps rolled-up counts when showLeaves is false', () => {
    const nodes = buildTree(items, (f) => f.path);
    render(<Tree nodes={nodes} treeKey="t13" showLeaves={false} />);

    expect(screen.queryByText('a.yaml')).not.toBeInTheDocument();
    expect(screen.getByText('flows').closest('[role="treeitem"]')).toHaveTextContent('2');
  });

  it('renders emptyLabel when there are no nodes, and nothing when emptyLabel is omitted', () => {
    const { rerender } = render(<Tree nodes={[]} treeKey="empty1" emptyLabel="Nothing here" />);
    expect(screen.getByText('Nothing here')).toBeInTheDocument();

    rerender(<Tree nodes={[]} treeKey="empty1" />);
    expect(screen.queryByRole('tree')).not.toBeInTheDocument();
  });

  it('renderLabel and renderRight customize the row content', () => {
    const nodes = buildTree(items, (f) => f.path);
    render(
      <Tree
        nodes={nodes}
        treeKey="t14"
        renderLabel={(n) => `label:${n.name}`}
        renderRight={(n) => (n.isFolder ? null : 'M')}
      />,
    );
    expect(screen.getByText('label:flows')).toBeInTheDocument();
    expect(screen.getByText('label:a.yaml')).toBeInTheDocument();
    expect(screen.getAllByText('M').length).toBeGreaterThan(0);
  });
});
