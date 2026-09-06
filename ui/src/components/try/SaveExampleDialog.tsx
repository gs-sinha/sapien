// Small modal collecting the fields common to both "save as example"
// flows (from a run, or hand-written from the current form): id, scope,
// description, tags. The caller does the actual POST (examples.fromRun or
// examples.create) so this component stays a plain, testable form.
import { useState } from 'react';
import type { ExampleScope } from '../../api/types';

export interface SaveExampleFields {
  id: string;
  scope: ExampleScope;
  description: string;
  tags: string[];
}

export function SaveExampleDialog({
  title,
  suggestedId,
  onCancel,
  onSubmit,
  submitting,
  error,
}: {
  title: string;
  suggestedId?: string;
  onCancel: () => void;
  onSubmit: (fields: SaveExampleFields) => void;
  submitting: boolean;
  error?: string | null;
}) {
  const [id, setId] = useState(suggestedId || '');
  const [scope, setScope] = useState<ExampleScope>('workspace');
  const [description, setDescription] = useState('');
  const [tagsText, setTagsText] = useState('');

  const submit = () => {
    if (!id.trim()) return;
    const tags = tagsText
      .split(',')
      .map((t) => t.trim())
      .filter(Boolean);
    onSubmit({ id: id.trim(), scope, description: description.trim(), tags });
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" aria-label={title}>
      <div className="w-full max-w-md rounded bg-white p-4 shadow-xl dark:bg-slate-900">
        <h2 className="mb-3 text-sm font-semibold">{title}</h2>
        <div className="space-y-3">
          <div>
            <label className="mb-1 block text-xs text-slate-500">
              Example id
              <input
                value={id}
                onChange={(e) => setId(e.target.value)}
                className="mt-1 w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-950"
              />
            </label>
          </div>
          <div>
            <label className="mb-1 block text-xs text-slate-500">
              Scope
              <select
                value={scope}
                onChange={(e) => setScope(e.target.value as ExampleScope)}
                className="mt-1 w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-950"
              >
                <option value="workspace">workspace</option>
                <option value="service">service</option>
              </select>
            </label>
          </div>
          <div>
            <label className="mb-1 block text-xs text-slate-500">
              Description
              <input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                className="mt-1 w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-950"
              />
            </label>
          </div>
          <div>
            <label className="mb-1 block text-xs text-slate-500">
              Tags (comma separated)
              <input
                value={tagsText}
                onChange={(e) => setTagsText(e.target.value)}
                className="mt-1 w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-950"
              />
            </label>
          </div>
        </div>

        {error && <div className="mt-3 text-xs text-red-600">{error}</div>}

        <div className="mt-4 flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="rounded border border-slate-300 px-3 py-1 text-sm dark:border-slate-700"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={submit}
            disabled={submitting || !id.trim()}
            className="rounded bg-slate-900 px-3 py-1 text-sm text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
          >
            {submitting ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  );
}
