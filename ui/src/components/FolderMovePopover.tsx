// "Move to folder…" action shared by the Flows/Memories/Examples list rows
// and their detail pages (PLAN §34f item 6): a small popover with a text
// input, autocompleting from the kind's existing folders; free text creates
// a new one, and "/" moves the item back to the root. Errors (Conflict, a
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
  label = 'Move to folder…',
}: {
  currentFolder?: string;
  /** Static autocomplete list -- used when the caller already has every
   *  sibling item in memory (the Flows/Memories/Examples list pages). */
  folders?: readonly string[];
  /** Fetched lazily the first time the popover opens, instead of a static
   *  list -- for a detail page, which doesn't otherwise load every sibling
   *  item just for this. A failed load just leaves the datalist empty
   *  (free text still works); never blocks opening the popover. */
  loadFolders?: () => Promise<string[]>;
  onMove: (folder: string) => Promise<unknown>;
  onMoved: () => void;
  label?: string;
}) {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState(currentFolder || '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loadedFolders, setLoadedFolders] = useState<string[]>([]);
  const ref = useRef<HTMLDivElement>(null);
  const listId = useId();
  const options = folders ?? loadedFolders;

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

  const submit = async () => {
    const folder = normalizeFolder(value);
    setBusy(true);
    setError(null);
    try {
      await onMove(folder);
      pushToast('success', folder ? `moved to ${folder}` : 'moved to root');
      setOpen(false);
      onMoved();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Move failed.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="relative inline-block" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="rounded border border-slate-300 px-1.5 py-0.5 text-xs dark:border-slate-700"
      >
        {label}
      </button>
      {open && (
        <div className="absolute right-0 z-10 mt-1 w-56 rounded border border-slate-200 bg-white p-2 text-left shadow-lg dark:border-slate-800 dark:bg-slate-900">
          <label className="mb-1 block text-xs text-slate-500" htmlFor={listId + '-input'}>
            Folder (&quot;/&quot; for root)
          </label>
          <input
            id={listId + '-input'}
            autoFocus
            list={listId}
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
            placeholder="/"
            className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
          />
          <datalist id={listId}>
            {options.map((f) => (
              <option key={f} value={f} />
            ))}
          </datalist>
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
              onClick={submit}
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
