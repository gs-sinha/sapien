import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { flows, runs } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import { pushToast } from '../../state/toast';
import { EnvironmentSelect, ProductionNotice, useEnvironmentSelection } from '../../pages/flows/EnvironmentSelect';
import { InputsForm } from '../../pages/flows/InputsForm';
import { YamlEditor } from './YamlEditor';
import type { FlowWithSource } from '../../api/types-runs';
import type { Run } from '../../api/types';

// "Edit payload and rerun": prefill the flow's YAML (its saved `source` for
// a flow-backed run, or the run's own `flow_snapshot` for a flowless/ad hoc
// run, which never had a saved flow to fetch), let the user edit a step's
// body or input directly in the text, then run it unsaved via
// POST /v1/runs/source. After a successful run it offers "Save as flow":
// PUT to the same flow id when one exists, otherwise POST a new one (there
// is no existing flow id to PUT for a flowless run).
export function EditAndRerunPanel({ run, onClose }: { run: Run; onClose: () => void }) {
  const { data: flow, loading: flowLoading } = useAsync<FlowWithSource | null>(
    () => (run.flow_id ? flows.get(run.flow_id) : Promise.resolve(null)),
    [run.flow_id],
  );

  const initialSource = run.flow_id ? undefined : run.flow_snapshot || '';
  const [yaml, setYaml] = useState(initialSource ?? '');
  const [seeded, setSeeded] = useState(!run.flow_id);
  if (!seeded && flow?.source !== undefined) {
    // Seed the editor once the flow-backed source arrives (flowLoading was
    // true on mount so `yaml` couldn't be initialized synchronously).
    setYaml(flow.source);
    setSeeded(true);
  }

  // Seeded with the run's own environment, so rerunning a production run
  // starts blocked until the tick, exactly like picking production by hand.
  const envSel = useEnvironmentSelection(run.environment);
  const [inputs, setInputs] = useState<Record<string, unknown>>(run.inputs || {});
  const [inputsValid, setInputsValid] = useState(true);
  const [running, setRunning] = useState(false);
  const [newRunID, setNewRunID] = useState<string | null>(null);
  const [savePath, setSavePath] = useState('');
  const [saving, setSaving] = useState(false);
  const navigate = useNavigate();

  const handleRun = async () => {
    setRunning(true);
    try {
      const result = await runs.runSource({
        yaml,
        opts: { environment: envSel.name, inputs, allow_production: envSel.allowProduction, trigger: 'ui' },
      });
      setNewRunID(result.id);
      pushToast('success', `Ran as ${result.id}`);
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : 'run failed');
    } finally {
      setRunning(false);
    }
  };

  const saveAsFlow = async () => {
    setSaving(true);
    try {
      if (run.flow_id) {
        await flows.update(run.flow_id, { yaml });
        pushToast('success', `Saved flow ${run.flow_id}`);
      } else {
        const saved = await flows.create({ yaml, path: savePath.trim() || undefined });
        pushToast('success', `Saved flow ${saved.id}`);
      }
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : 'save failed');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="rounded border border-slate-200 p-3 dark:border-slate-800">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-sm font-semibold">Edit and rerun</h2>
        <button type="button" onClick={onClose} className="text-xs text-slate-400 hover:text-slate-700 dark:hover:text-slate-200">
          close
        </button>
      </div>

      {flowLoading ? (
        <div className="text-xs text-slate-400">Loading flow source…</div>
      ) : (
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
          <YamlEditor value={yaml} onChange={setYaml} />
          <div className="space-y-3">
            <div>
              <label className="mb-1 block text-xs text-slate-500">Environment</label>
              <EnvironmentSelect selection={envSel} />
            </div>
            <ProductionNotice selection={envSel} />
            <InputsForm
              specs={flow?.inputs}
              initial={run.inputs}
              onChange={(values, valid) => {
                setInputs(values);
                setInputsValid(valid);
              }}
            />
            <button
              type="button"
              onClick={handleRun}
              disabled={running || !inputsValid || !yaml.trim() || envSel.productionBlocked}
              className="rounded bg-slate-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
            >
              {running ? 'Running…' : 'Run'}
            </button>

            {newRunID && (
              <div className="space-y-2 rounded border border-emerald-200 bg-emerald-50 p-2 text-sm dark:border-emerald-900 dark:bg-emerald-950/40">
                <button
                  type="button"
                  onClick={() => navigate(`/ui/runs/${encodeURIComponent(newRunID)}`)}
                  className="text-sky-700 underline dark:text-sky-400"
                >
                  View run {newRunID}
                </button>
                <div className="flex items-center gap-2">
                  {!run.flow_id && (
                    <input
                      value={savePath}
                      onChange={(e) => setSavePath(e.target.value)}
                      placeholder="file path (optional)"
                      className="w-40 rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
                    />
                  )}
                  <button
                    type="button"
                    onClick={saveAsFlow}
                    disabled={saving}
                    className="rounded border border-slate-300 px-2 py-1 text-xs disabled:opacity-50 dark:border-slate-700"
                  >
                    {saving ? 'Saving…' : run.flow_id ? `Save as flow (${run.flow_id})` : 'Save as flow'}
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
