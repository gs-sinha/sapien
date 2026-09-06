// Environment picker with production gating: a production environment is
// shown with a warning and the call stays blocked until the user
// explicitly ticks "allow production" (mirrors CallRequest.allow_production
// / RunOptions.allow_production).
import type { Environment } from '../../api/types';

export function EnvironmentSelect({
  environments,
  defaultName,
  value,
  onChange,
  allowProduction,
  onAllowProductionChange,
}: {
  environments: Environment[];
  defaultName?: string;
  value: string;
  onChange: (name: string) => void;
  allowProduction: boolean;
  onAllowProductionChange: (v: boolean) => void;
}) {
  const selected = environments.find((e) => e.name === value);

  return (
    <div>
      <h3 className="mb-1.5 text-xs font-semibold uppercase text-slate-500">Environment</h3>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      >
        {environments.map((env) => (
          <option key={env.name} value={env.name}>
            {env.name}
            {env.name === defaultName ? ' (default)' : ''}
            {env.production ? ' [production]' : ''}
          </option>
        ))}
      </select>
      {selected?.production && (
        <div className="mt-2 rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300">
          <div className="mb-1 font-medium">This environment is marked production.</div>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={allowProduction} onChange={(e) => onAllowProductionChange(e.target.checked)} />
            Allow running against production
          </label>
        </div>
      )}
    </div>
  );
}
