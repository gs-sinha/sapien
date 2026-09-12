import { useEffect, useState } from 'react';
import { environments } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import type { ApiClientError } from '../../api/client';
import type { Environment } from '../../api/types';

async function load(): Promise<{ list: Environment[]; def: string }> {
  const [list, def] = await Promise.all([environments.list(), environments.getDefault().catch(() => ({ name: '' }))]);
  return { list, def: def.name };
}

export interface EnvironmentSelection {
  list: Environment[];
  defaultName: string;
  loading: boolean;
  error: ApiClientError | Error | null;
  name: string;
  selected?: Environment;
  select: (name: string) => void;
  allowProduction: boolean;
  setAllowProduction: (v: boolean) => void;
  // True when the pick is a production environment the user has not
  // unlocked yet: the caller must disable its Run button on this, and send
  // allowProduction with the request.
  productionBlocked: boolean;
}

// Environment picking for the flow "Run" panel and the run detail "Edit and
// rerun" panel. Both need more than a name: the engine refuses an
// environment with `production: true` unless the request carries
// allow_production (internal/env/guard.go), so the selection owns that tick
// as well, and keeping the list, the name and the tick in one hook is what
// stops a caller from wiring up the select but not the flag.
//
// /v1/environments has no per-item "is this the default" flag, so the
// default name comes from the separate /v1/environments/default call and is
// only used to mark an option and seed the initial selection.
export function useEnvironmentSelection(initial = ''): EnvironmentSelection {
  const { data, loading, error } = useAsync(load, []);
  const [name, setName] = useState(initial);
  const [allowProduction, setAllowProduction] = useState(false);

  useEffect(() => {
    if (data && !name && data.list.length > 0) {
      setName(data.list.some((e) => e.name === data.def) ? data.def : data.list[0].name);
    }
  }, [data, name]);

  const select = (next: string) => {
    setName(next);
    // The unlock is per environment: switching away from production and
    // back must not carry an earlier tick along with it.
    setAllowProduction(false);
  };

  const selected = data?.list.find((e) => e.name === name);
  return {
    list: data?.list ?? [],
    defaultName: data?.def ?? '',
    loading,
    error,
    name,
    selected,
    select,
    allowProduction,
    setAllowProduction,
    productionBlocked: !!selected?.production && !allowProduction,
  };
}

export function EnvironmentSelect({ selection }: { selection: EnvironmentSelection }) {
  const { list, defaultName, loading, error, name, select } = selection;

  if (loading) return <div className="text-xs text-slate-400">Loading environments…</div>;
  if (error) return <div className="text-xs text-red-600">{error.message}</div>;
  if (list.length === 0) return <div className="text-xs text-slate-400">No environments configured.</div>;

  return (
    <select
      value={name}
      onChange={(e) => select(e.target.value)}
      className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
    >
      {list.map((e) => (
        <option key={e.name} value={e.name}>
          {e.name}
          {e.name === defaultName ? ' (default)' : ''}
          {e.production ? ' [production]' : ''}
        </option>
      ))}
    </select>
  );
}

// Rendered separately from the select so each panel can place it where its
// own layout has room (the flow run bar is a single flex row); it is the
// only way to clear productionBlocked, so a panel that offers Run must
// render it. Mirrors the Try It page's inline version
// (components/try/EnvironmentSelect.tsx).
export function ProductionNotice({ selection }: { selection: EnvironmentSelection }) {
  if (!selection.selected?.production) return null;
  return (
    <div className="rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300">
      <div className="mb-1 font-medium">This environment is marked production.</div>
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={selection.allowProduction}
          onChange={(e) => selection.setAllowProduction(e.target.checked)}
        />
        Allow running against production
      </label>
    </div>
  );
}
