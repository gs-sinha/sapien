import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { examples, folders as foldersApi } from '../api/client';
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
import { pushToast } from '../state/toast';
import type { ExampleQueryParams, SavedExample } from '../api/types';

function FilterInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <input
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className="w-40 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
    />
  );
}

export default function ExamplesPage() {
  const [service, setService] = useState('');
  const [operation, setOperation] = useState('');
  const [tag, setTag] = useState('');
  const [text, setText] = useState('');
  const [folder, setFolder] = useFolderParam();

  const filter: ExampleQueryParams = { service: service || undefined, operation: operation || undefined, tag: tag || undefined, text: text || undefined };
  const { data, error, loading, reload } = useAsync(() => examples.list(filter), [service, operation, tag, text]);

  // No live event to refetch on (example writes don't emit an event type of
  // their own), so a window focus refetch keeps the list fresh after
  // saving an example from Try It in the same tab/another tab.
  useEffect(() => {
    window.addEventListener('focus', reload);
    return () => window.removeEventListener('focus', reload);
  }, [reload]);

  const hasFilters = !!(service || operation || tag || text);
  const folderList = useMemo(() => distinctFolders(data || []), [data]);
  const filtered = useMemo(() => underFolder(data || [], folder), [data, folder]);

  const { selected, setSelected, failures, clearSelection, bulkMove } = useBulkMove(
    (id, target) => foldersApi.moveExample(id, target),
    reload,
  );

  // Selection is a view-level concern layered on top of whatever the
  // filters currently show; changing any of them invalidates it rather
  // than leaving a stale selection the user can no longer see.
  useEffect(() => {
    clearSelection();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [folder, service, operation, tag, text]);

  const handleDropIds = async (ids: string[], targetFolder: string) => {
    setSelected(new Set(ids));
    const failed = await bulkMove(ids, targetFolder);
    const dest = targetFolder || 'root';
    if (failed.length === 0) pushToast('success', `moved ${ids.length} examples to ${dest}`);
    else if (failed.length < ids.length) pushToast('error', `moved ${ids.length - failed.length}/${ids.length} examples to ${dest}; ${failed.length} failed`);
    else pushToast('error', `failed to move examples to ${dest}`);
  };

  return (
    <div className="p-4">
      <h1 className="mb-4 text-lg font-semibold">Examples</h1>
      <div className="mb-3 flex flex-wrap gap-2">
        <FilterInput value={service} onChange={setService} placeholder="service" />
        <FilterInput value={operation} onChange={setOperation} placeholder="operation" />
        <FilterInput value={tag} onChange={setTag} placeholder="tag" />
        <FilterInput value={text} onChange={setText} placeholder="search text" />
      </div>

      <div className="flex flex-col gap-4 md:flex-row">
        <FolderSidebar items={data || []} selected={folder} onSelect={setFolder} treeKey="folders:examples" onDropIds={handleDropIds} />
        <div className="min-w-0 flex-1">
          {folderList.length > 0 && <FolderBreadcrumb folder={folder} onNavigate={setFolder} />}
          {loading && <div className="text-sm text-slate-400">Loading…</div>}
          {error && <div className="text-sm text-red-600">{error.message}</div>}
          {!loading && !error && (!data || data.length === 0) && (
            <EmptyState
              title={hasFilters ? 'No examples match these filters' : 'No saved examples yet'}
              hint={hasFilters ? undefined : 'Save a working request from Try It (or `sapien call --save-example`) to build a library here.'}
            />
          )}
          {!loading && !error && data && data.length > 0 && (
            <BulkMoveBar
              count={selected.size}
              itemLabel="examples"
              folders={folderList}
              onMove={(target, onProgress) => bulkMove(Array.from(selected), target, onProgress)}
              onClear={clearSelection}
              failures={failures}
            />
          )}
          {!loading && !error && data && data.length > 0 && (
            <Table<SavedExample>
              rowKey={(e) => e.id}
              selectedKeys={selected}
              onSelectionChange={setSelected}
              draggable
              columns={[
                {
                  key: 'id',
                  header: 'ID',
                  render: (e) => (
                    <Link to={`/ui/examples/${encodeURIComponent(e.id)}`} className="text-sky-700 underline dark:text-sky-400">
                      {e.id}
                    </Link>
                  ),
                },
                { key: 'operation', header: 'Operation', render: (e) => <span className="font-mono text-xs">{e.operation}</span> },
                { key: 'scope', header: 'Scope', render: (e) => e.scope },
                {
                  key: 'tier',
                  header: 'Tier',
                  render: (e) => (
                    <span className="flex flex-wrap items-center gap-1.5">
                      <ItemTierBadge tier={e.tier} />
                      {e.tier === 'workspace' && (
                        <>
                          <ShippedBadge shipped={e.shipped} />
                          <CommitButton id={e.id} shipped={e.shipped} onCommit={() => examples.commit(e.id)} onCommitted={reload} />
                          {e.shipped === 'unpushed' && <PushButton onPushed={reload} />}
                        </>
                      )}
                      <MoveTierControl tier={e.tier} onMove={(target) => examples.move(e.id, target)} onMoved={reload} />
                    </span>
                  ),
                },
                {
                  key: 'folder',
                  header: 'Folder',
                  render: (e) => (
                    <span className="flex items-center gap-1.5">
                      <span className="text-xs text-slate-500">{e.folder || '(root)'}</span>
                      <FolderMovePopover
                        currentFolder={e.folder}
                        folders={folderList}
                        onMove={(next) => foldersApi.moveExample(e.id, next)}
                        onMoved={reload}
                      />
                    </span>
                  ),
                },
                { key: 'verified', header: 'Verified env', render: (e) => (e.verified ? e.verified.env || 'yes' : 'draft') },
                { key: 'description', header: 'Description', render: (e) => e.description || '-' },
                { key: 'updated', header: 'Updated', render: (e) => <Timestamp value={e.updated} /> },
              ]}
              rows={filtered}
            />
          )}
        </div>
      </div>
    </div>
  );
}
