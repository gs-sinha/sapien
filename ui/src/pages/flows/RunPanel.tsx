import { useState } from 'react';
import { flows, runs } from '../../api/client';
import { pushToast } from '../../state/toast';
import { applyStepEditsToYaml, hasAnyEdits } from './stepEdits';
import type { FlowStepEdits } from './stepEdits';
import { EnvironmentSelect } from './EnvironmentSelect';
import { InputsForm } from './InputsForm';
import type { Flow, Run } from '../../api/types';

// edits defaults to {} so RunPanel still works standalone (e.g. in a test
// that doesn't wire up FlowDetailPage's editing state).
//
// The run itself is synchronous on the wire (POST /v1/flows/{id}/run returns
// the finished Run), so this panel doesn't navigate anywhere: it hands the
// lifecycle to the page via onStart/onFinish/onFail, which renders live
// progress from the event stream in place (see ActiveRunPanel) and links to
// the run's own page instead of jumping there and losing this one.
export function RunPanel({
  flow,
  edits = {},
  onStart,
  onFinish,
  onFail,
}: {
  flow: Flow;
  edits?: FlowStepEdits;
  onStart?: () => void;
  onFinish?: (run: Run) => void;
  onFail?: (message: string) => void;
}) {
  const [env, setEnv] = useState('');
  const [inputs, setInputs] = useState<Record<string, unknown>>({});
  const [inputsValid, setInputsValid] = useState(true);
  const [running, setRunning] = useState(false);
  // A flow that declares no inputs still accepts ad-hoc ones (InputsForm's
  // JSON textarea), but that textarea shouldn't occupy the run bar by
  // default -- the common case is "press Run".
  const declaredInputs = flow.inputs && Object.keys(flow.inputs).length > 0;
  const [adHocOpen, setAdHocOpen] = useState(false);

  const withEdits = hasAnyEdits(edits);

  const run = async () => {
    if (!env) {
      pushToast('error', 'choose an environment first');
      return;
    }
    setRunning(true);
    onStart?.();
    try {
      // With pending step edits, run the patched YAML unsaved
      // (POST /v1/runs/source) instead of the flow's own saved steps, so
      // "Run with edits" always reflects exactly what's on screen.
      const result = withEdits
        ? await runs.runSource({ yaml: await applyStepEditsToYaml(flow.source || '', edits), opts: { environment: env, inputs, trigger: 'ui' } })
        : await flows.run(flow.id, { environment: env, inputs, trigger: 'ui' });
      onFinish?.(result);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'run failed';
      pushToast('error', message);
      onFail?.(message);
    } finally {
      setRunning(false);
    }
  };

  return (
    <div className="space-y-3 rounded border border-slate-200 p-3 dark:border-slate-800">
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-56">
          <label className="mb-1 block text-xs text-slate-500">Environment</label>
          <EnvironmentSelect value={env} onChange={setEnv} />
        </div>
        <button
          type="button"
          onClick={run}
          disabled={running || !inputsValid || !env}
          className="rounded bg-slate-900 px-4 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
        >
          {running ? 'Running…' : withEdits ? 'Run with edits' : 'Run'}
        </button>
        {!declaredInputs && (
          <button
            type="button"
            onClick={() => {
              const next = !adHocOpen;
              setAdHocOpen(next);
              // Closing the editor discards what was typed in it, including a
              // half-written JSON object that would otherwise keep Run
              // disabled with nothing on screen to explain why.
              if (!next) {
                setInputs({});
                setInputsValid(true);
              }
            }}
            className="text-xs text-slate-500 hover:text-slate-800 dark:hover:text-slate-200"
          >
            {adHocOpen ? 'Hide inputs' : 'Inputs (JSON)'}
          </button>
        )}
        {withEdits && <p className="text-xs text-slate-400">Runs the flow&apos;s saved YAML with your unsaved step edits applied.</p>}
      </div>
      {(declaredInputs || adHocOpen) && (
        <InputsForm
          specs={declaredInputs ? flow.inputs : undefined}
          onChange={(values, valid) => {
            setInputs(values);
            setInputsValid(valid);
          }}
        />
      )}
    </div>
  );
}
