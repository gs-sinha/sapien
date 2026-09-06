import type { ReactNode } from 'react';

export interface Column<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
  className?: string;
}

export function Table<T>({ columns, rows, rowKey, onRowClick }: { columns: Column<T>[]; rows: T[]; rowKey: (row: T) => string; onRowClick?: (row: T) => void }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-slate-200 text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800">
            {columns.map((c) => (
              <th key={c.key} className={`whitespace-nowrap px-3 py-2 font-medium ${c.className || ''}`}>
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={rowKey(row)}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
              className={`border-b border-slate-100 dark:border-slate-900 ${onRowClick ? 'cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-900' : ''}`}
            >
              {columns.map((c) => (
                <td key={c.key} className={`whitespace-nowrap px-3 py-2 ${c.className || ''}`}>
                  {c.render(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
