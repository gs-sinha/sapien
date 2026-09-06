// "Save as example" for one flow step's *current* payload (its edited
// input/body/headers if the user has changed them, otherwise the step's
// own declared values) -- POST /v1/examples with the fixed fields the
// engine actually accepts (internal/server/handlers_examples.go's
// handleExampleCreate decodes straight into domain.SavedExample; `service`
// is derived server-side from `operation`, see internal/example/store.go,
// so it's never sent from here). Distinct from
// components/run/SaveExampleDialog.tsx, which saves from a *run's* recorded
// step via POST /v1/examples/from-run (a different endpoint, different
// fields) -- that dialog stays run-only per this wave's brief.
import { useState } from 'react';
import { examples } from '../../api/client';
import { Modal } from '../../components/run/Modal';
import { pushToast } from '../../state/toast';
import type { ExampleScope } from '../../api/types';

export function SaveStepExampleDialog({
  operation,
  input,
  body,
  headers,
  onClose,
  onSaved,
}: {
  operation: string;
  input: Record<string, unknown>;
  body: unknown;
  headers: Record<string, string>;
  onClose: () => void;
  onSaved: (exampleId: string) => void;
}) {
  const [id, setId] = useState(`${operation}-example`);
  const [scope, setScope] = useState<ExampleScope>('workspace');
  const [description, setDescription] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    if (!id.trim()) {
      setError('id is required');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const ex = await examples.create({
        id: id.trim(),
        operation,
        description: description.trim() || undefined,
        scope,
        input: Object.keys(input).length > 0 ? input : undefined,
        body,
        headers: Object.keys(headers).length > 0 ? headers : undefined,
      });
      pushToast('success', `Saved example ${ex.id}`);
      onSaved(ex.id);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'failed to save example';
      setError(message);
      pushToast('error', message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title="Save as example" onClose={onClose}>
      <div className="space-y-3 text-sm">
        <label className="block">
          <span className="mb-1 block text-xs text-slate-500">ID</span>
          <input
            value={id}
            onChange={(e) => setId(e.target.value)}
            className="w-full rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
          />
        </label>
        <label className="block">
          <span className="mb-1 block text-xs text-slate-500">Scope</span>
          <select
            value={scope}
            onChange={(e) => setScope(e.target.value as ExampleScope)}
            className="w-full rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="workspace">workspace</option>
            <option value="service">service</option>
          </select>
        </label>
        <label className="block">
          <span className="mb-1 block text-xs text-slate-500">Description</span>
          <input
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className="w-full rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
          />
        </label>
        {error && <div className="text-xs text-red-600">{error}</div>}
        <div className="flex justify-end gap-2 pt-1">
          <button type="button" onClick={onClose} className="rounded border border-slate-300 px-3 py-1 text-xs dark:border-slate-700">
            Cancel
          </button>
          <button
            type="button"
            onClick={submit}
            disabled={saving}
            className="rounded bg-slate-900 px-3 py-1 text-xs text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </Modal>
  );
}
