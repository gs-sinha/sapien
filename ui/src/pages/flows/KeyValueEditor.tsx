// Generic add/remove key-value row editor, used by FlowStepCard for both a
// step's `input` (bound by name to the operation's path/query/header
// params) and its `headers`. Deliberately not components/try/HeadersEditor
// (that one's heading/labels are hardcoded to "Extra headers" for the Try
// It page) -- same shape, different labels, small enough not to share.
export interface KVRow {
  key: string;
  value: string;
}

export function rowsFromRecord(rec: Record<string, unknown> | undefined): KVRow[] {
  const entries = Object.entries(rec || {});
  return entries.map(([key, value]) => ({ key, value: typeof value === 'string' ? value : JSON.stringify(value) }));
}

export function recordFromRows(rows: KVRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const { key, value } of rows) {
    if (key.trim() !== '') out[key.trim()] = value;
  }
  return out;
}

export function KeyValueEditor({
  title,
  rows,
  onChange,
  knownKeys,
  requiredKeys,
  addLabel = 'Add field',
}: {
  title: string;
  rows: KVRow[];
  onChange: (rows: KVRow[]) => void;
  // Names the operation declares (path/query/header params) that aren't in
  // `rows` yet -- rendered as one-click "+ name" chips so the user doesn't
  // have to retype a param name from the Parameters table above.
  knownKeys?: string[];
  // Subset of knownKeys (and/or of an existing row's key) the operation
  // marks required -- shown with a "*" so a missing required param stands
  // out among the suggestion chips, and on an already-present row.
  requiredKeys?: string[];
  addLabel?: string;
}) {
  const update = (i: number, patch: Partial<KVRow>) => onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  const remove = (i: number) => onChange(rows.filter((_, idx) => idx !== i));
  const add = (key = '') => onChange([...rows, { key, value: '' }]);

  const requiredSet = new Set(requiredKeys || []);
  const presentKeys = new Set(rows.map((r) => r.key));
  const suggestions = (knownKeys || []).filter((k) => !presentKeys.has(k));

  return (
    <div>
      <h4 className="mb-1 text-xs font-semibold uppercase text-slate-500">{title}</h4>
      <div className="space-y-1.5">
        {rows.map((row, i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              value={row.key}
              onChange={(e) => update(i, { key: e.target.value })}
              placeholder="name"
              className="w-40 rounded border border-slate-300 bg-white px-2 py-1 text-xs font-mono dark:border-slate-700 dark:bg-slate-900"
            />
            {requiredSet.has(row.key) && <span className="text-xs text-red-500" title="required by the operation">*</span>}
            <input
              value={row.value}
              onChange={(e) => update(i, { value: e.target.value })}
              placeholder="value"
              className="flex-1 rounded border border-slate-300 bg-white px-2 py-1 text-xs font-mono dark:border-slate-700 dark:bg-slate-900"
            />
            <button
              type="button"
              onClick={() => remove(i)}
              aria-label={`Remove ${row.key || 'row'}`}
              className="rounded border border-slate-300 px-2 py-1 text-xs text-slate-500 hover:text-red-600 dark:border-slate-700"
            >
              &times;
            </button>
          </div>
        ))}
        {rows.length === 0 && <div className="text-xs text-slate-400">Empty.</div>}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        <button
          type="button"
          onClick={() => add()}
          className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-600 hover:text-slate-900 dark:border-slate-700 dark:text-slate-300 dark:hover:text-white"
        >
          {addLabel}
        </button>
        {suggestions.map((k) => (
          <button
            key={k}
            type="button"
            onClick={() => add(k)}
            className={
              requiredSet.has(k)
                ? 'rounded border border-dashed border-red-300 px-2 py-0.5 text-[11px] text-red-600 hover:text-red-800 dark:border-red-800 dark:text-red-400 dark:hover:text-red-300'
                : 'rounded border border-dashed border-slate-300 px-2 py-0.5 text-[11px] text-slate-500 hover:text-slate-800 dark:border-slate-700 dark:text-slate-400 dark:hover:text-slate-100'
            }
          >
            + {k}
            {requiredSet.has(k) ? ' *' : ''}
          </button>
        ))}
      </div>
    </div>
  );
}
