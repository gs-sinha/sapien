// Promote / move controls for one flow's tier: local -> team -> service.
// POST /v1/flows/{id}/rescope moves the file (and re-homes its flow-scoped
// memories); mirrors `sapien flow promote`. The service tier is only offered
// for services the flow actually calls and that this machine reads from a
// local checkout (Service.binding.writable), since a managed clone of a git
// source cannot take a file.
import { useEffect, useState } from 'react';
import { flows, services } from '../../api/client';
import { pushToast } from '../../state/toast';
import { tierLabel, tierOf } from './tier';
import type { Flow, FlowOwnerKind, Service } from '../../api/types';

// calledServices lists the services a flow's steps call: the prefix of each
// step.call before its first "." (operation ids are <service>.<operationId>).
export function calledServices(flow: Pick<Flow, 'steps'>): string[] {
  const out = new Set<string>();
  for (const s of flow.steps || []) {
    const call = s.call || '';
    const dot = call.indexOf('.');
    if (dot > 0) out.add(call.slice(0, dot));
  }
  return Array.from(out);
}

const buttonCls = 'rounded border border-slate-300 px-2 py-1 text-xs disabled:opacity-50 dark:border-slate-700';

export function TierControl({ flow, onChanged }: { flow: Flow; onChanged: () => void }) {
  const tier = tierOf(flow.owner_kind);
  const called = calledServices(flow);
  const calledKey = called.join(',');
  const [busy, setBusy] = useState(false);
  // Services this flow could move into: null until GET /v1/services answers.
  const [bindable, setBindable] = useState<Service[] | null>(null);

  useEffect(() => {
    if (tier !== 'workspace' || called.length === 0) {
      setBindable([]);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const list = await services.list();
        if (cancelled) return;
        setBindable(list.filter((s) => called.includes(s.name) && !!s.binding?.writable));
      } catch {
        if (!cancelled) setBindable([]);
      }
    })();
    return () => {
      cancelled = true;
    };
    // `called` is derived from the flow; its joined form is the stable dep.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tier, calledKey]);

  const move = async (kind: FlowOwnerKind, ownerId?: string) => {
    setBusy(true);
    try {
      await flows.rescope(flow.id, kind, ownerId);
      pushToast('success', `${flow.id} moved to ${tierLabel(kind, ownerId)}.`);
      onChanged();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Move failed.');
    } finally {
      setBusy(false);
    }
  };

  if (tier === 'local') {
    return (
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <button type="button" disabled={busy} onClick={() => move('workspace')} className={buttonCls}>
          {busy ? 'Moving…' : 'Promote to team'}
        </button>
        <span className="text-slate-400">Moves the file into flows/, where it ships with the workspace repo.</span>
      </div>
    );
  }

  if (tier === 'service') {
    return (
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <button type="button" disabled={busy} onClick={() => move('workspace')} className={buttonCls}>
          {busy ? 'Moving…' : 'Move to team'}
        </button>
        <span className="text-slate-400">Moves the file back into the workspace repo&apos;s flows/.</span>
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <button type="button" disabled={busy} onClick={() => move('local')} className={buttonCls}>
        {busy ? 'Moving…' : 'Move to local'}
      </button>
      {called.length > 0 && bindable === null && <span className="text-slate-400">Loading services…</span>}
      {bindable && bindable.length > 0 && (
        <select
          aria-label="Move to service"
          value=""
          disabled={busy}
          onChange={(e) => {
            if (e.target.value) move('service', e.target.value);
          }}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-xs disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900"
        >
          <option value="">Move to service…</option>
          {bindable.map((s) => (
            <option key={s.name} value={s.name}>
              {s.name}
            </option>
          ))}
        </select>
      )}
      {called.length > 0 && bindable && bindable.length === 0 && (
        <span className="text-slate-400">
          Move to service: none of {called.join(', ')} is read from a local checkout here (bind one on its service page).
        </span>
      )}
    </div>
  );
}
