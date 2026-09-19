import { useRef } from 'react';
import type { ReactNode } from 'react';
import { setDragIds } from '../lib/dragPayload';

export interface Column<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
  className?: string;
}

// Optional multi-select (PLAN §34f item 6 follow-up: bulk "move to folder"
// on the Flows/Memories/Examples list pages). Entirely opt-in -- a caller
// that passes neither `selectedKeys` nor `onSelectionChange` gets exactly
// the table it had before, including no leading checkbox column, so every
// other page using this component (Operations, Services, ValidatePanel) is
// unaffected.
export function Table<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
  selectedKeys,
  onSelectionChange,
  draggable = false,
}: {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  onRowClick?: (row: T) => void;
  /** Currently selected row keys. Passing this together with
   *  `onSelectionChange` turns on the leading checkbox column. */
  selectedKeys?: ReadonlySet<string>;
  onSelectionChange?: (next: Set<string>) => void;
  /** Makes every row an HTML5 drag source. The drag payload is the whole
   *  current selection when the dragged row is part of it, or just that
   *  row otherwise -- see PLAN §34f item 6 follow-up, item 3. No-op unless
   *  `selectedKeys`/`onSelectionChange` are also passed. */
  draggable?: boolean;
}) {
  const selectable = !!(selectedKeys && onSelectionChange);
  // Range anchor for shift-click select, e.g. Gmail/Finder-style: click a
  // box, shift-click another, and everything between takes the second
  // box's new state. A ref, not state -- it's an interaction detail the
  // caller never needs to see or restore.
  const lastIndexRef = useRef<number | null>(null);

  const allKeys = rows.map(rowKey);
  const selectedCount = selectable ? allKeys.filter((k) => selectedKeys!.has(k)).length : 0;
  const allSelected = selectable && rows.length > 0 && selectedCount === rows.length;
  const someSelected = selectable && selectedCount > 0 && !allSelected;

  const toggleRow = (idx: number, shiftKey: boolean) => {
    if (!selectable) return;
    const key = allKeys[idx];
    const nextChecked = !selectedKeys!.has(key);
    const next = new Set(selectedKeys);
    if (shiftKey && lastIndexRef.current !== null) {
      const [start, end] = [lastIndexRef.current, idx].sort((a, b) => a - b);
      for (let i = start; i <= end; i++) {
        if (nextChecked) next.add(allKeys[i]);
        else next.delete(allKeys[i]);
      }
    } else if (nextChecked) {
      next.add(key);
    } else {
      next.delete(key);
    }
    lastIndexRef.current = idx;
    onSelectionChange!(next);
  };

  const toggleAll = () => {
    if (!selectable) return;
    onSelectionChange!(allSelected ? new Set() : new Set(allKeys));
  };

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-slate-200 text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800">
            {selectable && (
              <th className="w-8 px-3 py-2">
                <input
                  type="checkbox"
                  aria-label="select all"
                  checked={allSelected}
                  ref={(el) => {
                    if (el) el.indeterminate = someSelected;
                  }}
                  onChange={() => {}}
                  onClick={(e) => {
                    e.stopPropagation();
                    toggleAll();
                  }}
                />
              </th>
            )}
            {columns.map((c) => (
              <th key={c.key} className={`whitespace-nowrap px-3 py-2 font-medium ${c.className || ''}`}>
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, idx) => {
            const key = rowKey(row);
            return (
              <tr
                key={key}
                draggable={draggable}
                onDragStart={
                  draggable
                    ? (e) => {
                        const ids = selectedKeys && selectedKeys.has(key) ? Array.from(selectedKeys) : [key];
                        setDragIds(e, ids);
                      }
                    : undefined
                }
                onClick={onRowClick ? () => onRowClick(row) : undefined}
                className={`border-b border-slate-100 dark:border-slate-900 ${onRowClick ? 'cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-900' : ''}`}
              >
                {selectable && (
                  <td className="px-3 py-2" onClick={(e) => e.stopPropagation()}>
                    <input
                      type="checkbox"
                      aria-label={`select ${key}`}
                      checked={selectedKeys!.has(key)}
                      onChange={() => {}}
                      onClick={(e) => {
                        e.stopPropagation();
                        toggleRow(idx, e.shiftKey);
                      }}
                    />
                  </td>
                )}
                {columns.map((c) => (
                  <td key={c.key} className={`whitespace-nowrap px-3 py-2 ${c.className || ''}`}>
                    {c.render(row)}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
