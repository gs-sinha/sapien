// Collapsible folder sidebar shared by the Flows/Memories/Examples pages
// (PLAN §34f item 6): an "All" row above the shared <Tree>, folders only
// (item rows are hidden -- see `showLeaves={false}` below -- but still
// counted onto their folder). Hidden entirely by the caller when no item in
// the list has a folder, so an unfoldered workspace looks exactly as it did
// before this feature existed.
//
// Drag-and-drop (§34f item 6 follow-up, item 3): dropping a row dragged
// from one of those pages' tables onto a folder here moves it (or the
// whole selection, if the dragged row was part of it -- see Table's drag
// payload). `onDropIds` is optional and turns this on; a caller that
// doesn't pass it gets a plain, non-interactive-to-drops sidebar exactly
// as before. An explicit "(root)" row is the drop target for "no folder",
// since "All" already means "no filter" and dropping there would be
// ambiguous; it only renders while a compatible drag is under way, so it
// never clutters ordinary browsing.
import { useMemo, useState } from 'react';
import { Tree, buildTree } from './tree';
import type { TreeNode } from './tree';
import { distinctFolders } from '../lib/folders';
import type { Foldered } from '../lib/folders';
import { getDragIds, hasDragIds } from '../lib/dragPayload';

// One synthetic leaf per foldered item, keyed on its folder path plus a
// unique suffix: buildTree then creates every ancestor folder even when it
// holds no items directly, and the hidden leaves are what the folder rows'
// roll-up counts are counting.
interface FolderLeaf {
  folder: string;
}

export function FolderSidebar({
  items,
  selected,
  onSelect,
  treeKey,
  className,
  onDropIds,
}: {
  items: readonly Foldered[];
  selected: string | null;
  onSelect: (folder: string | null) => void;
  treeKey: string;
  className?: string;
  /** `ids` moved, `folder` the drop target ("" for the root). Omit to
   *  leave the sidebar drop-inert. */
  onDropIds?: (ids: string[], folder: string) => void;
}) {
  const nodes = useMemo<TreeNode<FolderLeaf>[]>(() => {
    const leaves: Array<FolderLeaf & { __key: string }> = [];
    let i = 0;
    for (const it of items) {
      if (!it.folder) continue;
      leaves.push({ folder: it.folder, __key: `${it.folder}/\u0000${i++}` });
    }
    return buildTree(leaves, (l) => l.__key);
  }, [items]);

  // "" (root) as a value here is distinct from `overKey === null` (nothing
  // dragged over anything right now) -- the root drop row's own key is "".
  const [dragActive, setDragActive] = useState(false);
  const [overKey, setOverKey] = useState<string | null>(null);

  const drop = (folder: string, e: { dataTransfer: DataTransfer }) => {
    const ids = getDragIds(e);
    if (ids.length > 0) onDropIds?.(ids, folder);
    setDragActive(false);
    setOverKey(null);
  };

  if (distinctFolders(items).length === 0) return null;

  return (
    <div
      className={`w-full shrink-0 md:w-56 ${className || ''}`}
      onDragEnter={
        onDropIds
          ? (e) => {
              if (hasDragIds(e)) setDragActive(true);
            }
          : undefined
      }
      onDragOver={onDropIds ? (e) => e.preventDefault() : undefined}
      onDragLeave={
        onDropIds
          ? (e) => {
              if (!e.currentTarget.contains(e.relatedTarget as Node)) {
                setDragActive(false);
                setOverKey(null);
              }
            }
          : undefined
      }
    >
      <button
        type="button"
        onClick={() => onSelect(null)}
        aria-current={selected === null}
        className={`mb-1 block w-full rounded px-2 py-1 text-left text-sm ${
          selected === null
            ? 'bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900'
            : 'text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-900'
        }`}
      >
        All
      </button>
      {onDropIds && dragActive && (
        <div
          role="button"
          aria-label="(root) -- drop to remove from any folder"
          onDragEnter={(e) => {
            e.preventDefault();
            setOverKey('');
          }}
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => {
            e.preventDefault();
            drop('', e);
          }}
          className={`mb-1 block w-full rounded border border-dashed px-2 py-1 text-left text-sm text-slate-500 dark:text-slate-400 ${
            overKey === '' ? 'border-sky-500 bg-sky-50 dark:bg-sky-950' : 'border-slate-300 dark:border-slate-700'
          }`}
        >
          (root)
        </div>
      )}
      <Tree<FolderLeaf>
        nodes={nodes}
        treeKey={treeKey}
        selectedKey={selected}
        onSelect={(n) => onSelect(n.key)}
        showLeaves={false}
        dnd={
          onDropIds
            ? {
                overKey,
                onDragEnter: (n) => setOverKey(n.key),
                onDragLeave: (n) => setOverKey((k) => (k === n.key ? null : k)),
                onDrop: (n, e) => drop(n.key, e),
              }
            : undefined
        }
      />
    </div>
  );
}
