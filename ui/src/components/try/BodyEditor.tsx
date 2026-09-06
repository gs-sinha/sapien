// Plain textarea JSON body editor: no CodeMirror/Monaco (performance
// budget), just live validation, a format button, and a "from schema"
// button that seeds a skeleton from the operation's request body schema.
import { useMemo } from 'react';
import { buildSkeleton } from '../../pages/try/skeleton';
import type { Body } from '../../api/types';

export function jsonError(text: string): string | null {
  if (text.trim() === '') return null;
  try {
    JSON.parse(text);
    return null;
  } catch (err) {
    return err instanceof Error ? err.message : 'invalid JSON';
  }
}

export function BodyEditor({
  value,
  onChange,
  requestBody,
}: {
  value: string;
  onChange: (text: string) => void;
  requestBody?: Body;
}) {
  const error = useMemo(() => jsonError(value), [value]);

  const format = () => {
    try {
      const parsed = JSON.parse(value);
      onChange(JSON.stringify(parsed, null, 2));
    } catch {
      // Leave the text alone; the error is already shown below.
    }
  };

  const fromSchema = () => {
    const skeleton = buildSkeleton(requestBody?.schema);
    onChange(JSON.stringify(skeleton, null, 2));
  };

  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <h3 className="text-xs font-semibold uppercase text-slate-500">
          Body{requestBody?.required && <span className="text-red-600"> *</span>}
        </h3>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={fromSchema}
            disabled={!requestBody?.schema}
            className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-600 hover:text-slate-900 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:text-white"
          >
            From schema
          </button>
          <button
            type="button"
            onClick={format}
            disabled={!!error || value.trim() === ''}
            className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-600 hover:text-slate-900 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:text-white"
          >
            Format
          </button>
        </div>
      </div>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
        rows={12}
        placeholder={requestBody ? '{\n  \n}' : 'This operation has no documented request body, but you can still send one.'}
        className={`w-full rounded border bg-white p-2 font-mono text-xs leading-5 dark:bg-slate-900 ${
          error ? 'border-red-400 dark:border-red-800' : 'border-slate-300 dark:border-slate-700'
        }`}
      />
      {error && <div className="mt-1 text-xs text-red-600">{error}</div>}
    </div>
  );
}
