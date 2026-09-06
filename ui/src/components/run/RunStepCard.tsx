import { useState } from 'react';
import type { Ref } from 'react';
import { useNavigate } from 'react-router-dom';
import { rerunStepAlone } from '../../api/flowsExtra';
import { JsonView } from '../JsonView';
import { StatusPill } from '../StatusPill';
import { pushToast } from '../../state/toast';
import { SaveExampleDialog } from './SaveExampleDialog';
import type { StepResult } from '../../api/types';

type Tab = 'request' | 'response' | 'assertions' | 'extracted' | 'error';

function TimingRow({ step }: { step: StepResult }) {
  const t = step.timings;
  if (!t) return null;
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-500">
      <span>dns {Math.round(t.dns_ms)} ms</span>
      <span>connect {Math.round(t.connect_ms)} ms</span>
      <span>ttfb {Math.round(t.ttfb_ms)} ms</span>
      <span>total {Math.round(t.total_ms)} ms</span>
    </div>
  );
}

export function RunStepCard({
  step,
  runId,
  env,
  defaultOpen = false,
  scrollRef,
}: {
  step: StepResult;
  runId: string;
  env: string;
  defaultOpen?: boolean;
  scrollRef?: Ref<HTMLDivElement>;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const [tab, setTab] = useState<Tab>(step.error || (step.response && step.response.status >= 400) ? 'response' : 'request');
  const [rerunning, setRerunning] = useState(false);
  const [savingExample, setSavingExample] = useState(false);
  const [savedExampleId, setSavedExampleId] = useState<string | null>(null);
  const navigate = useNavigate();

  const hasAssertions = (step.assertions?.length || 0) > 0;
  const hasExtracted = step.out && Object.keys(step.out).length > 0;
  const hasError = !!step.error;

  const rerunAlone = async () => {
    setRerunning(true);
    try {
      const run = await rerunStepAlone(step, env);
      navigate(`/ui/runs/${encodeURIComponent(run.id)}`);
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : 'rerun failed');
    } finally {
      setRerunning(false);
    }
  };

  return (
    <div ref={scrollRef} className="border-b border-slate-100 dark:border-slate-900">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 px-3 py-2 text-left text-sm hover:bg-slate-50 dark:hover:bg-slate-900"
      >
        <span className="w-4 text-slate-400">{open ? '▾' : '▸'}</span>
        <span className="w-8 text-xs text-slate-400">#{step.index}</span>
        <span className="font-mono text-xs">{step.step_id}</span>
        <span className="flex-1 truncate text-slate-500">{step.operation}</span>
        {step.attempts && step.attempts > 1 && <span className="text-xs text-slate-400">&times;{step.attempts}</span>}
        {step.timings && <span className="text-xs text-slate-400">{Math.round(step.timings.total_ms)} ms</span>}
        <StatusPill status={step.status} />
      </button>
      {open && (
        <div className="border-t border-slate-100 bg-slate-50/50 p-3 dark:border-slate-900 dark:bg-slate-900/40">
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <TimingRow step={step} />
            <div className="ml-auto flex items-center gap-2">
              {savedExampleId && (
                <a href={`/ui/examples/${encodeURIComponent(savedExampleId)}`} className="text-xs text-sky-700 underline dark:text-sky-400">
                  View example
                </a>
              )}
              <button
                type="button"
                onClick={() => setSavingExample(true)}
                className="rounded border border-slate-300 px-2 py-1 text-xs text-slate-600 hover:bg-white dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800"
              >
                Save as example
              </button>
              <button
                type="button"
                onClick={rerunAlone}
                disabled={rerunning || !step.operation}
                className="rounded border border-slate-300 px-2 py-1 text-xs text-slate-600 hover:bg-white disabled:opacity-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800"
              >
                {rerunning ? 'Rerunning…' : 'Rerun step alone'}
              </button>
            </div>
          </div>

          <div className="mb-2 flex gap-1 border-b border-slate-200 text-xs dark:border-slate-800">
            {(
              [
                ['request', 'Request'],
                ['response', 'Response'],
                ...(hasAssertions ? [['assertions', 'Assertions'] as const] : []),
                ...(hasExtracted ? [['extracted', 'Extracted'] as const] : []),
                ...(hasError ? [['error', 'Error'] as const] : []),
              ] as Array<[Tab, string]>
            ).map(([key, label]) => (
              <button
                key={key}
                type="button"
                onClick={() => setTab(key)}
                className={`-mb-px border-b-2 px-2 py-1 ${
                  tab === key
                    ? 'border-sky-600 font-medium text-sky-700 dark:border-sky-400 dark:text-sky-400'
                    : 'border-transparent text-slate-500'
                }`}
              >
                {label}
              </button>
            ))}
          </div>

          {tab === 'request' &&
            (step.request ? <JsonView data={step.request} /> : <div className="text-xs text-slate-400">no request recorded</div>)}
          {tab === 'response' &&
            (step.response ? <JsonView data={step.response} /> : <div className="text-xs text-slate-400">no response recorded</div>)}
          {tab === 'assertions' && hasAssertions && <JsonView data={step.assertions} />}
          {tab === 'extracted' && hasExtracted && <JsonView data={step.out} />}
          {tab === 'error' && step.error && (
            <div className="text-sm text-red-600 dark:text-red-400">
              <div className="font-mono text-xs">{step.error.code}</div>
              <div>{step.error.message}</div>
              {step.error.details && <JsonView data={step.error.details} />}
            </div>
          )}
        </div>
      )}
      {savingExample && (
        <SaveExampleDialog
          runId={runId}
          step={step}
          onClose={() => setSavingExample(false)}
          onSaved={(id) => {
            setSavedExampleId(id);
            setSavingExample(false);
          }}
        />
      )}
    </div>
  );
}
