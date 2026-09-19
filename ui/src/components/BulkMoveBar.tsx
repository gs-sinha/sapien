// Slim action bar shown above the Flows/Memories/Examples table once at
// least one row is selected (PLAN §34f item 6 follow-up, item 2): "N
// selected · Move to folder… · Clear", plus -- after a partial failure --
// each failed id and its error message, inline, right here rather than in
// a toast that could be missed. `onMove` is handed straight to the shared
// FolderMovePopover in bulk mode (`count` set), which is what drives its
// "Move N to folder…" / "Moving D / N…" button text and its toast.
import { FolderMovePopover } from './FolderMovePopover';
import type { BulkMoveFailure } from '../lib/useBulkMove';

export function BulkMoveBar({
  count,
  itemLabel,
  folders,
  onMove,
  onClear,
  failures,
}: {
  count: number;
  /** Plural noun for the popover's toast, e.g. "flows". */
  itemLabel: string;
  folders: readonly string[];
  onMove: (folder: string, onProgress?: (done: number, total: number) => void) => Promise<unknown>;
  onClear: () => void;
  failures: readonly BulkMoveFailure[];
}) {
  if (count === 0) return null;
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2 rounded border border-slate-200 bg-slate-50 px-3 py-2 text-sm dark:border-slate-800 dark:bg-slate-900">
      <span>{count} selected</span>
      <span aria-hidden className="text-slate-400">
        ·
      </span>
      <FolderMovePopover folders={folders} count={count} label={itemLabel} onMove={onMove} onMoved={() => {}} />
      <span aria-hidden className="text-slate-400">
        ·
      </span>
      <button type="button" onClick={onClear} className="rounded border border-slate-300 px-1.5 py-0.5 text-xs dark:border-slate-700">
        Clear
      </button>
      {failures.length > 0 && (
        <div className="w-full text-xs text-red-600">
          {failures.map((f) => (
            <div key={f.id}>
              {f.id}: {f.message}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
