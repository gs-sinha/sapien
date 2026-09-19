// A collapsible section header (PLAN §34f, "warnings on the service page and
// docs on the operation page collapse, remembered per browser"): a button
// with a chevron, a count, and aria-expanded, whose open/closed state is
// remembered in localStorage per `storageKey` -- so once a reader collapses
// "Warnings" on one service, it stays collapsed on the next one too, the
// same way ServiceDetailPage and OperationDetailPage already remember other
// per-browser preferences (e.g. state/theme.ts).
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';

const STORAGE_PREFIX = 'sapien.collapsible.';

function loadOpen(key: string, defaultOpen: boolean): boolean {
  try {
    const v = localStorage.getItem(STORAGE_PREFIX + key);
    if (v === null) return defaultOpen;
    return v === '1';
  } catch {
    // Private browsing, storage disabled, etc.: fall back to the default
    // every time rather than throwing.
    return defaultOpen;
  }
}

function saveOpen(key: string, open: boolean): void {
  try {
    localStorage.setItem(STORAGE_PREFIX + key, open ? '1' : '0');
  } catch {
    // Not persistable here; the section still toggles for this render.
  }
}

export function Collapsible({
  storageKey,
  title,
  count,
  defaultOpen = false,
  summary,
  children,
}: {
  /** Identifies this section across pages/instances, e.g. "service.warnings". */
  storageKey: string;
  title: string;
  count?: number;
  defaultOpen?: boolean;
  /** Shown truncated to one line under the header while collapsed, e.g. doc titles. */
  summary?: ReactNode;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(() => loadOpen(storageKey, defaultOpen));

  useEffect(() => {
    saveOpen(storageKey, open);
  }, [storageKey, open]);

  const heading = count === undefined ? title : `${title} (${count})`;

  return (
    <div className="mt-4">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-1.5 text-left text-sm font-semibold"
      >
        <span aria-hidden="true" className="inline-block w-3 text-slate-400">
          {open ? '▾' : '▸'}
        </span>
        <span>{heading}</span>
      </button>
      {!open && summary && <p className="mt-0.5 truncate pl-[18px] text-xs text-slate-400">{summary}</p>}
      {open && <div className="mt-2">{children}</div>}
    </div>
  );
}
