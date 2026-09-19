// Dependency-free tree builder shared by the Changes page (files grouped
// under folders, from a git status listing) and the folder sidebar on the
// Flows/Memories/Examples pages (folders only, item leaves hidden but still
// counted). Both pass a flat item list plus a `/`-separated path per item;
// buildTree infers folder ancestors purely from path segments and never
// compacts a single-child folder chain into one node -- every real folder
// needs to be its own selectable, independently expandable row.
export interface TreeNode<T> {
  /** Full `/`-joined path from the tree root; stable, and used as the row
   *  key, the localStorage expand-state key, and the keyboard-nav identity. */
  key: string;
  /** The last path segment. */
  name: string;
  /** True once some item's path nests below this node (an inferred
   *  ancestor). A node with no children is a leaf ("file"). */
  isFolder: boolean;
  /** 0 for a root-level node. */
  depth: number;
  children: TreeNode<T>[];
  /** The item itself, attached at the terminal segment of its own path.
   *  Undefined for a pure folder (an ancestor with no item of its own). */
  item?: T;
}

interface Builder<T> {
  key: string;
  name: string;
  depth: number;
  children: Map<string, Builder<T>>;
  item?: T;
}

function byFolderThenName<T>(a: TreeNode<T>, b: TreeNode<T>): number {
  if (a.isFolder !== b.isFolder) return a.isFolder ? -1 : 1;
  return a.name.localeCompare(b.name);
}

/**
 * Turns a flat list of items, each with a `/`-separated path, into nested
 * TreeNode roots: folders first, then files, alphabetical within each
 * group at every level. An item is attached to the terminal segment of its
 * own path; every segment before that is an inferred folder ancestor,
 * created on demand and shared by every item whose path passes through it.
 *
 * Edge case: if one item's path is itself a strict prefix of another's
 * (e.g. "a" and "a/b"), the "a" node ends up with both an attached item
 * and children, and renders as a folder (children win); its item is kept
 * on the node but not surfaced by the tree itself. Real repo/folder paths
 * shouldn't produce this, so it's left as a documented edge case rather
 * than a hard error.
 */
export function buildTree<T>(items: readonly T[], getPath: (item: T) => string): TreeNode<T>[] {
  const root: Builder<T> = { key: '', name: '', depth: -1, children: new Map() };

  for (const it of items) {
    const parts = (getPath(it) || '').split('/').filter((p) => p.length > 0);
    if (parts.length === 0) continue;
    let node = root;
    for (let i = 0; i < parts.length; i++) {
      const key = parts.slice(0, i + 1).join('/');
      let child = node.children.get(parts[i]);
      if (!child) {
        child = { key, name: parts[i], depth: node.depth + 1, children: new Map() };
        node.children.set(parts[i], child);
      }
      node = child;
    }
    node.item = it;
  }

  function finalize(b: Builder<T>): TreeNode<T> {
    const children = Array.from(b.children.values()).map(finalize).sort(byFolderThenName);
    return { key: b.key, name: b.name, isFolder: children.length > 0, depth: b.depth, children, item: b.item };
  }

  return Array.from(root.children.values()).map(finalize).sort(byFolderThenName);
}

// ---- traversal helpers ----

/** Depth-first list of the rows a <Tree> would currently paint: honors
 *  collapsed folders, and can hide leaf (file) rows entirely for a
 *  folders-only tree (the sidebar). */
export function flattenVisible<T>(nodes: readonly TreeNode<T>[], isExpanded: (key: string) => boolean, showLeaves = true): TreeNode<T>[] {
  const out: TreeNode<T>[] = [];
  const walk = (list: readonly TreeNode<T>[]) => {
    for (const n of list) {
      if (!n.isFolder && !showLeaves) continue;
      out.push(n);
      if (n.isFolder && isExpanded(n.key)) walk(n.children);
    }
  };
  walk(nodes);
  return out;
}

/** Every leaf (file) key at or under `node` (itself included if it's a
 *  leaf), optionally narrowed by `include` -- used to exclude rows that
 *  can never be checked (e.g. the Changes page's unpushed files) from both
 *  bulk-toggle and tri-state math. */
export function leafKeys<T>(node: TreeNode<T>, include: (n: TreeNode<T>) => boolean = () => true): string[] {
  if (!node.isFolder) return include(node) ? [node.key] : [];
  const out: string[] = [];
  for (const c of node.children) out.push(...leafKeys(c, include));
  return out;
}

/** Count of leaf items at or under `node` -- the roll-up badge on a folder row. */
export function countLeaves<T>(node: TreeNode<T>, include: (n: TreeNode<T>) => boolean = () => true): number {
  return leafKeys(node, include).length;
}

// ---- tri-state checkbox helpers ----

export type CheckState = 'checked' | 'unchecked' | 'indeterminate';

/**
 * A folder's checkbox state, derived purely from which of its descendant
 * leaves are present in `checked` -- there is no separate "folder checked"
 * flag to keep in sync, so it can never drift from the leaves. `checkable`
 * excludes leaves that can't be checked at all from both the total and the
 * checked count, so a folder made only of such files reads as "unchecked"
 * rather than a permanent, unclickable "indeterminate".
 */
export function folderCheckState<T>(
  node: TreeNode<T>,
  checked: ReadonlySet<string>,
  checkable: (n: TreeNode<T>) => boolean = () => true,
): CheckState {
  const keys = leafKeys(node, checkable);
  if (keys.length === 0) return 'unchecked';
  let n = 0;
  for (const k of keys) if (checked.has(k)) n++;
  if (n === 0) return 'unchecked';
  if (n === keys.length) return 'checked';
  return 'indeterminate';
}
