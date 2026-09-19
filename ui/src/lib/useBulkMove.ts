// Shared "move N selected to folder" engine for the Flows/Memories/Examples
// list pages (PLAN §34f item 6 follow-up, item 2 and item 3): one per-item
// move call at a time, in order -- never in parallel, since the daemon
// moves a file and reindexes it per call -- continuing past a failure
// rather than aborting the rest of the selection. The list is reloaded
// once at the end, not per item, and only the ids that failed stay
// selected so the user can retry (reopen the popover, pick another
// folder) or drag them again. Used both from the action bar's
// FolderMovePopover (onMove) and directly from a sidebar drop (item 3).
import { useState } from 'react';

export interface BulkMoveFailure {
  id: string;
  message: string;
}

export interface UseBulkMove {
  selected: ReadonlySet<string>;
  setSelected: (next: Set<string>) => void;
  /** Failures from the most recent bulk move; cleared by a fresh move or
   *  by `clearSelection`. Empty after a fully successful move. */
  failures: BulkMoveFailure[];
  clearSelection: () => void;
  /** Moves `ids` to `folder`, one at a time and in order. Never throws --
   *  a per-item failure is caught, recorded, and moved past -- so the
   *  caller (FolderMovePopover) can always close and reload. Returns the
   *  failures, which FolderMovePopover reads to pick the toast wording. */
  bulkMove: (ids: readonly string[], folder: string, onProgress?: (done: number, total: number) => void) => Promise<BulkMoveFailure[]>;
}

export function useBulkMove(moveOne: (id: string, folder: string) => Promise<unknown>, reload: () => void): UseBulkMove {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [failures, setFailures] = useState<BulkMoveFailure[]>([]);

  const clearSelection = () => {
    setSelected(new Set());
    setFailures([]);
  };

  const bulkMove = async (
    ids: readonly string[],
    folder: string,
    onProgress?: (done: number, total: number) => void,
  ): Promise<BulkMoveFailure[]> => {
    const failed: BulkMoveFailure[] = [];
    for (let i = 0; i < ids.length; i++) {
      onProgress?.(i + 1, ids.length);
      try {
        await moveOne(ids[i], folder);
      } catch (e) {
        failed.push({ id: ids[i], message: e instanceof Error ? e.message : 'Move failed.' });
      }
    }
    reload();
    setFailures(failed);
    setSelected(failed.length > 0 ? new Set(failed.map((f) => f.id)) : new Set());
    return failed;
  };

  return { selected, setSelected, failures, clearSelection, bulkMove };
}
