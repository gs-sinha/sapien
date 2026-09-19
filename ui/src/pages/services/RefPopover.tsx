// The "Change…" popover for ServiceRefControl (PLAN §34f item 2): loads
// GET /v1/services/{id}/branches, offers a filterable branch/tag list and a
// local-vs-team scope choice, and applies with PUT /v1/services/{id}/ref.
import { useEffect, useState } from 'react';
import { serviceRefs } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import { pushToast } from '../../state/toast';
import { buttonCls } from './BindingPanel';
import type { ServiceRefScope } from '../../api/types';

export function RefPopover({
  serviceId,
  currentRef,
  onApplied,
  onClose,
}: {
  serviceId: string;
  currentRef?: string;
  onApplied: () => void;
  onClose: () => void;
}) {
  const { data, error, loading } = useAsync(() => serviceRefs.branches(serviceId), [serviceId]);
  const [filter, setFilter] = useState('');
  const [scope, setScope] = useState<ServiceRefScope>('local');
  const [selected, setSelected] = useState(currentRef || '');
  const [busy, setBusy] = useState(false);

  // Seed the selection from the branches answer only once, and only if the
  // caller didn't already hand us a current ref (a local override, say).
  useEffect(() => {
    if (data && !selected) setSelected(data.current || data.default || '');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const f = filter.trim().toLowerCase();
  const branches = (data?.branches || []).filter((b) => !f || b.toLowerCase().includes(f));
  const tags = (data?.tags || []).filter((t) => !f || t.toLowerCase().includes(f));

  const apply = async () => {
    const ref = selected.trim();
    if (!ref) return;
    setBusy(true);
    try {
      await serviceRefs.set(serviceId, { ref, scope });
      pushToast('success', `${serviceId} now reads ${ref}${scope === 'team' ? ' (team ref)' : ' (this machine)'}.`);
      onApplied();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Set ref failed.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-label="Change ref"
      className="absolute left-0 top-full z-30 mt-1 w-80 rounded border border-slate-300 bg-white p-3 text-sm shadow-xl dark:border-slate-700 dark:bg-slate-900"
    >
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-xs font-semibold">Change ref</h3>
        <button type="button" onClick={onClose} className="text-xs text-slate-400 hover:text-slate-600 dark:hover:text-slate-200">
          Close
        </button>
      </div>

      {loading && <div className="p-2 text-xs text-slate-400">Loading branches…</div>}
      {error && <div className="p-2 text-xs text-red-600">{error.message}</div>}

      {data && (
        <>
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="filter branches and tags"
            aria-label="Filter branches and tags"
            className="mb-2 w-full rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
          />
          <div className="mb-2 max-h-48 overflow-y-auto rounded border border-slate-200 dark:border-slate-800">
            {branches.length === 0 && tags.length === 0 && <div className="p-2 text-xs text-slate-400">No matches.</div>}
            {branches.length > 0 && (
              <div>
                <div className="bg-slate-50 px-2 py-1 text-[10px] uppercase text-slate-400 dark:bg-slate-800">Branches</div>
                {branches.map((b) => (
                  <button
                    key={`b:${b}`}
                    type="button"
                    onClick={() => setSelected(b)}
                    className={`block w-full truncate px-2 py-1 text-left font-mono text-xs ${
                      selected === b ? 'bg-sky-100 dark:bg-sky-950' : 'hover:bg-slate-100 dark:hover:bg-slate-800'
                    }`}
                  >
                    {b}
                    {b === data.default ? ' (default)' : ''}
                  </button>
                ))}
              </div>
            )}
            {tags.length > 0 && (
              <div>
                <div className="bg-slate-50 px-2 py-1 text-[10px] uppercase text-slate-400 dark:bg-slate-800">Tags</div>
                {tags.map((t) => (
                  <button
                    key={`t:${t}`}
                    type="button"
                    onClick={() => setSelected(t)}
                    className={`block w-full truncate px-2 py-1 text-left font-mono text-xs ${
                      selected === t ? 'bg-sky-100 dark:bg-sky-950' : 'hover:bg-slate-100 dark:hover:bg-slate-800'
                    }`}
                  >
                    {t}
                  </button>
                ))}
              </div>
            )}
          </div>

          <div className="mb-2 space-y-1 text-xs">
            <label className="flex items-center gap-1.5">
              <input type="radio" name="ref-scope" checked={scope === 'local'} onChange={() => setScope('local')} />
              Only on this machine
            </label>
            <label className="flex items-center gap-1.5">
              <input type="radio" name="ref-scope" checked={scope === 'team'} onChange={() => setScope('team')} />
              For the team
            </label>
            {scope === 'team' && <p className="pl-5 text-slate-500">edits sapien.workspace.yaml; commit it from Changes</p>}
          </div>

          <div className="flex items-center justify-between gap-2">
            <span className="truncate font-mono text-xs text-slate-500">{selected || 'select a ref'}</span>
            <button type="button" disabled={busy || !selected.trim()} onClick={apply} className={buttonCls}>
              {busy ? 'Applying…' : 'Apply'}
            </button>
          </div>
        </>
      )}
    </div>
  );
}
