import { useState } from 'react';
import { examples } from '../../api/client';
import { pushToast } from '../../state/toast';
import { Modal } from './Modal';
import type { ExampleScope, StepResult } from '../../api/types';

export function SaveExampleDialog({
  runId,
  step,
  onClose,
  onSaved,
}: {
  runId: string;
  step: StepResult;
  onClose: () => void;
  onSaved: (exampleId: string) => void;
}) {
  const [id, setId] = useState(`${step.operation || step.step_id}-example`);
  const [scope, setScope] = useState<ExampleScope>('workspace');
  const [description, setDescription] = useState('');
  const [tags, setTags] = useState('');
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
      const ex = await examples.fromRun({
        run_id: runId,
        step_id: step.step_id,
        id: id.trim(),
        description: description.trim() || undefined,
        scope,
        tags: tags
          .split(',')
          .map((t) => t.trim())
          .filter(Boolean),
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
        <label className="block">
          <span className="mb-1 block text-xs text-slate-500">Tags (comma separated)</span>
          <input
            value={tags}
            onChange={(e) => setTags(e.target.value)}
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
