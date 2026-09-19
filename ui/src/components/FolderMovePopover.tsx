// "Move to folder…" action shared by the Flows/Memories/Examples list rows
// and their detail pages (PLAN §34f item 6): a small popover listing the
// kind's existing folders -- one click moves the item there -- above a text
// input; free text creates a new folder, and "/" moves the item back to the
// root. The list is ours rather than a native <datalist>: with a datalist
// open, Chrome spends Enter on its own suggestion popup, so typing the name
// of an existing folder and pressing Enter closed the popover having moved
// nothing, which is the common case, not the odd one. Errors (Conflict, a
// read-only item) surface inline rather than only as a toast, since the
// popover is small enough that a toast could be missed or already gone by
// the time the user looks back at it.
import { useEffect, useId, useRef, useState } from 'react';
import { pushToast } from '../state/toast';

function normalizeFolder(raw: string): string {
  const trimmed = raw.trim();
  if (trimmed === '' || trimmed === '/') return '';
  return trimmed.replace(/^\/+/, '').replace(/\/+$/, '');
}

export function FolderMovePopover({
  currentFolder,
  folders,
  loadFolders,
  onMove,
  onMoved,
  label,
  count,
}: {
  currentFolder?: string;
  /** Static autocomplete list -- used when the caller already has every
   *  sibling item in memory (the Flows/Memories/Examples list pages). */
  folders?: readonly string[];
  /** Fetched lazily the first time the popover opens, instead of a static
   *  list -- for a detail page, which doesn't otherwise load every sibling
   *  item just for this. A failed load just leaves the list empty
   *  (free text still works); never blocks opening the popover. */
  loadFolders?: () => Promise<string[]>;
  /** `onProgress` is only ever passed (and only ever called) in bulk mode
   *  (`count` set): the caller runs its own sequential per-item moves and
   *  reports back after each one so the button can show "Moving D / N…".
   *  A resolved value that's an array of per-item failures (bulk mode
   *  only; anything else is ignored) drives the toast wording below --
   *  the caller never throws for an individual item's failure, since a
   *  partial failure still needs the list reloaded and the popover closed
   *  so the action bar's own inline error list (not this popover) is what
   *  the user sees next. */
  onMove: (folder: string, onProgress?: (done: number, total: number) => void) => Promise<unknown>;
  onMoved: () => void;
  /** Single-item mode (`count` omitted, the default): full button text,
   *  unchanged default "Move to folder…". Bulk mode (`count` given, from
   *  a list page's selection action bar): the plural noun for the toast
   *  ("flows"/"memories"/"examples") -- the button itself always reads
   *  "Move N to folder…" instead. */
  label?: string;
  /** Selected-row count; only set for the bulk action-bar usage. */
  count?: number;
}) {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState(currentFolder || '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [loadedFolders, setLoadedFolders] = useState<string[]>([]);
  const ref = useRef<HTMLDivElement>(null);
  const listId = useId();
  const options = folders ?? loadedFolders;
  // What is typed narrows the list; the folder the item is already in stays
  // listed (disabled) so the list reads as "where things are", not a diff.
  const needle = normalizeFolder(value).toLowerCase();
  const matching = options.filter((f) => f !== '' && (needle === '' || needle === (currentFolder || '').toLowerCase() || f.toLowerCase().includes(needle)));

  useEffect(() => {
    if (!open) return;
    setValue(currentFolder || '');
    setError(null);
    if (loadFolders) {
      let cancelled = false;
      loadFolders()
        .then((f) => {
          if (!cancelled) setLoadedFolders(f);
        })
        .catch(() => {
          // best effort; see prop comment above.
        });
      return () => {
        cancelled = true;
      };
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onDocClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDocClick);
    return () => document.removeEventListener('mousedown', onDocClick);
  }, [open]);

  const submit = async (target?: string) => {
    const folder = normalizeFolder(target ?? value);
    setBusy(true);
    setError(null);
    setProgress(null);
    try {
      const result = await onMove(folder, count !== undefined ? (done, total) => setProgress({ done, total }) : undefined);
      if (count !== undefined) {
        const dest = folder || 'root';
        const noun = label ?? 'items';
        const failedCount = Array.isArray(result) ? result.length : 0;
        const okCount = count - failedCount;
        if (failedCount === 0) pushToast('success', `moved ${count} ${noun} to ${dest}`);
        else if (okCount > 0) pushToast('error', `moved ${okCount}/${count} ${noun} to ${dest}; ${failedCount} failed`);
        else pushToast('error', `failed to move ${noun} to ${dest}`);
      } else {
        pushToast('success', folder ? `moved to ${folder}` : 'moved to root');
      }
      setOpen(false);
      onMoved();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Move failed.');
    } finally {
      setBusy(false);
      setProgress(null);
    }
  };

  const buttonLabel = progress ? `Moving ${progress.done} / ${progress.total}…` : count !== undefined ? `Move ${count} to folder…` : label ?? 'Move to folder…';

  return (
    <div className="relative inline-block" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="rounded border border-slate-300 px-1.5 py-0.5 text-xs dark:border-slate-700"
      >
        {buttonLabel}
      </button>
      {open && (
        <div className="absolute right-0 z-10 mt-1 w-56 rounded border border-slate-200 bg-white p-2 text-left shadow-lg dark:border-slate-800 dark:bg-slate-900">
          <label className="mb-1 block text-xs text-slate-500" htmlFor={listId + '-input'}>
            Move to folder (&quot;/&quot; for the root)
          </label>
          {matching.length > 0 && (
            <ul className="mb-2 max-h-40 overflow-auto" aria-label="Existing folders">
              {matching.map((f) => (
                <li key={f}>
                  <button
                    type="button"
                    disabled={busy || f === (currentFolder || '')}
                    onClick={() => void submit(f)}
                    className="block w-full truncate rounded px-1.5 py-1 text-left font-mono text-xs hover:bg-slate-100 disabled:opacity-50 dark:hover:bg-slate-800"
                    title={f === (currentFolder || '') ? 'already here' : `move to ${f}`}
                  >
                    {f}
                  </button>
                </li>
              ))}
            </ul>
          )}
          <input
            id={listId + '-input'}
            autoFocus
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                void submit();
              } else if (e.key === 'Escape') {
                setOpen(false);
              }
            }}
            placeholder="new or existing folder, e.g. billing/refunds"
            className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
          />
          {error && <div className="mt-1 text-xs text-red-600">{error}</div>}
          <div className="mt-2 flex justify-end gap-1">
            <button
              type="button"
              onClick={() => setOpen(false)}
              className="rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => void submit()}
              className="rounded border border-sky-600 bg-sky-50 px-2 py-1 text-xs text-sky-800 disabled:opacity-50 dark:border-sky-500 dark:bg-sky-950 dark:text-sky-300"
            >
              {busy ? 'Moving…' : 'Move'}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
