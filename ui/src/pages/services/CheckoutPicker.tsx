// A modal that browses this machine's filesystem through the daemon
// (GET /v1/services/{name}/checkouts?path=<dir>) so BindingPanel can offer
// "read from this checkout" without the browser ever learning an absolute
// path from a file dialog. Every git repository the walk meets is annotated
// with whether its origin is this service's team repository (`matches`);
// a repository that is a fork or mirror, or a matching checkout with no API
// package, can still be bound with the "bind anyway" checkbox (force: true).
import { useState } from 'react';
import { services } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import { pushToast } from '../../state/toast';
import { annotationLabel, buttonCls } from './BindingPanel';
import type { DirEntry, Service } from '../../api/types';

export function CheckoutPicker({
  service,
  onBound,
  onClose,
}: {
  service: string;
  onBound: (service: Service) => void;
  onClose: () => void;
}) {
  const [path, setPath] = useState<string | undefined>(undefined);
  const { data: listing, error, loading } = useAsync(() => services.browseCheckouts(service, path), [service, path]);
  const [busy, setBusy] = useState(false);
  const [force, setForce] = useState(false);

  const bind = async (target: string) => {
    setBusy(true);
    try {
      const svc = await services.bind(service, target, force);
      pushToast('success', `${service} now reads from ${target}.`);
      onBound(svc);
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Bind failed.');
    } finally {
      setBusy(false);
    }
  };

  const goUp = () => {
    if (listing?.parent) setPath(listing.parent);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" aria-label="Choose a local checkout">
      <div className="w-full max-w-lg rounded bg-white p-4 shadow-xl dark:bg-slate-900">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-sm font-semibold">Choose a local checkout</h2>
          <button type="button" onClick={onClose} className="text-xs text-slate-400 hover:text-slate-600 dark:hover:text-slate-200">
            Close
          </button>
        </div>

        <div className="mb-2 flex items-center gap-2">
          <button type="button" onClick={goUp} disabled={!listing?.parent} className={buttonCls}>
            Up
          </button>
          <span className="truncate font-mono text-xs text-slate-500">{listing?.path ?? '~'}</span>
        </div>

        {loading && <div className="p-2 text-sm text-slate-400">Loading…</div>}
        {error && <div className="p-2 text-sm text-red-600">{error.message}</div>}

        {listing && (
          <ul className="max-h-80 overflow-y-auto rounded border border-slate-200 dark:border-slate-800">
            {listing.entries.length === 0 && <li className="p-2 text-sm text-slate-400">No subdirectories.</li>}
            {listing.entries.map((entry) => (
              <CheckoutEntryRow key={entry.path} entry={entry} busy={busy} force={force} onDescend={() => setPath(entry.path)} onUse={() => bind(entry.path)} />
            ))}
          </ul>
        )}

        <label className="mt-3 flex items-center gap-2 text-xs text-slate-500">
          <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
          bind anyway (fork or mirror)
        </label>
      </div>
    </div>
  );
}

function CheckoutEntryRow({
  entry,
  busy,
  force,
  onDescend,
  onUse,
}: {
  entry: DirEntry;
  busy: boolean;
  force: boolean;
  onDescend: () => void;
  onUse: () => void;
}) {
  const annotation = entry.checkout ? annotationLabel(entry.checkout) : '';
  // Matching entries always offer "Use this checkout"; a non-matching
  // repository only offers it once "bind anyway" is checked, and clicking
  // its name descends into it instead (same as a plain directory).
  const showUse = entry.checkout && (entry.matches || force);
  const canDescend = !entry.checkout || !entry.matches;

  return (
    <li className="flex items-center justify-between gap-2 border-t border-slate-100 px-2 py-1.5 text-sm first:border-t-0 dark:border-slate-800">
      <div className="min-w-0 flex-1">
        {canDescend ? (
          <button type="button" onClick={onDescend} className="truncate text-left font-medium hover:underline">
            {entry.name}
          </button>
        ) : (
          <span className="truncate font-medium">{entry.name}</span>
        )}
        {entry.checkout && annotation && <div className="text-xs text-slate-500">{annotation}</div>}
        {entry.checkout && entry.matches && <div className="text-xs font-medium text-emerald-600 dark:text-emerald-400">matches</div>}
        {entry.checkout && entry.reason && (
          <div className={entry.matches ? 'text-xs text-amber-700 dark:text-amber-400' : 'text-xs text-slate-400'}>{entry.reason}</div>
        )}
      </div>
      {showUse && (
        <button type="button" disabled={busy} onClick={onUse} className={buttonCls}>
          Use this checkout
        </button>
      )}
    </li>
  );
}
