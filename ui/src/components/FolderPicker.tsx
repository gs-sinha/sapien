// A modal that browses this machine's filesystem through the daemon
// (GET /v1/fs/dirs?path=<dir>) so AddWorkspaceDialog can pick a parent
// directory or an existing checkout without the browser ever learning an
// absolute path from a file dialog. One level at a time: clicking a
// directory descends into it, and "Select this folder" (or a row's own
// "Select") hands the open directory back to the caller.
import { useState } from 'react';
import { fsApi } from '../api/client';
import { useAsync } from '../lib/useAsync';
import { buttonCls } from '../pages/services/BindingPanel';

export function FolderPicker({
  title = 'Choose a folder',
  onSelect,
  onClose,
}: {
  title?: string;
  onSelect: (dir: string) => void;
  onClose: () => void;
}) {
  const [path, setPath] = useState<string | undefined>(undefined);
  const { data: listing, error, loading } = useAsync(() => fsApi.dirs(path), [path]);

  const goUp = () => {
    if (listing?.parent) setPath(listing.parent);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" aria-label={title}>
      <div className="w-full max-w-lg rounded bg-white p-4 shadow-xl dark:bg-slate-900">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-sm font-semibold">{title}</h2>
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
              <li key={entry.path} className="flex items-center justify-between gap-2 border-t border-slate-100 px-2 py-1.5 text-sm first:border-t-0 dark:border-slate-800">
                <button type="button" onClick={() => setPath(entry.path)} className="min-w-0 flex-1 truncate text-left font-medium hover:underline">
                  {entry.name}
                </button>
                <button type="button" onClick={() => onSelect(entry.path)} className={buttonCls}>
                  Select
                </button>
              </li>
            ))}
          </ul>
        )}

        <div className="mt-3 flex justify-end">
          <button type="button" onClick={() => listing && onSelect(listing.path)} disabled={!listing} className={buttonCls}>
            Select this folder
          </button>
        </div>
      </div>
    </div>
  );
}
