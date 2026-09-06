// Renders one input per operation param (path/query/header/cookie),
// grouped by location, with a type hint and a required marker. Values are
// kept in the parent's `params` map (CallRequest.params: "path/query/header
// by name" -- see internal/engine/engine.go), typed by coerceParamValue.
import { coerceParamValue, defaultParamValue, paramDisplayString, typeHint } from '../../pages/try/formState';
import type { Param } from '../../api/types';

const locationLabel: Record<Param['in'], string> = {
  path: 'Path parameters',
  query: 'Query parameters',
  header: 'Header parameters',
  cookie: 'Cookie parameters',
};

function ParamInput({ param, value, onChange }: { param: Param; value: unknown; onChange: (value: unknown) => void }) {
  const schema = param.schema;

  if (schema?.kind === 'boolean') {
    return (
      <select
        value={value === true ? 'true' : value === false ? 'false' : ''}
        onChange={(e) => onChange(e.target.value === '' ? '' : e.target.value === 'true')}
        className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      >
        <option value="">(unset)</option>
        <option value="true">true</option>
        <option value="false">false</option>
      </select>
    );
  }

  if (schema?.enum && schema.enum.length > 0) {
    return (
      <select
        value={paramDisplayString(value)}
        onChange={(e) => onChange(coerceParamValue(param, e.target.value))}
        className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      >
        <option value="">(unset)</option>
        {schema.enum.map((v) => (
          <option key={String(v)} value={String(v)}>
            {String(v)}
          </option>
        ))}
      </select>
    );
  }

  const inputType = schema?.kind === 'integer' || schema?.kind === 'number' ? 'number' : 'text';
  return (
    <input
      type={inputType}
      value={paramDisplayString(value)}
      onChange={(e) => onChange(coerceParamValue(param, e.target.value))}
      placeholder={param.example !== undefined ? String(param.example) : undefined}
      className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
    />
  );
}

export function ParamsForm({
  params,
  values,
  onChange,
}: {
  params: Param[];
  values: Record<string, unknown>;
  onChange: (name: string, value: unknown) => void;
}) {
  if (params.length === 0) {
    return <div className="text-sm text-slate-400">This operation takes no path, query, header, or cookie parameters.</div>;
  }

  const groups: Param['in'][] = ['path', 'query', 'header', 'cookie'];

  return (
    <div className="space-y-4">
      {groups.map((loc) => {
        const inGroup = params.filter((p) => p.in === loc);
        if (inGroup.length === 0) return null;
        return (
          <div key={loc}>
            <h3 className="mb-1.5 text-xs font-semibold uppercase text-slate-500">{locationLabel[loc]}</h3>
            <div className="space-y-2">
              {inGroup.map((p) => (
                <div key={p.name} className="grid grid-cols-[9rem_1fr] items-start gap-2">
                  <label className="pt-1.5 text-sm">
                    {p.name}
                    {p.required && <span className="text-red-600"> *</span>}
                    <div className="text-[11px] font-normal text-slate-400">{typeHint(p)}</div>
                  </label>
                  <div>
                    <ParamInput
                      param={p}
                      value={p.name in values ? values[p.name] : defaultParamValue(p)}
                      onChange={(v) => onChange(p.name, v)}
                    />
                    {p.description && <div className="mt-0.5 text-[11px] text-slate-400">{p.description}</div>}
                  </div>
                </div>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}
