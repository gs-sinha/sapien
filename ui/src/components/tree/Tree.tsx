// Shared tree view for the Changes page's file tree and the folder sidebar
// on Flows/Memories/Examples. Renders whatever `buildTree` produced: indent
// guides, chevrons, roving-tabindex keyboard navigation, optional tri-state
// checkboxes, and a right-aligned slot per row (a badge, a state letter, an
// action). No drag/drop, no virtualization -- workspace repos and folder
// counts here are small enough that a plain DOM list is fine.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { DragEvent, KeyboardEvent, ReactNode } from 'react';
import { countLeaves, flattenVisible, folderCheckState } from './buildTree';
import type { TreeNode } from './buildTree';
import { loadCollapsed, saveCollapsed } from './expandState';

/** Optional per-node HTML5 drop-target wiring (PLAN §34f item 6 follow-up,
 *  item 3: dropping a dragged row onto a folder in the sidebar). Nothing
 *  here is Tree's own concern -- it just forwards drag events for nodes
 *  `canDrop` allows, and lets the caller drive the highlight via
 *  `overKey`, keeping Tree itself library-free and dependency-free. */
export interface TreeDnd<T> {
  /** The node currently under the pointer, for the highlight style. */
  overKey: string | null;
  /** Defaults to folder nodes only. */
  canDrop?: (node: TreeNode<T>) => boolean;
  onDragEnter?: (node: TreeNode<T>) => void;
  onDragLeave?: (node: TreeNode<T>) => void;
  onDrop: (node: TreeNode<T>, e: DragEvent<HTMLDivElement>) => void;
}

export interface TreeProps<T> {
  nodes: TreeNode<T>[];
  /** localStorage key for this tree's remembered expand state (e.g. "changes", "folders:flows"). */
  treeKey: string;
  selectedKey?: string | null;
  onSelect?: (node: TreeNode<T>) => void;
  checkable?: boolean;
  /** Leaf keys currently checked; a folder's box is derived from these, never stored separately. */
  checkedKeys?: ReadonlySet<string>;
  onToggleCheck?: (node: TreeNode<T>, next: boolean) => void;
  /** Leaves this returns false for get no checkbox at all, and are excluded
   *  from their ancestors' tri-state math and from a folder's bulk toggle
   *  (e.g. the Changes page's already-committed "unpushed" rows). */
  checkableFilter?: (node: TreeNode<T>) => boolean;
  renderLabel?: (node: TreeNode<T>) => ReactNode;
  renderRight?: (node: TreeNode<T>) => ReactNode;
  /** false hides leaf (file) rows entirely -- the folders-only sidebar. Their
   *  counts still roll up onto the folder rows that remain. */
  showLeaves?: boolean;
  className?: string;
  emptyLabel?: string;
  /** See `TreeDnd`. Omitted entirely -- the default -- means no row is a
   *  drop target, exactly as before this existed. */
  dnd?: TreeDnd<T>;
}

function cssEscape(s: string): string {
  // Only "," and quotes are meaningful inside the attribute selector below;
  // paths never carry the other CSS-special characters querySelector cares
  // about, so a small escape covers every real Sapien path.
  return s.replace(/["\\]/g, '\\$&');
}

export function Tree<T>({
  nodes,
  treeKey,
  selectedKey = null,
  onSelect,
  checkable = false,
  checkedKeys,
  onToggleCheck,
  checkableFilter = () => true,
  renderLabel,
  renderRight,
  showLeaves = true,
  className,
  emptyLabel,
  dnd,
}: TreeProps<T>) {
  const [collapsed, setCollapsed] = useState<Set<string>>(() => loadCollapsed(treeKey));
  useEffect(() => {
    setCollapsed(loadCollapsed(treeKey));
    // Only re-seed when the tree identity itself changes; re-reading on
    // every render would stomp a toggle made in the same tick as a parent
    // re-render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [treeKey]);

  const isExpanded = (key: string) => !collapsed.has(key);

  const visible = useMemo(() => flattenVisible(nodes, isExpanded, showLeaves), [nodes, collapsed, showLeaves]);

  const [focusedKey, setFocusedKey] = useState<string | null>(selectedKey ?? visible[0]?.key ?? null);
  useEffect(() => {
    if (selectedKey && visible.some((n) => n.key === selectedKey)) {
      setFocusedKey(selectedKey);
      return;
    }
    if (!focusedKey || !visible.some((n) => n.key === focusedKey)) setFocusedKey(visible[0]?.key ?? null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible, selectedKey]);

  const containerRef = useRef<HTMLDivElement>(null);

  const toggleExpand = (key: string, expand?: boolean) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      const willExpand = expand !== undefined ? expand : next.has(key);
      if (willExpand) next.delete(key);
      else next.add(key);
      saveCollapsed(treeKey, next);
      return next;
    });
  };

  const checkStateOf = (node: TreeNode<T>): 'checked' | 'unchecked' | 'indeterminate' => {
    if (!checkedKeys) return 'unchecked';
    if (!node.isFolder) return checkedKeys.has(node.key) ? 'checked' : 'unchecked';
    return folderCheckState(node, checkedKeys, checkableFilter);
  };

  const toggleCheck = (node: TreeNode<T>) => {
    if (!onToggleCheck) return;
    const next = checkStateOf(node) !== 'checked';
    onToggleCheck(node, next);
  };

  // The target row is always already in the DOM (it's a member of `visible`,
  // which is what's currently rendered), so this can focus it synchronously
  // rather than waiting a frame for a re-render that isn't needed.
  const focusRow = (key: string) => {
    containerRef.current?.querySelector<HTMLElement>(`[data-tree-key="${cssEscape(key)}"]`)?.focus();
  };

  const move = (delta: number) => {
    const idx = visible.findIndex((n) => n.key === focusedKey);
    const nextIdx = Math.max(0, Math.min(visible.length - 1, (idx < 0 ? 0 : idx) + delta));
    const n = visible[nextIdx];
    if (n) {
      setFocusedKey(n.key);
      focusRow(n.key);
    }
  };

  const parentKeyOf = (key: string): string | null => {
    const parts = key.split('/');
    parts.pop();
    return parts.length ? parts.join('/') : null;
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>, node: TreeNode<T>) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        move(1);
        break;
      case 'ArrowUp':
        e.preventDefault();
        move(-1);
        break;
      case 'ArrowRight':
        e.preventDefault();
        if (node.isFolder) {
          if (!isExpanded(node.key)) toggleExpand(node.key, true);
          else move(1);
        }
        break;
      case 'ArrowLeft':
        e.preventDefault();
        if (node.isFolder && isExpanded(node.key)) {
          toggleExpand(node.key, false);
        } else {
          const parentKey = parentKeyOf(node.key);
          if (parentKey) {
            setFocusedKey(parentKey);
            focusRow(parentKey);
          }
        }
        break;
      case 'Enter':
        e.preventDefault();
        onSelect?.(node);
        break;
      case ' ':
        e.preventDefault();
        if (checkable && (node.isFolder || checkableFilter(node))) toggleCheck(node);
        else if (node.isFolder) toggleExpand(node.key);
        break;
      default:
        break;
    }
  };

  if (nodes.length === 0) {
    return emptyLabel ? <div className="p-2 text-xs text-slate-400">{emptyLabel}</div> : null;
  }

  return (
    <div ref={containerRef} role="tree" aria-label={treeKey} className={className}>
      {visible.map((node) => {
        const expanded = node.isFolder ? isExpanded(node.key) : undefined;
        const selected = node.key === selectedKey;
        const state = checkable ? checkStateOf(node) : undefined;
        const showCheckbox = checkable && (node.isFolder || checkableFilter(node));
        const count = node.isFolder ? countLeaves(node) : undefined;
        const droppable = dnd && (dnd.canDrop ? dnd.canDrop(node) : node.isFolder);
        const droppedOn = droppable && dnd!.overKey === node.key;
        return (
          <div
            key={node.key}
            role="treeitem"
            data-tree-key={node.key}
            aria-expanded={node.isFolder ? expanded : undefined}
            aria-selected={selected}
            aria-level={node.depth + 1}
            tabIndex={node.key === focusedKey ? 0 : -1}
            onKeyDown={(e) => onKeyDown(e, node)}
            onFocus={() => setFocusedKey(node.key)}
            onClick={() => {
              onSelect?.(node);
              setFocusedKey(node.key);
            }}
            onDragEnter={
              droppable
                ? (e) => {
                    e.preventDefault();
                    dnd!.onDragEnter?.(node);
                  }
                : undefined
            }
            onDragOver={droppable ? (e) => e.preventDefault() : undefined}
            onDragLeave={
              droppable
                ? (e) => {
                    if (!e.currentTarget.contains(e.relatedTarget as Node)) dnd!.onDragLeave?.(node);
                  }
                : undefined
            }
            onDrop={
              droppable
                ? (e) => {
                    e.preventDefault();
                    dnd!.onDrop(node, e);
                  }
                : undefined
            }
            className={`flex cursor-pointer items-center gap-1 rounded px-1 py-1 text-sm outline-none ${
              droppedOn
                ? 'ring-2 ring-inset ring-sky-500 bg-sky-50 dark:bg-sky-950'
                : selected
                  ? 'bg-sky-50 dark:bg-sky-950'
                  : 'hover:bg-slate-50 dark:hover:bg-slate-900'
            } focus-visible:ring-1 focus-visible:ring-sky-500`}
          >
            {Array.from({ length: node.depth }).map((_, i) => (
              <span key={i} className="inline-block h-5 w-4 shrink-0 self-stretch border-l border-slate-200 dark:border-slate-800" />
            ))}
            {node.isFolder && (showLeaves || node.children.some((c) => c.isFolder)) ? (
              <button
                type="button"
                tabIndex={-1}
                onClick={(e) => {
                  e.stopPropagation();
                  toggleExpand(node.key);
                }}
                className="flex h-4 w-4 shrink-0 items-center justify-center text-slate-400"
                aria-hidden
              >
                {expanded ? '▾' : '▸'}
              </button>
            ) : (
              <span className="inline-block h-4 w-4 shrink-0" />
            )}
            {showCheckbox && (
              <input
                type="checkbox"
                checked={state === 'checked'}
                ref={(el) => {
                  if (el) el.indeterminate = state === 'indeterminate';
                }}
                onClick={(e) => e.stopPropagation()}
                onChange={() => toggleCheck(node)}
                aria-label={`Select ${node.name}`}
                className="h-3.5 w-3.5 shrink-0"
              />
            )}
            <span className="min-w-0 flex-1 truncate">{renderLabel ? renderLabel(node) : node.name}</span>
            {node.isFolder && count !== undefined && (
              <span className="flex shrink-0 items-center gap-1 text-xs text-slate-400">
                <span className="h-1 w-1 rounded-full bg-slate-400" />
                {count}
              </span>
            )}
            {renderRight && <span className="shrink-0">{renderRight(node)}</span>}
          </div>
        );
      })}
    </div>
  );
}
