// Free-form extra headers list (on top of any documented header params,
// which ParamsForm already covers): add/remove key-value rows.
export interface HeaderRow {
  key: string;
  value: string;
}

export function headerRowsToRecord(rows: HeaderRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const { key, value } of rows) {
    if (key.trim() !== '') out[key.trim()] = value;
  }
  return out;
}

export function HeadersEditor({ rows, onChange }: { rows: HeaderRow[]; onChange: (rows: HeaderRow[]) => void }) {
  const update = (i: number, patch: Partial<HeaderRow>) => {
    onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  };
  const remove = (i: number) => onChange(rows.filter((_, idx) => idx !== i));
  const add = () => onChange([...rows, { key: '', value: '' }]);

  return (
    <div>
      <h3 className="mb-1.5 text-xs font-semibold uppercase text-slate-500">Extra headers</h3>
      <div className="space-y-1.5">
        {rows.map((row, i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              value={row.key}
              onChange={(e) => update(i, { key: e.target.value })}
              placeholder="Header-Name"
              className="w-40 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
            <input
              value={row.value}
              onChange={(e) => update(i, { value: e.target.value })}
              placeholder="value"
              className="flex-1 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
            <button
              type="button"
              onClick={() => remove(i)}
              aria-label={`Remove header ${row.key || i}`}
              className="rounded border border-slate-300 px-2 py-1 text-xs text-slate-500 hover:text-red-600 dark:border-slate-700"
            >
              &times;
            </button>
          </div>
        ))}
      </div>
      <button
        type="button"
        onClick={add}
        className="mt-2 rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-600 hover:text-slate-900 dark:border-slate-700 dark:text-slate-300 dark:hover:text-white"
      >
        Add header
      </button>
    </div>
  );
}
