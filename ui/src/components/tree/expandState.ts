// Per-tree collapsed-folder set, persisted so a tree looks the same after a
// reload. Keyed by whatever string the caller passes as `treeKey` (e.g.
// "changes" or "folders:flows"), so unrelated trees never share state.
const PREFIX = 'sapien:tree-collapsed:';

export function loadCollapsed(treeKey: string): Set<string> {
  try {
    const raw = localStorage.getItem(PREFIX + treeKey);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    return new Set(Array.isArray(parsed) ? parsed.filter((k) => typeof k === 'string') : []);
  } catch {
    return new Set();
  }
}

export function saveCollapsed(treeKey: string, collapsed: ReadonlySet<string>): void {
  try {
    localStorage.setItem(PREFIX + treeKey, JSON.stringify(Array.from(collapsed)));
  } catch {
    // Private window or blocked storage: the expand state just doesn't
    // survive a reload, which is better than failing the toggle.
  }
}
