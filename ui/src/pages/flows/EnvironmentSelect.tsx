import { useEffect } from 'react';
import { environments } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import type { Environment } from '../../api/types';

async function load(): Promise<{ list: Environment[]; def: string }> {
  const [list, def] = await Promise.all([environments.list(), environments.getDefault().catch(() => ({ name: '' }))]);
  return { list, def: def.name };
}

// Environment picker shared by the flow "Run" panel and the run detail
// "Edit and rerun" panel: /v1/environments has no per-item "is this the
// default" flag, so the default name comes from the separate
// /v1/environments/default call and is only used to mark an option and
// seed the initial selection.
export function EnvironmentSelect({ value, onChange }: { value: string; onChange: (name: string) => void }) {
  const { data, loading, error } = useAsync(load, []);

  useEffect(() => {
    if (data && !value && data.list.length > 0) {
      const preferred = data.list.some((e) => e.name === data.def) ? data.def : data.list[0].name;
      onChange(preferred);
    }
    // Only seed the initial value once environments load; onChange identity
    // is not part of this effect's condition.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  if (loading) return <div className="text-xs text-slate-400">Loading environments…</div>;
  if (error) return <div className="text-xs text-red-600">{error.message}</div>;
  if (!data || data.list.length === 0) return <div className="text-xs text-slate-400">No environments configured.</div>;

  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
    >
      {data.list.map((e) => (
        <option key={e.name} value={e.name}>
          {e.name}
          {e.name === data.def ? ' (default)' : ''}
        </option>
      ))}
    </select>
  );
}
