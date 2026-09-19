import { useEffect, useRef, useState } from 'react';
import { JsonView } from '../../components/JsonView';
import { StatusPill } from '../../components/StatusPill';
import { blockHeaderText, containsStepId, isBlockStep } from '../../lib/flowStep';
import { KeyValueEditor, recordFromRows, rowsFromRecord } from './KeyValueEditor';
import { SaveStepExampleDialog } from './SaveStepExampleDialog';
import { bodyToText, isStepModified, mergedBody, mergedHeaders, mergedInput, textToBody } from './stepEdits';
import type { FlowStepEdits, StepEdit } from './stepEdits';
import type { Operation, Step } from '../../api/types';

/** A `when:` line shown on any step card (call or block) that has one (PLAN §34f.7). */
function WhenLine({ when }: { when?: string }) {
  if (!when) return null;
  return (
    <div className="text-xs text-slate-500 dark:text-slate-400">
      when: <span className="font-mono">{when}</span>
    </div>
  );
}

function Section({ title, data }: { title: string; data: unknown }) {
  if (data === undefined || data === null) return null;
  if (typeof data === 'object' && Object.keys(data as object).length === 0) return null;
  return (
    <div>
      <h4 className="mb-1 text-xs font-semibold uppercase text-slate-500">{title}</h4>
      <JsonView data={data} />
    </div>
  );
}

/**
 * FlowStepCard renders one step of a flow's definition -- a call/example
 * step's editable card, or (PLAN §34f items 7/8) a `when:` line on any step
 * and, for a loop block (`step.steps` set, no `call`/`example`), a group
 * card whose header names the block and whose nested steps render as their
 * own indented FlowStepCards. `edits`/`opsByCallId`/`liveStatuses` are the
 * whole-flow maps FlowDetailPage already keeps (keyed by step id, which
 * stays unique across the flow, blocks included), so recursing into a
 * block's `steps` just looks itself up again -- no separate props threading
 * per nesting level.
 */
export function FlowStepCard({
  step,
  edits,
  opsByCallId,
  liveStatuses,
  onChangeEdit,
  onReset,
  depth = 0,
  openStepId,
}: {
  step: Step;
  // Pending edits for every step in the flow, if any (see
  // pages/flows/stepEdits.ts). A step id's absence here means "still
  // whatever the flow's own step declares" -- FlowStepCard never edits
  // `step` itself.
  edits: FlowStepEdits;
  // The resolved operation (GET /v1/operations/{id}) for every step.call
  // that resolved, keyed by operation id; used only to seed the Input
  // editor's suggestion chips (declared param names, required ones marked).
  opsByCallId: Record<string, Operation>;
  // Every step's status in the run currently being watched on this page
  // (pages/flows/ActiveRunPanel.tsx), fed by run.step events as they
  // arrive, keyed by step id. A step id absent here hasn't reported yet
  // (or no run is being watched).
  liveStatuses: Record<string, string>;
  onChangeEdit: (stepId: string, patch: Partial<StepEdit>) => void;
  onReset: (stepId: string) => void;
  /** Indentation level: 0 at the top level, 1 inside a loop block (blocks cannot nest -- PLAN §34f.8 -- so this never goes further). */
  depth?: number;
  /** Selecting a node on the flow chart (PLAN §34f item 9) names a step id
   * here for one render; the matching card (or the block containing it)
   * opens itself in response, in addition to being scrolled to (done by
   * the page itself via `data-step-card-id`). */
  openStepId?: string;
}) {
  if (isBlockStep(step)) {
    return (
      <FlowBlockStepCard
        step={step}
        edits={edits}
        opsByCallId={opsByCallId}
        liveStatuses={liveStatuses}
        onChangeEdit={onChangeEdit}
        onReset={onReset}
        depth={depth}
        openStepId={openStepId}
      />
    );
  }
  return (
    <CallStepCard
      step={step}
      edit={edits[step.id]}
      operation={step.call ? opsByCallId[step.call] : undefined}
      runStatus={liveStatuses[step.id]}
      onChangeEdit={(patch) => onChangeEdit(step.id, patch)}
      onReset={() => onReset(step.id)}
      depth={depth}
      openStepId={openStepId}
    />
  );
}

/** A block's group card: header (id, foreach/repeat summary, live status)
 * plus its nested steps, each its own indented FlowStepCard. */
function FlowBlockStepCard({
  step,
  edits,
  opsByCallId,
  liveStatuses,
  onChangeEdit,
  onReset,
  depth,
  openStepId,
}: {
  step: Step;
  edits: FlowStepEdits;
  opsByCallId: Record<string, Operation>;
  liveStatuses: Record<string, string>;
  onChangeEdit: (stepId: string, patch: Partial<StepEdit>) => void;
  onReset: (stepId: string) => void;
  depth: number;
  openStepId?: string;
}) {
  const [open, setOpen] = useState(true);
  const runStatus = liveStatuses[step.id];
  const header = blockHeaderText(step);

  useEffect(() => {
    if (openStepId && containsStepId(step, openStepId)) setOpen(true);
  }, [openStepId, step]);

  return (
    <div className="border-b border-slate-100 dark:border-slate-900" style={{ paddingLeft: depth * 16 }} data-step-card-id={step.id}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 bg-slate-50/60 px-3 py-2 text-left text-sm hover:bg-slate-100 dark:bg-slate-900/40 dark:hover:bg-slate-900"
      >
        <span className="w-4 text-slate-400">{open ? '▾' : '▸'}</span>
        <span className="font-mono text-xs font-semibold">{step.id}</span>
        {runStatus && <StatusPill status={runStatus} />}
        <span className="flex-1 truncate font-mono text-xs text-slate-500" title={header.full}>
          {header.short}
        </span>
        <span className="text-xs text-slate-400">{(step.steps || []).length} steps</span>
      </button>
      {open && (
        <div>
          <div className="space-y-1 border-t border-slate-100 bg-slate-50/30 px-3 py-2 dark:border-slate-900 dark:bg-slate-900/20">
            <WhenLine when={step.when} />
            {step.break_when && (
              <div className="text-xs text-slate-500 dark:text-slate-400">
                break_when: <span className="font-mono">{step.break_when}</span>
              </div>
            )}
            {step.on_error === 'continue' && <div className="text-xs text-slate-500 dark:text-slate-400">on_error: continue</div>}
          </div>
          {(step.steps || []).map((child) => (
            <FlowStepCard
              key={child.id}
              step={child}
              edits={edits}
              opsByCallId={opsByCallId}
              liveStatuses={liveStatuses}
              onChangeEdit={onChangeEdit}
              onReset={onReset}
              depth={depth + 1}
              openStepId={openStepId}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function CallStepCard({
  step,
  edit,
  operation,
  runStatus,
  onChangeEdit,
  onReset,
  depth,
  openStepId,
}: {
  step: Step;
  edit?: StepEdit;
  operation?: Operation;
  runStatus?: string;
  onChangeEdit: (patch: Partial<StepEdit>) => void;
  onReset: () => void;
  depth: number;
  openStepId?: string;
}) {
  const [open, setOpen] = useState(false);
  const [savingExample, setSavingExample] = useState(false);

  useEffect(() => {
    if (openStepId === step.id) setOpen(true);
  }, [openStepId, step.id]);
  const assertCount = step.assert?.length || 0;
  const modified = isStepModified(edit);

  const input = mergedInput(step, edit);
  const headers = mergedHeaders(step, edit);
  const body = mergedBody(step, edit);

  // The body textarea keeps its own raw-text draft rather than deriving
  // `value` fresh from `bodyToText(mergedBody(...))` on every render: that
  // would re-run JSON.stringify on each keystroke and fight the user's
  // cursor/formatting while still typing. The draft is only resynced to the
  // step's own body at the exact moment an edit is cleared (Reset, or "Save
  // to flow" clearing every step's edits) -- see the effect below.
  const [bodyDraft, setBodyDraft] = useState(() => bodyToText(body));
  const wasBodyEdited = useRef(edit?.body !== undefined);
  useEffect(() => {
    const isEditedNow = edit?.body !== undefined;
    if (!isEditedNow && wasBodyEdited.current) {
      setBodyDraft(bodyToText(step.body));
    }
    wasBodyEdited.current = isEditedNow;
  }, [edit?.body, step.body]);

  const bodyParsed = textToBody(bodyDraft);

  // Step.Input binds by name to path/query/header params (cookie params
  // aren't settable from a flow step), so those are what seed the Input
  // editor's suggestion chips -- Step.Headers is separate, arbitrary request
  // headers, not tied to the operation's declared params.
  const nonCookieParams = (operation?.params || []).filter((p) => p.in !== 'cookie');
  const inputKnownKeys = nonCookieParams.map((p) => p.name);
  const inputRequiredKeys = nonCookieParams.filter((p) => p.required).map((p) => p.name);

  const reset = () => {
    onReset();
    setBodyDraft(bodyToText(step.body));
  };

  return (
    <div className="border-b border-slate-100 dark:border-slate-900" style={{ paddingLeft: depth * 16 }} data-step-card-id={step.id}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 px-3 py-2 text-left text-sm hover:bg-slate-50 dark:hover:bg-slate-900"
      >
        <span className="w-4 text-slate-400">{open ? '▾' : '▸'}</span>
        <span className="font-mono text-xs">{step.id}</span>
        {step.when && <span className="text-xs text-slate-400" title={`when: ${step.when}`}>when</span>}
        {runStatus && <StatusPill status={runStatus} />}
        {modified && (
          <span className="rounded-full bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-300">
            modified
          </span>
        )}
        <span className="flex-1 truncate text-slate-500">{step.call || (step.example ? `example: ${step.example}` : '-')}</span>
        {assertCount > 0 && <span className="text-xs text-slate-400">{assertCount} assert</span>}
        {step.until && <span className="text-xs text-slate-400">until</span>}
      </button>
      {open && (
        <div className="space-y-3 border-t border-slate-100 bg-slate-50/50 p-3 dark:border-slate-900 dark:bg-slate-900/40">
          <WhenLine when={step.when} />
          <div className="flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              onClick={reset}
              disabled={!modified}
              className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-600 hover:text-slate-900 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:text-white"
            >
              Reset
            </button>
            <button
              type="button"
              onClick={() => setSavingExample(true)}
              disabled={!step.call || !bodyParsed.valid}
              title={!step.call ? 'This step has no operation to save an example for.' : undefined}
              className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-600 hover:text-slate-900 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:text-white"
            >
              Save as example
            </button>
          </div>

          {/* Values are always kept as plain text (see KeyValueEditor), so
              touching any row here flattens every row's value to a string --
              acceptable for `${...}` template-heavy flow inputs, but a
              pre-existing non-string input value (a number, bool, object)
              becomes its quoted string form once any row in this editor is
              edited. */}
          <KeyValueEditor
            title="Input"
            rows={rowsFromRecord(input)}
            onChange={(rows) => onChangeEdit({ input: recordFromRows(rows) })}
            knownKeys={inputKnownKeys}
            requiredKeys={inputRequiredKeys}
            addLabel="Add input"
          />

          <div>
            <div className="mb-1 flex items-center justify-between">
              <h4 className="text-xs font-semibold uppercase text-slate-500">Body</h4>
              <button
                type="button"
                onClick={() => {
                  if (!bodyParsed.valid) return;
                  const formatted = bodyToText(bodyParsed.value);
                  setBodyDraft(formatted);
                  onChangeEdit({ body: bodyParsed.value });
                }}
                disabled={!bodyParsed.valid || bodyDraft.trim() === ''}
                className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-600 hover:text-slate-900 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:text-white"
              >
                Format
              </button>
            </div>
            <textarea
              value={bodyDraft}
              onChange={(e) => {
                const text = e.target.value;
                setBodyDraft(text);
                onChangeEdit({ body: textToBody(text).value });
              }}
              rows={6}
              spellCheck={false}
              placeholder={'No body. A ${...} template is kept as a literal string; anything else must be valid JSON.'}
              className={`w-full rounded border bg-white p-2 font-mono text-xs leading-5 dark:bg-slate-900 ${
                bodyParsed.valid ? 'border-slate-300 dark:border-slate-700' : 'border-red-400 dark:border-red-800'
              }`}
            />
            {!bodyParsed.valid && (
              <div className="mt-1 text-[11px] text-red-500">Invalid JSON &mdash; kept as typed; fix it before running or saving.</div>
            )}
          </div>

          <KeyValueEditor
            title="Headers"
            rows={rowsFromRecord(headers)}
            onChange={(rows) => onChangeEdit({ headers: recordFromRows(rows) })}
            addLabel="Add header"
          />

          <Section title="Params" data={step.params} />
          <Section title="Extract" data={step.extract} />
          <Section title="Assert" data={step.assert} />
          <Section title="Until / Poll" data={step.until ? { until: step.until, poll: step.poll, timeout: step.timeout } : step.poll} />
        </div>
      )}
      {savingExample && step.call && (
        <SaveStepExampleDialog
          operation={step.call}
          input={input}
          body={body}
          headers={headers}
          onClose={() => setSavingExample(false)}
          onSaved={() => setSavingExample(false)}
        />
      )}
    </div>
  );
}
