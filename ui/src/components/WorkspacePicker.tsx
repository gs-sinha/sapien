import { useEffect, useRef, useState } from 'react';
import { workspacesApi } from '../api/client';
import { useEvents } from '../state/events';
import { useWorkspace } from '../state/workspace';
import { pushToast } from '../state/toast';
import { AddWorkspaceDialog } from './AddWorkspaceDialog';
import type { WorkspaceInfo } from '../state/workspace';

// The workspace switcher at the top of the nav.
//
// Switching changes a header, not a URL: every route means the same thing in
// every workspace, so the app reloads its data in place. It does force a full
// reload rather than trying to invalidate each page's state -- catalogs,
// flows, runs and memories are all per-workspace, and ids from one workspace
// never resolve in another, so anything held from before the switch is wrong.
export function WorkspacePicker() {
  const { current, list, setList, select } = useWorkspace();
  const [open, setOpen] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    workspacesApi
      .list()
      .then((ws) => {
        if (!cancelled) setList(ws);
      })
      .catch(() => {
        // A daemon too old for /v1/workspaces, or one that is down: the
        // picker just shows the current workspace and doesn't offer a
        // switch, rather than putting an error in the nav.
      });
    return () => {
      cancelled = true;
    };
  }, [setList]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const active = list.find((w) => w.dir === current) || list.find((w) => w.primary) || list[0];
  const label = active?.name || 'workspace';

  const switchTo = (w: WorkspaceInfo) => {
    setOpen(false);
    if (w.dir === (active?.dir || '')) return;
    if (w.error) {
      pushToast('error', `${w.name} is unavailable: ${w.error}`);
      return;
    }
    // Select the primary as "" so a tab tracks the daemon's own workspace
    // rather than pinning a path that may not be served next time.
    select(w.primary ? '' : w.dir);
    useEvents.getState().restart();
    window.location.reload();
  };

  return (
    <div ref={rootRef} className="relative mb-3">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        disabled={list.length === 0}
        title={active?.dir}
        className="flex w-full items-center gap-1 rounded px-2 py-1 text-left hover:bg-slate-100 disabled:cursor-default disabled:hover:bg-transparent dark:hover:bg-slate-900"
      >
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5 text-sm font-bold tracking-tight">
            <span
              aria-hidden="true"
              className="grid h-6 w-6 shrink-0 place-items-center rounded-md border border-slate-200 bg-white text-[13px] leading-none dark:border-slate-800 dark:bg-slate-900"
            >
              🗿
            </span>
            sapien
          </span>
          <span className="block truncate pl-[30px] text-xs text-slate-500">{label}</span>
        </span>
        {list.length > 1 && <span className="text-[10px] text-slate-400">▾</span>}
      </button>

      {open && list.length > 0 && (
        <div className="absolute left-0 right-0 top-full z-20 mt-1 overflow-hidden rounded border border-slate-200 bg-white shadow-lg dark:border-slate-700 dark:bg-slate-900">
          {list.map((w) => (
            <button
              key={w.dir}
              type="button"
              onClick={() => switchTo(w)}
              title={w.dir}
              className={`block w-full px-2 py-1.5 text-left text-xs hover:bg-slate-100 dark:hover:bg-slate-800 ${
                w.dir === active?.dir ? 'bg-slate-50 dark:bg-slate-800/60' : ''
              }`}
            >
              <span className="flex items-center gap-1">
                <span className="w-3 text-slate-400">{w.dir === active?.dir ? '✓' : ''}</span>
                <span className="truncate font-medium">{w.name}</span>
              </span>
              <span className="ml-4 block truncate text-[11px] text-slate-400">
                {w.error ? w.error : `${w.services ?? 0} services`}
              </span>
            </button>
          ))}
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              setShowAdd(true);
            }}
            className="block w-full border-t border-slate-100 px-2 py-1.5 text-left text-xs font-medium text-slate-500 hover:bg-slate-100 dark:border-slate-800 dark:hover:bg-slate-800"
          >
            Add workspace…
          </button>
        </div>
      )}

      {showAdd && (
        <AddWorkspaceDialog
          onClose={() => setShowAdd(false)}
          onAdded={(dir) => {
            // Same sequence as switchTo: point every later request at the new
            // workspace, drop the previous workspace's buffered events, then
            // reload so each page refetches in the new frame.
            select(dir);
            useEvents.getState().restart();
            window.location.reload();
          }}
        />
      )}
    </div>
  );
}
