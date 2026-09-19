// Collapsible folder sidebar shared by the Flows/Memories/Examples pages
// (PLAN §34f item 6): an "All" row above the shared <Tree>, folders only
// (item rows are hidden -- see `showLeaves={false}` below -- but still
// counted onto their folder). Hidden entirely by the caller when no item in
// the list has a folder, so an unfoldered workspace looks exactly as it did
// before this feature existed.
import { useMemo } from 'react';
import { Tree, buildTree } from './tree';
import type { TreeNode } from './tree';
import { distinctFolders } from '../lib/folders';
import type { Foldered } from '../lib/folders';

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
}: {
  items: readonly Foldered[];
  selected: string | null;
  onSelect: (folder: string | null) => void;
  treeKey: string;
  className?: string;
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

  if (distinctFolders(items).length === 0) return null;

  return (
    <div className={`w-full shrink-0 md:w-56 ${className || ''}`}>
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
      <Tree<FolderLeaf>
        nodes={nodes}
        treeKey={treeKey}
        selectedKey={selected}
        onSelect={(n) => onSelect(n.key)}
        showLeaves={false}
      />
    </div>
  );
}
