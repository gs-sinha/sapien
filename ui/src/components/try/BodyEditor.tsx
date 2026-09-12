// Plain textarea JSON body editor: no CodeMirror/Monaco (performance
// budget), just live validation, a format button, and a "Whole shape" button
// that fills in every field the request schema declares. The payload is
// synthesized by the daemon (internal/example.Resolve / SynthesizeBody), not
// here: the UI, get_api, and the CLI must not each have their own idea of
// what a call to an operation looks like.
import { useState } from 'react';
import type { Body, RequestExample } from '../../api/types';

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
  loadFullShape,
}: {
  value: string;
  onChange: (text: string) => void;
  requestBody?: Body;
  loadFullShape?: () => Promise<RequestExample>;
}) {
  const error = jsonError(value);
  const [loading, setLoading] = useState(false);

  const format = () => {
    try {
      const parsed = JSON.parse(value);
      onChange(JSON.stringify(parsed, null, 2));
    } catch {
      // Leave the text alone; the error is already shown below.
    }
  };

  const fillWholeShape = async () => {
    if (!loadFullShape) return;
    setLoading(true);
    try {
      const ex = await loadFullShape();
      onChange(ex.body !== undefined && ex.body !== null ? JSON.stringify(ex.body, null, 2) : '');
    } catch {
      // Leave the body alone; the reader can still type one.
    } finally {
      setLoading(false);
    }
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
            onClick={fillWholeShape}
            disabled={!requestBody?.schema || !loadFullShape || loading}
            title="Fill in every field the schema declares, including optional ones"
            className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-600 hover:text-slate-900 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:text-white"
          >
            {loading ? 'Filling…' : 'Whole shape'}
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
