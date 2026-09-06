import { useEffect, useRef, useState } from 'react';
import type { InputSpec } from '../../api/types';

// Renders one control per flow input (InputSpec.type: string|integer|number|
// boolean|object|array), prefilled from its default, or -- when the caller
// has no InputSpec map at all (a flowless run's "edit and rerun", which only
// has the run's raw `inputs` values, no type info) -- a single JSON textarea
// seeded from `initial`. Every field is kept as a raw string internally (a
// boolean is a "true"/"false" <select>) so one coercion step at the end
// turns them into typed values; onChange fires with the coerced object and
// whether it's currently well-formed (object/array fields are JSON-parsed).
export interface InputsFormProps {
  specs?: Record<string, InputSpec>;
  initial?: Record<string, unknown>;
  onChange: (values: Record<string, unknown>, valid: boolean) => void;
}

function defaultRaw(spec: InputSpec, override?: unknown): string {
  const v = override !== undefined ? override : spec.default;
  if (v === undefined) return spec.type === 'boolean' ? 'false' : '';
  return typeof v === 'string' ? v : JSON.stringify(v);
}

function coerceField(spec: InputSpec, raw: string): { value: unknown; valid: boolean } {
  const type = spec.type || 'string';
  if (raw === '' && !spec.required) return { value: undefined, valid: true };
  switch (type) {
    case 'boolean':
      return { value: raw === 'true', valid: true };
    case 'integer':
    case 'number': {
      const n = Number(raw);
      return { value: n, valid: raw.trim() !== '' && !Number.isNaN(n) };
    }
    case 'object':
    case 'array':
      try {
        return { value: raw.trim() === '' ? (type === 'array' ? [] : {}) : JSON.parse(raw), valid: true };
      } catch {
        return { value: raw, valid: false };
      }
    default:
      return { value: raw, valid: true };
  }
}

export function InputsForm({ specs, initial, onChange }: InputsFormProps) {
  const specKeys = specs ? Object.keys(specs) : null;

  const [fields, setFields] = useState<Record<string, string>>(() => {
    if (specs) {
      const init: Record<string, string> = {};
      for (const [k, spec] of Object.entries(specs)) init[k] = defaultRaw(spec, initial?.[k]);
      return init;
    }
    return { __json: JSON.stringify(initial || {}, null, 2) };
  });

  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    if (specs) {
      const values: Record<string, unknown> = {};
      let valid = true;
      for (const [k, spec] of Object.entries(specs)) {
        const { value, valid: fieldValid } = coerceField(spec, fields[k] ?? '');
        if (!fieldValid) valid = false;
        if (value !== undefined) values[k] = value;
      }
      onChangeRef.current(values, valid);
    } else {
      try {
        const parsed = fields.__json.trim() === '' ? {} : JSON.parse(fields.__json);
        onChangeRef.current(parsed, true);
      } catch {
        onChangeRef.current({}, false);
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fields, specs]);

  if (!specKeys) {
    return (
      <div>
        <label className="mb-1 block text-xs text-slate-500">Inputs (JSON)</label>
        <textarea
          value={fields.__json}
          onChange={(e) => setFields({ __json: e.target.value })}
          rows={6}
          spellCheck={false}
          className="w-full rounded border border-slate-300 bg-white p-2 font-mono text-xs dark:border-slate-700 dark:bg-slate-900"
        />
      </div>
    );
  }

  if (specKeys.length === 0) {
    return <div className="text-xs text-slate-400">This flow declares no inputs.</div>;
  }

  return (
    <div className="space-y-2">
      {specKeys.map((k) => {
        const spec = specs![k];
        const type = spec.type || 'string';
        const raw = fields[k] ?? '';
        const field = coerceField(spec, raw);
        return (
          <label key={k} className="block text-sm">
            <span className="mb-1 flex items-center gap-1 text-xs text-slate-500">
              {k}
              {spec.required && <span className="text-red-500">*</span>}
              {spec.description && <span className="font-normal text-slate-400">- {spec.description}</span>}
            </span>
            {type === 'boolean' ? (
              <select
                value={raw || 'false'}
                onChange={(e) => setFields((f) => ({ ...f, [k]: e.target.value }))}
                className="w-full rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
              >
                <option value="true">true</option>
                <option value="false">false</option>
              </select>
            ) : type === 'object' || type === 'array' ? (
              <textarea
                value={raw}
                onChange={(e) => setFields((f) => ({ ...f, [k]: e.target.value }))}
                rows={3}
                spellCheck={false}
                className="w-full rounded border border-slate-300 bg-white p-2 font-mono text-xs dark:border-slate-700 dark:bg-slate-900"
              />
            ) : (
              <input
                type={type === 'integer' || type === 'number' ? 'number' : 'text'}
                value={raw}
                onChange={(e) => setFields((f) => ({ ...f, [k]: e.target.value }))}
                className="w-full rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
              />
            )}
            {!field.valid && <span className="text-xs text-red-500">invalid {type}</span>}
          </label>
        );
      })}
    </div>
  );
}
