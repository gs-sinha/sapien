import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { flows, operations } from '../api/client';
import type { FlowWithSource } from '../api/types-runs';
import { KeyValue } from '../components/KeyValue';
import { ActiveRunPanel, stepStatuses } from './flows/ActiveRunPanel';
import type { ActiveRun } from './flows/ActiveRunPanel';
import { FlowDescription } from './flows/FlowDescription';
import { FlowStepCard } from './flows/FlowStepCard';
import { RecentRuns } from './flows/RecentRuns';
import { RunPanel } from './flows/RunPanel';
import { isFlowOwnerKind, ShippedBadge, TierBadge, tierOf } from './flows/tier';
import { TierControl } from './flows/TierControl';
import { ValidatePanel } from './flows/ValidatePanel';
import { YamlSourcePanel } from './flows/YamlSourcePanel';
import { applyStepEditsToYaml, hasAnyEdits, loadStepEdits, saveStepEdits } from './flows/stepEdits';
import type { FlowStepEdits, StepEdit } from './flows/stepEdits';
import { useAsync } from '../lib/useAsync';
import { subscribe } from '../state/events';
import { pushToast } from '../state/toast';
import type { Operation, Run, ShipStatus } from '../api/types';

// GET /v1/flows/{id} answers a domain.Flow, which carries no `shipped` (only
// FlowSummary does): the workspace-tier ship badge shown in this page's
// header is filled in from a second, best-effort lookup of that flow's own
// summary. A failure there just means the badge doesn't show, not a page
// error.
type FlowDetail = FlowWithSource & { shipped?: ShipStatus };

async function loadFlow(id: string): Promise<FlowDetail> {
  const flow = await flows.get(id);
  let shipped: ShipStatus | undefined;
  try {
    const summaries = await flows.list(id);
    shipped = summaries.find((s) => s.id === id)?.shipped;
  } catch {
    // best effort; see comment above.
  }
  return { ...flow, shipped };
}

// Shown once per browser tab session, the first time "Save to flow" is
// used: js-yaml's dumper round-trips the data but not the file's comments
// or formatting (see stepEdits.ts's applyStepEditsToYaml), so a save is
// destructive to those even though it's never destructive to the data.
const SAVE_WARNING_KEY = 'sapien:flow-save-warned';

function hasShownSaveWarning(): boolean {
  try {
    return sessionStorage.getItem(SAVE_WARNING_KEY) === '1';
  } catch {
    return false;
  }
}

function markSaveWarningShown(): void {
  try {
    sessionStorage.setItem(SAVE_WARNING_KEY, '1');
  } catch {
    // sessionStorage unavailable; the warning just reappears next save.
  }
}

export default function FlowDetailPage() {
  const { id = '' } = useParams();
  const { data: flow, error, loading, reload } = useAsync<FlowDetail>(() => loadFlow(id), [id]);

  const [edits, setEdits] = useState<FlowStepEdits>({});
  const [savingToFlow, setSavingToFlow] = useState(false);
  const [opsByCallId, setOpsByCallId] = useState<Record<string, Operation>>({});
  const [activeRun, setActiveRun] = useState<ActiveRun | null>(null);

  // Edits are per-flow, persisted in sessionStorage (pages/flows/stepEdits.ts)
  // so navigating to a run and back keeps them; re-seed whenever the route's
  // flow id changes (React Router reuses this component across param
  // changes, so this can't be a lazy useState initializer).
  useEffect(() => {
    setEdits(id ? loadStepEdits(id) : {});
    setActiveRun(null);
  }, [id]);

  // Resolve each distinct step.call once the flow (re)loads, so FlowStepCard
  // can seed its Input editor's known-param/required chips. Best effort: a
  // lookup failure (deleted/renamed operation) just means no chips for that
  // step, not a page error.
  useEffect(() => {
    if (!flow) return;
    const callIds = Array.from(new Set((flow.steps || []).map((s) => s.call).filter((c): c is string => !!c)));
    let cancelled = false;
    Promise.all(
      callIds.map((opId) =>
        operations
          .get(opId)
          .then((op) => [opId, op] as const)
          .catch(() => [opId, undefined] as const),
      ),
    ).then((pairs) => {
      if (cancelled) return;
      const next: Record<string, Operation> = {};
      for (const [opId, op] of pairs) if (op) next[opId] = op;
      setOpsByCallId(next);
    });
    return () => {
      cancelled = true;
    };
  }, [flow]);

  useEffect(
    () =>
      subscribe('flow.changed', (e) => {
        if (!e.ids.flow_id || e.ids.flow_id === id) reload();
      }),
    [id, reload],
  );

  // Live progress for a run started from this page. POST /v1/flows/{id}/run
  // only answers once the run is over, so the run's id and its per-step
  // progress both come from the event stream while it's still in flight: the
  // run.started event names the id, and every run.step after that carries a
  // step's new status. Both updates go through a functional setState so the
  // subscription never closes over a stale ActiveRun.
  useEffect(() => {
    const un1 = subscribe('run.started', (e) => {
      if (e.ids.flow_id && e.ids.flow_id !== id) return;
      setActiveRun((prev) => (prev && prev.phase === 'running' && !prev.runId ? { ...prev, runId: e.ids.run_id } : prev));
    });
    const un2 = subscribe('run.step', (e) => {
      setActiveRun((prev) => {
        if (!prev || !e.ids.step_id || e.ids.run_id !== prev.runId) return prev;
        return { ...prev, steps: { ...prev.steps, [e.ids.step_id]: e.status || 'running' } };
      });
    });
    return () => {
      un1();
      un2();
    };
  }, [id]);

  const setStepEdit = (stepId: string, patch: Partial<StepEdit>) => {
    setEdits((prev) => {
      const next: FlowStepEdits = { ...prev, [stepId]: { ...prev[stepId], ...patch } };
      saveStepEdits(id, next);
      return next;
    });
  };

  const resetStep = (stepId: string) => {
    setEdits((prev) => {
      if (!(stepId in prev)) return prev;
      const next = { ...prev };
      delete next[stepId];
      saveStepEdits(id, next);
      return next;
    });
  };

  const saveToFlow = async () => {
    if (!flow) return;
    if (!hasShownSaveWarning()) {
      const ok = window.confirm(
        "Saving applies these step edits to the flow's YAML. Comments and formatting in the file are not preserved. Continue?",
      );
      if (!ok) return;
      markSaveWarningShown();
    }
    setSavingToFlow(true);
    try {
      const yaml = await applyStepEditsToYaml(flow.source || '', edits);
      await flows.update(flow.id, { yaml });
      saveStepEdits(id, {});
      setEdits({});
      pushToast('success', `Saved ${flow.id}`);
      reload();
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : 'save failed');
    } finally {
      setSavingToFlow(false);
    }
  };

  const onRunStart = () => setActiveRun({ phase: 'running', startedAt: Date.now(), steps: {} });
  const onRunFinish = (run: Run) =>
    setActiveRun((prev) => ({ phase: 'done', startedAt: prev?.startedAt ?? Date.now(), steps: prev?.steps || {}, runId: run.id, run }));
  const onRunFail = (message: string) =>
    setActiveRun((prev) => ({ phase: 'error', startedAt: prev?.startedAt ?? Date.now(), steps: prev?.steps || {}, runId: prev?.runId, message }));

  if (loading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="p-4 text-sm text-red-600">{error.message}</div>;
  if (!flow) return null;

  const dirty = hasAnyEdits(edits);
  const liveStatuses = activeRun ? stepStatuses(activeRun) : {};
  // A daemon from before tiers sends no owner_kind: no badge, no promote row.
  const hasTier = isFlowOwnerKind(flow.owner_kind);

  // Order follows what this page is for: run the flow, watch it, read its
  // steps. The YAML source and the validator sit at the bottom, collapsed,
  // rather than between the reader and the Run button.
  return (
    <div className="space-y-4 p-4">
      <div>
        <div className="mb-1 flex flex-wrap items-center gap-2">
          <h1 className="text-lg font-semibold">{flow.name || flow.id}</h1>
          {hasTier && <TierBadge ownerKind={flow.owner_kind} ownerId={flow.owner_id} />}
          {hasTier && tierOf(flow.owner_kind) === 'workspace' && <ShippedBadge shipped={flow.shipped} />}
        </div>
        <FlowDescription text={flow.description} />
        <div className="max-w-2xl">
          <KeyValue
            pairs={[
              ['id', flow.id],
              ['owner', `${flow.owner_kind || '-'}${flow.owner_id ? `/${flow.owner_id}` : ''}`],
              ['path', flow.path || '-'],
              ['tags', (flow.tags || []).join(', ') || '-'],
            ]}
          />
        </div>
        {hasTier && (
          <div className="mt-2">
            <TierControl flow={flow} onChanged={reload} />
          </div>
        )}
      </div>

      <div>
        <h2 className="mb-2 text-sm font-semibold">Run</h2>
        <div className="space-y-3">
          <RunPanel flow={flow} edits={edits} onStart={onRunStart} onFinish={onRunFinish} onFail={onRunFail} />
          {activeRun && (
            <ActiveRunPanel active={activeRun} totalSteps={(flow.steps || []).length} onDismiss={() => setActiveRun(null)} />
          )}
        </div>
      </div>

      <div>
        <div className="mb-2 flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold">Steps</h2>
          <button
            type="button"
            onClick={saveToFlow}
            disabled={!dirty || savingToFlow}
            title={dirty ? undefined : 'No pending edits.'}
            className="rounded border border-slate-300 px-2 py-1 text-xs disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700"
          >
            {savingToFlow ? 'Saving…' : 'Save to flow'}
          </button>
        </div>
        <div className="rounded border border-slate-200 dark:border-slate-800">
          {(flow.steps || []).map((s) => (
            <FlowStepCard
              key={s.id}
              step={s}
              edit={edits[s.id]}
              operation={s.call ? opsByCallId[s.call] : undefined}
              runStatus={liveStatuses[s.id]}
              onChangeEdit={(patch) => setStepEdit(s.id, patch)}
              onReset={() => resetStep(s.id)}
            />
          ))}
          {(!flow.steps || flow.steps.length === 0) && <div className="p-3 text-sm text-slate-400">No steps.</div>}
        </div>
      </div>

      <div>
        <h2 className="mb-2 text-sm font-semibold">Recent runs</h2>
        <RecentRuns flowId={flow.id} />
      </div>

      <YamlSourcePanel source={flow.source || ''} />

      <div>
        <h2 className="mb-2 text-sm font-semibold">Validate</h2>
        <ValidatePanel source={flow.source || ''} />
      </div>
    </div>
  );
}
