// List|Chart view toggle for the flow and run pages (PLAN §34f item 9),
// remembered per browser via localStorage the same best-effort way
// components/Collapsible.tsx remembers a section's open/closed state:
// wrapped in try/catch, falling back to the default ('list') whenever
// storage is unavailable or holds something unexpected.
import { useEffect, useState } from 'react';

export type ViewMode = 'list' | 'chart';

const STORAGE_PREFIX = 'sapien.viewMode.';

function loadViewMode(key: string): ViewMode {
  try {
    return localStorage.getItem(STORAGE_PREFIX + key) === 'chart' ? 'chart' : 'list';
  } catch {
    return 'list';
  }
}

function saveViewMode(key: string, mode: ViewMode): void {
  try {
    localStorage.setItem(STORAGE_PREFIX + key, mode);
  } catch {
    // Not persistable here; the toggle still works for this render.
  }
}

/** `key` identifies the page (e.g. "flow", "run") so each remembers its own choice. */
export function useViewMode(key: string): [ViewMode, (mode: ViewMode) => void] {
  const [mode, setMode] = useState<ViewMode>(() => loadViewMode(key));

  useEffect(() => {
    saveViewMode(key, mode);
  }, [key, mode]);

  return [mode, setMode];
}
