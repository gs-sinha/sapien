import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { memories, folders as foldersApi } from '../api/client';
import { BulkMoveBar } from '../components/BulkMoveBar';
import { EmptyState } from '../components/EmptyState';
import { FolderBreadcrumb } from '../components/FolderBreadcrumb';
import { FolderMovePopover } from '../components/FolderMovePopover';
import { FolderSidebar } from '../components/FolderSidebar';
import { Table } from '../components/Table';
import { Timestamp } from '../components/Timestamp';
import { CommitButton, ItemTierBadge, MoveTierControl, PushButton, ShippedBadge } from '../components/tiers';
import { distinctFolders, underFolder } from '../lib/folders';
import { useAsync } from '../lib/useAsync';
import { useBulkMove } from '../lib/useBulkMove';
import { useFolderParam } from '../lib/useFolderParam';
import { subscribe } from '../state/events';
import { pushToast } from '../state/toast';
import type { Memory, MemoryScope, MemoryType } from '../api/types';

const memoryTypes: MemoryType[] = ['note', 'semantic', 'behavioral', 'testing', 'invariant', 'environment', 'gotcha'];
const memoryScopes: MemoryScope[] = ['personal', 'workspace', 'service', 'flow'];

export default function MemoriesPage() {
  const [q, setQ] = useState('');
  const [submitted, setSubmitted] = useState('');
  const [type, setType] = useState<MemoryType | ''>('');
  const [scope, setScope] = useState<MemoryScope | ''>('');
  const [service, setService] = useState('');
  const [folder, setFolder] = useFolderParam();

  const filter = { type: type || undefined, scope: scope || undefined, service: service || undefined };

  const { data, error, loading, reload } = useAsync(
    () =>
      submitted
        ? memories.search({ q: submitted, ...filter }).then((res) => res.map((s) => s.memory))
        : memories.list(filter),
    [submitted, type, scope, service],
  );

  useEffect(() => {
    const un1 = subscribe('memory.created', reload);
    const un2 = subscribe('memory.changed', reload);
    return () => {
      un1();
      un2();
    };
  }, [reload]);

  const { selected, setSelected, failures, clearSelection, bulkMove } = useBulkMove(
    (id, target) => foldersApi.moveMemory(id, target),
    reload,
  );

  // Selection is a view-level concern layered on top of whatever the
  // filters currently show; changing any of them invalidates it rather
  // than leaving a stale selection the user can no longer see.
  useEffect(() => {
    clearSelection();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [folder, submitted, type, scope, service]);

  const handleDropIds = async (ids: string[], targetFolder: string) => {
    setSelected(new Set(ids));
    const failed = await bulkMove(ids, targetFolder);
    const dest = targetFolder || 'root';
    if (failed.length === 0) pushToast('success', `moved ${ids.length} memories to ${dest}`);
    else if (failed.length < ids.length) pushToast('error', `moved ${ids.length - failed.length}/${ids.length} memories to ${dest}; ${failed.length} failed`);
    else pushToast('error', `failed to move memories to ${dest}`);
  };

  const folderList = useMemo(() => distinctFolders(data || []), [data]);
  const filtered = useMemo(() => underFolder(data || [], folder), [data, folder]);

  return (
    <div className="p-4">
      <h1 className="mb-4 text-lg font-semibold">Memories</h1>
      <form
        className="mb-3 flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setSubmitted(q);
        }}
      >
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="search memories"
          className="w-64 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        />
        <select
          value={type}
          onChange={(e) => setType(e.target.value as MemoryType | '')}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        >
          <option value="">any type</option>
          {memoryTypes.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
        <select
          value={scope}
          onChange={(e) => setScope(e.target.value as MemoryScope | '')}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        >
          <option value="">any scope</option>
          {memoryScopes.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <input
          value={service}
          onChange={(e) => setService(e.target.value)}
          placeholder="service"
          className="w-32 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        />
        <button type="submit" className="rounded border border-slate-300 px-3 py-1 text-sm dark:border-slate-700">
          Search
        </button>
      </form>

      <div className="flex flex-col gap-4 md:flex-row">
        <FolderSidebar items={data || []} selected={folder} onSelect={setFolder} treeKey="folders:memories" onDropIds={handleDropIds} />
        <div className="min-w-0 flex-1">
          {folderList.length > 0 && <FolderBreadcrumb folder={folder} onNavigate={setFolder} />}
          {loading && <div className="text-sm text-slate-400">Loading…</div>}
          {error && <div className="text-sm text-red-600">{error.message}</div>}
          {!loading && !error && (!data || data.length === 0) && (
            <EmptyState title="No memories" hint="Memories captured by agents or people during calls and runs show up here." />
          )}
          {!loading && !error && data && data.length > 0 && (
            <BulkMoveBar
              count={selected.size}
              itemLabel="memories"
              folders={folderList}
              onMove={(target, onProgress) => bulkMove(Array.from(selected), target, onProgress)}
              onClear={clearSelection}
              failures={failures}
            />
          )}
          {!loading && !error && data && data.length > 0 && (
            <Table<Memory>
              rowKey={(m) => m.id}
              selectedKeys={selected}
              onSelectionChange={setSelected}
              draggable
              columns={[
                {
                  key: 'id',
                  header: 'ID',
                  render: (m) => (
                    <Link to={`/ui/memories/${encodeURIComponent(m.id)}`} className="font-mono text-xs text-sky-700 underline dark:text-sky-400">
                      {m.id}
                    </Link>
                  ),
                },
                { key: 'type', header: 'Type', render: (m) => m.type },
                { key: 'scope', header: 'Scope', render: (m) => m.scope },
                {
                  key: 'tier',
                  header: 'Tier',
                  render: (m) => (
                    <span className="flex flex-wrap items-center gap-1.5">
                      <ItemTierBadge tier={m.tier} />
                      {m.tier === 'workspace' && (
                        <>
                          <ShippedBadge shipped={m.shipped} />
                          <CommitButton id={m.id} shipped={m.shipped} onCommit={() => memories.commit(m.id)} onCommitted={reload} />
                          {m.shipped === 'unpushed' && <PushButton onPushed={reload} />}
                        </>
                      )}
                      <MoveTierControl tier={m.tier} onMove={(target) => memories.move(m.id, target)} onMoved={reload} />
                    </span>
                  ),
                },
                {
                  key: 'folder',
                  header: 'Folder',
                  render: (m) => (
                    <span className="flex items-center gap-1.5">
                      <span className="text-xs text-slate-500">{m.folder || '(root)'}</span>
                      <FolderMovePopover
                        currentFolder={m.folder}
                        folders={folderList}
                        onMove={(next) => foldersApi.moveMemory(m.id, next)}
                        onMoved={reload}
                      />
                    </span>
                  ),
                },
                {
                  key: 'subject',
                  header: 'Subject',
                  render: (m) => m.subject.operation || m.subject.service || m.subject.flow || m.subject.concept || '-',
                },
                { key: 'text', header: 'First line', render: (m) => <span className="line-clamp-1 max-w-md text-slate-500">{(m.text.split('\n')[0] || '').slice(0, 140)}</span> },
                { key: 'status', header: 'Status', render: (m) => m.status },
                { key: 'updated', header: 'Updated', render: (m) => <Timestamp value={m.updated} /> },
              ]}
              rows={filtered}
            />
          )}
        </div>
      </div>
    </div>
  );
}
