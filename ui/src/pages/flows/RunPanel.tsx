import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { flows, runs } from '../../api/client';
import { pushToast } from '../../state/toast';
import { applyStepEditsToYaml, hasAnyEdits } from './stepEdits';
import type { FlowStepEdits } from './stepEdits';
import { EnvironmentSelect } from './EnvironmentSelect';
import { InputsForm } from './InputsForm';
import type { Flow } from '../../api/types';

// edits defaults to {} so RunPanel still works standalone (e.g. in a test
// that doesn't wire up FlowDetailPage's editing state).
export function RunPanel({ flow, edits = {} }: { flow: Flow; edits?: FlowStepEdits }) {
  const [env, setEnv] = useState('');
  const [inputs, setInputs] = useState<Record<string, unknown>>({});
  const [inputsValid, setInputsValid] = useState(true);
  const [running, setRunning] = useState(false);
  const navigate = useNavigate();

  const withEdits = hasAnyEdits(edits);

  const run = async () => {
    if (!env) {
      pushToast('error', 'choose an environment first');
      return;
    }
    setRunning(true);
    try {
      // With pending step edits, run the patched YAML unsaved
      // (POST /v1/runs/source) instead of the flow's own saved steps, so
      // "Run with edits" always reflects exactly what's on screen.
      const result = withEdits
        ? await runs.runSource({ yaml: await applyStepEditsToYaml(flow.source || '', edits), opts: { environment: env, inputs, trigger: 'ui' } })
        : await flows.run(flow.id, { environment: env, inputs, trigger: 'ui' });
      navigate(`/ui/runs/${encodeURIComponent(result.id)}`);
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : 'run failed');
    } finally {
      setRunning(false);
    }
  };

  return (
    <div className="space-y-3">
      <div>
        <label className="mb-1 block text-xs text-slate-500">Environment</label>
        <EnvironmentSelect value={env} onChange={setEnv} />
      </div>
      <InputsForm specs={flow.inputs} onChange={(values, valid) => { setInputs(values); setInputsValid(valid); }} />
      <button
        type="button"
        onClick={run}
        disabled={running || !inputsValid || !env}
        className="rounded bg-slate-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
      >
        {running ? 'Running…' : withEdits ? 'Run with edits' : 'Run'}
      </button>
      {withEdits && <p className="text-xs text-slate-400">Running with unsaved step edits applied on top of the flow&apos;s saved YAML.</p>}
    </div>
  );
}
