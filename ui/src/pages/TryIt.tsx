// /ui/try/:operationId -- build a request for one operation by hand, run
// it, and save what worked. See ui/README.md "Known gaps" for background:
// this is the wave-2 page that fills in "running from the UI" and "saving
// examples from a run".
import { useEffect, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { callOperation, environments, examples, operations } from '../api/client';
import { getRunHints } from '../api/tryExtra';
import type { ApiClientError } from '../api/client';
import { BodyEditor, jsonError } from '../components/try/BodyEditor';
import { EnvironmentSelect } from '../components/try/EnvironmentSelect';
import { ExamplePicker } from '../components/try/ExamplePicker';
import { HeadersEditor, headerRowsToRecord } from '../components/try/HeadersEditor';
import type { HeaderRow } from '../components/try/HeadersEditor';
import { ParamsForm } from '../components/try/ParamsForm';
import { ResultPanel } from '../components/try/ResultPanel';
import { SaveExampleDialog } from '../components/try/SaveExampleDialog';
import type { SaveExampleFields } from '../components/try/SaveExampleDialog';
import { defaultParamValue, loadFormState, saveFormState } from './try/formState';
import { pushToast } from '../state/toast';
import type { CallRequest, Environment, Operation, RequestExample, Run, SavedExample } from '../api/types';
import type { Hint } from '../api/types-try';

function applyExample(ex: SavedExample, setParams: (v: Record<string, unknown>) => void, setBodyText: (v: string) => void, setHeaderRows: (v: HeaderRow[]) => void) {
  setParams({ ...(ex.input || {}) });
  setBodyText(ex.body !== undefined && ex.body !== null ? JSON.stringify(ex.body, null, 2) : '');
  setHeaderRows(Object.entries(ex.headers || {}).map(([key, value]) => ({ key, value })));
}

const SOURCE_LABEL: Record<RequestExample['source'], string> = {
  verified: 'Prefilled from a verified example',
  saved: 'Prefilled from a saved example',
  contract: "Prefilled from the contract's own example",
  schema: 'Prefilled from the schema',
};

// PrefillBanner says where the payload in the form came from. "Verified" means
// this request really was sent and really worked; "schema" means every value
// is a placeholder. Without the label a reader cannot tell those apart, and
// the difference is the whole value of the prefill.
function PrefillBanner({ example }: { example: RequestExample }) {
  const trustworthy = example.source === 'verified';
  return (
    <div
      className={`rounded border px-2.5 py-1.5 text-xs ${
        trustworthy
          ? 'border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300'
          : 'border-slate-300 bg-slate-50 text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-400'
      }`}
    >
      {SOURCE_LABEL[example.source]}
      {example.source_id ? ` "${example.source_id}"` : ''}
      {example.note ? ` -- ${example.note}` : ''}
    </div>
  );
}

export default function TryIt() {
  const { operationId = '' } = useParams();
  const [searchParams] = useSearchParams();

  const [op, setOp] = useState<Operation | null>(null);
  const [opLoading, setOpLoading] = useState(true);
  const [opError, setOpError] = useState<Error | null>(null);

  const [envList, setEnvList] = useState<Environment[]>([]);
  const [defaultEnvName, setDefaultEnvName] = useState('');
  const [exampleList, setExampleList] = useState<SavedExample[]>([]);
  const [selectedExampleId, setSelectedExampleId] = useState('');

  const [params, setParams] = useState<Record<string, unknown>>({});
  const [bodyText, setBodyText] = useState('');
  const [headerRows, setHeaderRows] = useState<HeaderRow[]>([]);
  const [env, setEnv] = useState('');
  const [allowProduction, setAllowProduction] = useState(false);
  const [ready, setReady] = useState(false);

  const [submitting, setSubmitting] = useState(false);
  const [run, setRun] = useState<Run | null>(null);
  const [callError, setCallError] = useState<ApiClientError | Error | null>(null);
  const [hints, setHints] = useState<Hint[]>([]);

  // prefilled records where the form's starting payload came from, so the
  // page can say so instead of leaving a reader to guess whether the body in
  // front of them is known to work. Cleared as soon as an example is picked.
  const [prefilled, setPrefilled] = useState<RequestExample | null>(null);

  const [saveMode, setSaveMode] = useState<'from-run' | 'hand-written' | null>(null);
  const [saveSubmitting, setSaveSubmitting] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Load the operation, environments, and this operation's saved examples,
  // then decide the form's starting state: ?example= (always wins) beats
  // the per-operation sessionStorage snapshot, which beats a blank form.
  useEffect(() => {
    if (!operationId) return;
    let cancelled = false;
    setReady(false);
    setOpLoading(true);
    setOpError(null);
    setOp(null);
    setRun(null);
    setCallError(null);
    setHints([]);
    setSelectedExampleId('');

    async function load() {
      const [opRes, envsRes, defRes, exRes, reqEx] = await Promise.all([
        operations.get(operationId),
        environments.list(),
        environments.getDefault().catch(() => ({ name: '' })),
        examples.list({ operation: operationId }),
        // Supplementary: a blank form is still usable, so a failure here
        // must not stop the page from loading.
        operations.example(operationId).catch(() => null),
      ]);
      if (cancelled) return;
      setOp(opRes);
      setEnvList(envsRes);
      setDefaultEnvName(defRes.name);
      setExampleList(exRes);

      const exampleParam = searchParams.get('example') || '';
      const envParam = searchParams.get('env') || '';
      const saved = exampleParam ? null : loadFormState(operationId);

      let matchedExample: SavedExample | null = null;
      if (exampleParam) {
        matchedExample = exRes.find((e) => e.id === exampleParam) || null;
        if (!matchedExample) {
          matchedExample = await examples.get(exampleParam).catch(() => null);
        }
      }
      if (cancelled) return;

      setPrefilled(null);
      if (matchedExample) {
        applyExample(matchedExample, setParams, setBodyText, setHeaderRows);
        setSelectedExampleId(matchedExample.id);
        setAllowProduction(false);
      } else if (saved) {
        setParams(saved.params);
        setBodyText(saved.bodyText);
        setHeaderRows(saved.headers);
        setAllowProduction(saved.allowProduction);
      } else if (reqEx) {
        // Nothing to restore: start from the best request the daemon can
        // offer rather than an empty textarea the reader has to compile a
        // payload into by hand, and say where it came from.
        const blank: Record<string, unknown> = {};
        for (const p of opRes.params || []) blank[p.name] = defaultParamValue(p);
        setParams({ ...blank, ...(reqEx.input || {}) });
        setBodyText(reqEx.body !== undefined && reqEx.body !== null ? JSON.stringify(reqEx.body, null, 2) : '');
        setHeaderRows(Object.entries(reqEx.headers || {}).map(([key, value]) => ({ key, value })));
        setAllowProduction(false);
        // Nothing to say about a form there was nothing to prefill (a GET
        // with no parameters); the banner would be noise.
        if (reqEx.body !== undefined || reqEx.input || reqEx.headers) setPrefilled(reqEx);
      } else {
        const blank: Record<string, unknown> = {};
        for (const p of opRes.params || []) blank[p.name] = defaultParamValue(p);
        setParams(blank);
        setBodyText('');
        setHeaderRows([]);
        setAllowProduction(false);
      }

      const initialEnv = envParam || (saved && !matchedExample ? saved.env : '') || defRes.name || envsRes[0]?.name || '';
      setEnv(initialEnv);
      setReady(true);
    }

    load()
      .catch((err) => {
        if (!cancelled) setOpError(err instanceof Error ? err : new Error(String(err)));
      })
      .finally(() => {
        if (!cancelled) setOpLoading(false);
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [operationId]);

  // Persist the form (not the result) per operation so navigating away and
  // back keeps the payload; skipped until the initial load above finishes
  // so it never clobbers a restored/prefilled state with blanks.
  useEffect(() => {
    if (!ready) return;
    saveFormState(operationId, { params, bodyText, headers: headerRows, env, allowProduction });
  }, [ready, operationId, params, bodyText, headerRows, env, allowProduction]);

  const onSelectExample = (id: string) => {
    setSelectedExampleId(id);
    setPrefilled(null);
    if (!id) return;
    const match = exampleList.find((e) => e.id === id);
    if (match) applyExample(match, setParams, setBodyText, setHeaderRows);
  };

  const selectedEnv = envList.find((e) => e.name === env);
  const productionBlocked = !!selectedEnv?.production && !allowProduction;
  const bodyInvalid = !!jsonError(bodyText);
  const canRun = !!op && !!env && !productionBlocked && !bodyInvalid && !submitting;

  const handleRun = async () => {
    if (!op || !canRun) return;
    setSubmitting(true);
    setCallError(null);
    setRun(null);
    setHints([]);
    try {
      const body = bodyText.trim() ? JSON.parse(bodyText) : undefined;
      const req: CallRequest = {
        operation: op.id,
        params,
        body,
        headers: headerRowsToRecord(headerRows),
        env,
        allow_production: allowProduction,
        trigger: 'ui',
      };
      const result = await callOperation(req);
      setRun(result);
      if (result.status !== 'passed') {
        getRunHints(result.id)
          .then(setHints)
          .catch(() => {
            // Hints are supplementary; a lookup failure just means the
            // "Might explain it" section stays empty.
          });
      }
    } catch (err) {
      setCallError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setSubmitting(false);
    }
  };

  const submitSaveFromRun = async (fields: SaveExampleFields) => {
    if (!run) return;
    setSaveSubmitting(true);
    setSaveError(null);
    try {
      const stepId = run.steps?.[0]?.step_id;
      const saved = await examples.fromRun({
        run_id: run.id,
        step_id: stepId,
        id: fields.id,
        description: fields.description || undefined,
        scope: fields.scope,
        tags: fields.tags.length ? fields.tags : undefined,
        source: { kind: 'user' },
      });
      pushToast('success', `Saved example "${saved.id}"`);
      setSaveMode(null);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaveSubmitting(false);
    }
  };

  const submitSaveHandWritten = async (fields: SaveExampleFields) => {
    if (!op) return;
    if (bodyInvalid) {
      setSaveError('Body is not valid JSON.');
      return;
    }
    setSaveSubmitting(true);
    setSaveError(null);
    try {
      const body = bodyText.trim() ? JSON.parse(bodyText) : undefined;
      const saved = await examples.create({
        id: fields.id,
        operation: op.id,
        scope: fields.scope,
        description: fields.description || undefined,
        input: params,
        body,
        headers: headerRowsToRecord(headerRows),
        tags: fields.tags.length ? fields.tags : undefined,
      });
      pushToast('success', `Saved example "${saved.id}"`);
      setSaveMode(null);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaveSubmitting(false);
    }
  };

  if (opLoading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (opError) return <div className="p-4 text-sm text-red-600">{opError.message}</div>;
  if (!op) return null;

  return (
    <div className="p-4">
      <div className="mb-4">
        <h1 className="text-lg font-semibold">Try it: {op.id}</h1>
        <p className="text-sm text-slate-500">
          {op.http?.method} {op.http?.path}
          {op.summary ? ` -- ${op.summary}` : ''}
        </p>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="space-y-4">
          <div>
            <h3 className="mb-1.5 text-xs font-semibold uppercase text-slate-500">Start from a saved example</h3>
            <ExamplePicker examples={exampleList} value={selectedExampleId} onChange={onSelectExample} />
          </div>

          {prefilled && <PrefillBanner example={prefilled} />}

          <ParamsForm params={op.params || []} values={params} onChange={(name, value) => setParams((p) => ({ ...p, [name]: value }))} />

          <BodyEditor
            value={bodyText}
            onChange={(text) => {
              setBodyText(text);
              setPrefilled(null);
            }}
            requestBody={op.request_body}
            loadFullShape={() => operations.example(op.id, { fields: 'all' })}
          />

          <HeadersEditor rows={headerRows} onChange={setHeaderRows} />

          <EnvironmentSelect
            environments={envList}
            defaultName={defaultEnvName}
            value={env}
            onChange={setEnv}
            allowProduction={allowProduction}
            onAllowProductionChange={setAllowProduction}
          />

          <div className="flex gap-2">
            <button
              type="button"
              onClick={handleRun}
              disabled={!canRun}
              className="rounded bg-slate-900 px-4 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
            >
              {submitting ? 'Running…' : 'Run'}
            </button>
            <button
              type="button"
              onClick={() => {
                setSaveError(null);
                setSaveMode('hand-written');
              }}
              className="rounded border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-700"
            >
              Save as hand-written example
            </button>
          </div>
          {productionBlocked && <div className="text-xs text-amber-700 dark:text-amber-400">Tick "allow production" to run against this environment.</div>}
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold">Result</h2>
            {run && (
              <button
                type="button"
                onClick={() => {
                  setSaveError(null);
                  setSaveMode('from-run');
                }}
                className="rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700"
              >
                Save as example
              </button>
            )}
          </div>
          {!run && !callError && <div className="text-sm text-slate-400">Run the operation to see the result here.</div>}
          <ResultPanel run={run} callError={callError} hints={hints} />
        </div>
      </div>

      {saveMode === 'from-run' && (
        <SaveExampleDialog
          title="Save as example"
          suggestedId={`${op.id.replace(/\./g, '-')}-example`}
          onCancel={() => setSaveMode(null)}
          onSubmit={submitSaveFromRun}
          submitting={saveSubmitting}
          error={saveError}
        />
      )}
      {saveMode === 'hand-written' && (
        <SaveExampleDialog
          title="Save as hand-written example"
          suggestedId={`${op.id.replace(/\./g, '-')}-example`}
          onCancel={() => setSaveMode(null)}
          onSubmit={submitSaveHandWritten}
          submitting={saveSubmitting}
          error={saveError}
        />
      )}
    </div>
  );
}
