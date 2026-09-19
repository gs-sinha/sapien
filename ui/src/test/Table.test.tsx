// Table's optional row selection and drag source (PLAN §34f item 6 follow-up,
// item 1 and item 3): off unless `selectedKeys`/`onSelectionChange` are both
// passed, so every other page using Table (Operations, Services,
// ValidatePanel) renders exactly as before. FlowsPage-level tests in
// BulkMove.test.tsx cover the bulk-move/drag-drop behavior this enables;
// this file is the direct unit coverage of Table's own selection mechanics.
import { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { Table } from '../components/Table';

interface Row {
  id: string;
}

const rows: Row[] = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }];
const columns = [{ key: 'id', header: 'ID', render: (r: Row) => r.id }];

function SelectableTable() {
  const [selected, setSelected] = useState(new Set<string>());
  return <Table<Row> rowKey={(r) => r.id} columns={columns} rows={rows} selectedKeys={selected} onSelectionChange={setSelected} />;
}

describe('Table selection', () => {
  it('renders no checkbox column when selectedKeys/onSelectionChange are not passed', () => {
    render(<Table<Row> rowKey={(r) => r.id} columns={columns} rows={rows} />);
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('checks a row, and the header checkbox tri-states as none/some/all are checked', async () => {
    const user = userEvent.setup();
    render(<SelectableTable />);

    const headerBox = screen.getByRole('checkbox', { name: 'select all' }) as HTMLInputElement;
    expect(headerBox).not.toBeChecked();
    expect(headerBox.indeterminate).toBe(false);

    await user.click(screen.getByRole('checkbox', { name: 'select a' }));
    expect(headerBox).not.toBeChecked();
    expect(headerBox.indeterminate).toBe(true);

    await user.click(screen.getByRole('checkbox', { name: 'select b' }));
    await user.click(screen.getByRole('checkbox', { name: 'select c' }));
    await user.click(screen.getByRole('checkbox', { name: 'select d' }));
    expect(headerBox).toBeChecked();
    expect(headerBox.indeterminate).toBe(false);

    // Clicking the header checkbox while every row is checked clears all of them.
    await user.click(headerBox);
    for (const r of rows) expect(screen.getByRole('checkbox', { name: `select ${r.id}` })).not.toBeChecked();

    // Clicking it again (nothing checked) selects every row.
    await user.click(headerBox);
    for (const r of rows) expect(screen.getByRole('checkbox', { name: `select ${r.id}` })).toBeChecked();
  });

  it('shift-clicking a second row box selects the whole range between it and the last-clicked box', async () => {
    const user = userEvent.setup();
    render(<SelectableTable />);

    await user.click(screen.getByRole('checkbox', { name: 'select a' }));
    await user.keyboard('[ShiftLeft>]');
    await user.click(screen.getByRole('checkbox', { name: 'select c' }));
    await user.keyboard('[/ShiftLeft]');

    expect(screen.getByRole('checkbox', { name: 'select a' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'select b' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'select c' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'select d' })).not.toBeChecked();
  });

  it('is not draggable by default, and drags the whole selection when the dragged row is part of it', () => {
    const selected = new Set(['a', 'c']);
    const { rerender } = render(<Table<Row> rowKey={(r) => r.id} columns={columns} rows={rows} />);
    const rowA = screen.getByText('a').closest('tr')!;
    expect(rowA).toHaveAttribute('draggable', 'false');

    rerender(<Table<Row> rowKey={(r) => r.id} columns={columns} rows={rows} selectedKeys={selected} onSelectionChange={vi.fn()} draggable />);
    const draggableRowA = screen.getByText('a').closest('tr')!;
    expect(draggableRowA).toHaveAttribute('draggable', 'true');

    const setData = vi.fn();
    fireEvent.dragStart(draggableRowA, { dataTransfer: { setData, effectAllowed: '' } });
    expect(setData).toHaveBeenCalledWith('application/x-sapien-move-ids', JSON.stringify(['a', 'c']));

    // A row outside the selection drags just itself.
    const rowB = screen.getByText('b').closest('tr')!;
    const setDataB = vi.fn();
    fireEvent.dragStart(rowB, { dataTransfer: { setData: setDataB, effectAllowed: '' } });
    expect(setDataB).toHaveBeenCalledWith('application/x-sapien-move-ids', JSON.stringify(['b']));
  });
});
