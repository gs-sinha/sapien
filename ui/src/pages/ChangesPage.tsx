// The Changes page (PLAN §34f item 1): one tree of the workspace repository,
// like an editor's source-control panel. Covers every file in the workspace
// repo -- including sapien.workspace.yaml, environments/, and .gitignore --
// which no per-kind Commit (flows.commit, memories.commit, examples.commit)
// reaches. Bound service checkouts are listed for visibility but are
// read-only here: those repositories are the developer's own, committed
// with their code, never through Sapien.
import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { repoChanges } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { relative } from '../components/Timestamp';
import { Tree, buildTree, leafKeys } from '../components/tree';
import type { TreeNode } from '../components/tree';
import { YamlView } from '../components/YamlView';
import { useAsync } from '../lib/useAsync';
import { subscribe } from '../state/events';
import { useRepo } from '../state/repo';
import { pushToast } from '../state/toast';
import { CommitBox } from './changes/CommitBox';
import { DiffView } from './changes/DiffView';
import { generateCommitMessage } from './changes/commitMessage';
import type { RepoChangeFile, RepoDiff, RepoFileState, RepoServiceChange, RepoServiceChangeFile } from '../api/types';

const STATE_LETTER: Record<RepoFileState, string> = {
  untracked: 'U',
  modified: 'M',
  deleted: 'D',
  renamed: 'R',
  conflicted: '!',
  unpushed: '↑',
};

const STATE_TITLE: Record<RepoFileState, string> = {
  untracked: 'untracked',
  modified: 'modified',
  deleted: 'deleted',
  renamed: 'renamed',
  conflicted: 'conflicted',
  unpushed: 'unpushed',
};

const itemRoute: Record<string, (id: string) => string> = {
  flow: (id) => `/ui/flows/${encodeURIComponent(id)}`,
  memory: (id) => `/ui/memories/${encodeURIComponent(id)}`,
  example: (id) => `/ui/examples/${encodeURIComponent(id)}`,
};

// A file is checkable (can be selected for commit) unless it's already
// committed and only waiting on a push -- there is nothing left for Commit
// to do to it.
function checkable(node: TreeNode<RepoChangeFile>): boolean {
  return !node.isFolder && node.item?.state !== 'unpushed';
}

function fileLabel(node: TreeNode<RepoChangeFile>): ReactNode {
  if (node.isFolder) return node.name;
  const f = node.item;
  return (
    <span className="flex min-w-0 flex-col">
      <span className="truncate">{node.name}</span>
      {f?.kind && f.kind !== 'other' && (
        <span className="truncate text-xs text-slate-400">
          {f.kind}
          {f.title ? ` · ${f.title}` : ''}
        </span>
      )}
    </span>
  );
}

function fileRight(node: TreeNode<RepoChangeFile>): ReactNode {
  if (node.isFolder) return null;
  const state = node.item?.state;
  if (!state) return null;
  return (
    <span title={STATE_TITLE[state]} className="font-mono text-xs text-slate-500">
      {STATE_LETTER[state]}
    </span>
  );
}

function isYamlPath(path: string): boolean {
  return /\.(ya?ml)$/i.test(path);
}

function ServiceRow({ service }: { service: RepoServiceChange }) {
  if (service.mode === 'team') {
    return (
      <div className="flex items-center gap-1 rounded px-1 py-1 text-sm text-slate-600 dark:text-slate-400">
        <span>
          {service.name} · team{service.ref ? ` @ ${service.ref}` : ''}
        </span>
      </div>
    );
  }

  const tree = buildTree(service.files, (f) => f.path);
  return (
    <div className="py-1">
      <div
        title="this repository is yours; commit it with your code"
        className="flex items-center gap-1.5 rounded px-1 py-1 text-sm text-slate-700 dark:text-slate-300"
      >
        <span aria-hidden>🔒</span>
        <span>
          {service.name} · {service.branch || '?'} · {service.files.length} changed
        </span>
      </div>
      {tree.length > 0 && (
        <div className="ml-5">
          <Tree<RepoServiceChangeFile>
            nodes={tree}
            treeKey={`changes-service:${service.name}`}
            renderRight={(n) => fileRightForService(n)}
          />
        </div>
      )}
    </div>
  );
}

function fileRightForService(node: TreeNode<RepoServiceChangeFile>): ReactNode {
  if (node.isFolder) return null;
  const state = node.item?.state;
  if (!state) return null;
  return (
    <span title={STATE_TITLE[state]} className="font-mono text-xs text-slate-500">
      {STATE_LETTER[state]}
    </span>
  );
}

export default function ChangesPage() {
  const { data, error, loading, reload } = useAsync(() => repoChanges.get(), []);
  const status = useRepo((s) => s.status) || data?.status;

  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [message, setMessage] = useState('');
  const [messageEdited, setMessageEdited] = useState(false);
  const [committing, setCommitting] = useState(false);
  const [commitError, setCommitError] = useState<string | null>(null);

  const [diff, setDiff] = useState<RepoDiff | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);
  const [diffError, setDiffError] = useState<string | null>(null);

  const files = useMemo(() => data?.files || [], [data]);

  // Refetch on the workspace.repo event and on the catalog/file-change
  // events the other list pages already listen to (flow.changed,
  // memory.created/changed), debounced so a burst of events (a pull that
  // touches many files) doesn't fire a GET per file.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const debounced = () => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => reload(), 300);
    };
    const unsubs = [
      subscribe('workspace.repo', debounced),
      subscribe('flow.changed', debounced),
      subscribe('memory.created', debounced),
      subscribe('memory.changed', debounced),
      subscribe('catalog.changed', debounced),
    ];
    return () => {
      if (timer) clearTimeout(timer);
      unsubs.forEach((u) => u());
    };
  }, [reload]);

  // Drop any checked/selected paths the latest fetch no longer lists (e.g.
  // after a commit, or a file that got resolved externally).
  useEffect(() => {
    const known = new Set(files.map((f) => f.path));
    setChecked((prev) => {
      const next = new Set(Array.from(prev).filter((p) => known.has(p)));
      return next.size === prev.size ? prev : next;
    });
    if (selectedPath && !known.has(selectedPath)) setSelectedPath(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [files]);

  useEffect(() => {
    if (messageEdited) return;
    setMessage(generateCommitMessage(files, checked));
  }, [files, checked, messageEdited]);

  useEffect(() => {
    if (!selectedPath) {
      setDiff(null);
      setDiffError(null);
      return;
    }
    let cancelled = false;
    setDiffLoading(true);
    setDiffError(null);
    repoChanges
      .diff(selectedPath)
      .then((d) => {
        if (!cancelled) {
          setDiff(d);
          setDiffLoading(false);
        }
      })
      .catch((e) => {
        if (!cancelled) {
          setDiffError(e instanceof Error ? e.message : String(e));
          setDiffLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [selectedPath]);

  if (loading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="p-4 text-sm text-red-600">{error.message}</div>;
  if (!data || !status) return null;

  if (!status.in_git) {
    return (
      <div className="p-4">
        <EmptyState title="This workspace is not a git repository" />
      </div>
    );
  }

  const fileTree = buildTree(files, (f) => f.path);
  const allCheckableKeys = files.filter((f) => f.state !== 'unpushed').map((f) => f.path);
  const rootChecked = allCheckableKeys.length > 0 && allCheckableKeys.every((p) => checked.has(p));
  const rootIndeterminate = !rootChecked && allCheckableKeys.some((p) => checked.has(p));

  const toggleRoot = () => {
    setChecked((prev) => {
      const next = new Set(prev);
      const shouldCheck = !rootChecked;
      for (const p of allCheckableKeys) {
        if (shouldCheck) next.add(p);
        else next.delete(p);
      }
      return next;
    });
  };

  const handleToggleCheck = (node: TreeNode<RepoChangeFile>, next: boolean) => {
    setChecked((prev) => {
      const nextSet = new Set(prev);
      for (const k of leafKeys(node, checkable)) {
        if (next) nextSet.add(k);
        else nextSet.delete(k);
      }
      return nextSet;
    });
  };

  const commit = async () => {
    setCommitting(true);
    setCommitError(null);
    try {
      await repoChanges.commit(Array.from(checked), message);
      pushToast('success', `committed ${checked.size} file${checked.size === 1 ? '' : 's'}`);
      setChecked(new Set());
      setMessage('');
      setMessageEdited(false);
      reload();
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'Commit failed.';
      setCommitError(msg);
      pushToast('error', msg);
    } finally {
      setCommitting(false);
    }
  };

  const selectedFile = selectedPath ? files.find((f) => f.path === selectedPath) : undefined;
  const openHref = selectedFile?.kind && selectedFile.id ? itemRoute[selectedFile.kind]?.(selectedFile.id) : undefined;

  return (
    <div className="flex h-full flex-col overflow-hidden p-4">
      <h1 className="mb-3 text-lg font-semibold">Changes</h1>
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-hidden md:flex-row">
        {/* Left: tree + commit box */}
        <div className="flex min-w-0 flex-col overflow-hidden md:w-[40%] md:shrink-0">
          <div className="mb-2 text-sm text-slate-600 dark:text-slate-400">
            team · {status.branch || 'repo'}
            {status.behind > 0 ? ` ↓${status.behind}` : ''}
            {status.ahead > 0 ? ` ↑${status.ahead}` : ''}
          </div>
          <div className="min-h-0 flex-1 overflow-auto rounded border border-slate-200 dark:border-slate-800">
            {files.length === 0 ? (
              <div className="p-4">
                <EmptyState
                  title="Nothing to commit"
                  hint={status.fetched_at ? `Last checked ${relative(new Date(status.fetched_at))}.` : undefined}
                />
              </div>
            ) : (
              <div className="p-1">
                <div className="flex items-center gap-1.5 rounded px-1 py-1 text-sm font-medium">
                  <input
                    type="checkbox"
                    checked={rootChecked}
                    ref={(el) => {
                      if (el) el.indeterminate = rootIndeterminate;
                    }}
                    onChange={toggleRoot}
                    aria-label="Select all files"
                    disabled={allCheckableKeys.length === 0}
                    className="h-3.5 w-3.5"
                  />
                  <span>workspace repo</span>
                </div>
                <div className="ml-3">
                  <Tree<RepoChangeFile>
                    nodes={fileTree}
                    treeKey="changes"
                    checkable
                    checkedKeys={checked}
                    onToggleCheck={handleToggleCheck}
                    checkableFilter={checkable}
                    selectedKey={selectedPath}
                    onSelect={(n) => {
                      if (!n.isFolder) setSelectedPath(n.key);
                    }}
                    renderLabel={fileLabel}
                    renderRight={fileRight}
                  />
                </div>
              </div>
            )}
            {data.services.length > 0 && (
              <div className="border-t border-slate-200 p-1 dark:border-slate-800">
                <div className="rounded px-1 py-1 text-sm font-medium">Services</div>
                <div className="ml-3">
                  {data.services.map((s) => (
                    <ServiceRow key={s.name} service={s} />
                  ))}
                </div>
              </div>
            )}
          </div>
          <CommitBox
            status={status}
            selectedCount={checked.size}
            message={message}
            onMessageChange={(v) => {
              setMessage(v);
              setMessageEdited(true);
            }}
            onCommit={commit}
            committing={committing}
            commitError={commitError}
          />
        </div>

        {/* Right: diff pane */}
        <div className="min-w-0 flex-1 overflow-auto rounded border border-slate-200 p-3 dark:border-slate-800">
          {!selectedPath && <div className="text-sm text-slate-400">Select a file to see its diff.</div>}
          {selectedPath && diffLoading && <div className="text-sm text-slate-400">Loading…</div>}
          {selectedPath && diffError && <div className="text-sm text-red-600">{diffError}</div>}
          {selectedPath && diff && !diffLoading && (
            <div className="space-y-2">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="font-mono">{diff.path}</span>
                {openHref && (
                  <Link to={openHref} className="text-xs text-sky-700 underline dark:text-sky-400">
                    Open {selectedFile?.kind}
                  </Link>
                )}
              </div>
              {diff.binary && <div className="text-xs text-slate-400">Binary file: no diff shown.</div>}
              {diff.truncated && <div className="text-xs text-amber-600 dark:text-amber-400">Diff truncated.</div>}
              {/* `content` (an untracked file) wins over `diff` when both are
                  present -- the contract marks `diff` as always-present but
                  `content` as the one to show "for an untracked file", and a
                  brand-new file's `diff` is either absent or empty anyway. */}
              {!diff.binary && diff.content ? (
                isYamlPath(diff.path) ? (
                  <YamlView source={diff.content} />
                ) : (
                  <pre className="overflow-x-auto rounded bg-slate-50 p-3 font-mono text-xs leading-5 text-slate-800 dark:bg-slate-900 dark:text-slate-200">
                    {diff.content}
                  </pre>
                )
              ) : (
                !diff.binary && diff.diff && <DiffView diff={diff.diff} />
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
