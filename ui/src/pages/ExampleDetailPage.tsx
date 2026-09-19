import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { examples, folders as foldersApi } from '../api/client';
import { FolderMovePopover } from '../components/FolderMovePopover';
import { JsonView } from '../components/JsonView';
import { KeyValue } from '../components/KeyValue';
import { Timestamp } from '../components/Timestamp';
import { CommitButton, ItemTierBadge, MoveTierControl, PushButton, ShippedBadge } from '../components/tiers';
import { YamlView } from '../components/YamlView';
import { distinctFolders } from '../lib/folders';
import { useAsync } from '../lib/useAsync';
import { dumpYaml } from '../lib/yaml';
import { pushToast } from '../state/toast';
import type { ExampleScope, SavedExample } from '../api/types';

// Examples, like flows, don't carry their exact file text over the wire
// (SavedExample.Path is a location, not the bytes); this reconstructs a
// read-only YAML view from the parsed fields, the same approach
// FlowDetailPage uses (see ui/README.md "Known gaps").
function toYamlShape(ex: SavedExample) {
  return {
    version: ex.version,
    id: ex.id,
    operation: ex.operation,
    description: ex.description,
    input: ex.input,
    body: ex.body,
    headers: ex.headers,
    expect: ex.expect,
    tags: ex.tags,
  };
}

export default function ExampleDetailPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const { data: ex, error, loading, reload } = useAsync(() => examples.get(id), [id]);

  const [scopeDraft, setScopeDraft] = useState<ExampleScope>('workspace');
  const [rescoping, setRescoping] = useState(false);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    if (ex) setScopeDraft(ex.scope);
  }, [ex]);

  if (loading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="p-4 text-sm text-red-600">{error.message}</div>;
  if (!ex) return null;

  const rescope = async () => {
    if (scopeDraft === ex.scope) return;
    setRescoping(true);
    try {
      await examples.update(ex.id, { ...ex, scope: scopeDraft });
      pushToast('success', `Rescoped to ${scopeDraft}`);
      reload();
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : String(err));
      setScopeDraft(ex.scope);
    } finally {
      setRescoping(false);
    }
  };

  const remove = async () => {
    if (!window.confirm(`Delete example "${ex.id}"? This cannot be undone.`)) return;
    setDeleting(true);
    try {
      await examples.delete(ex.id);
      pushToast('success', `Deleted example "${ex.id}"`);
      navigate('/ui/examples');
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : String(err));
      setDeleting(false);
    }
  };

  return (
    <div className="p-4">
      <div className="mb-1 flex flex-wrap items-center gap-2">
        <h1 className="text-lg font-semibold">{ex.id}</h1>
        <ItemTierBadge tier={ex.tier} />
        {ex.tier === 'workspace' && (
          <>
            <ShippedBadge shipped={ex.shipped} />
            <CommitButton id={ex.id} shipped={ex.shipped} onCommit={() => examples.commit(ex.id)} onCommitted={reload} />
            {ex.shipped === 'unpushed' && <PushButton onPushed={reload} />}
          </>
        )}
        <Link
          to={`/ui/try/${encodeURIComponent(ex.operation)}?example=${encodeURIComponent(ex.id)}`}
          className="rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700"
        >
          Try it
        </Link>
        <select
          value={scopeDraft}
          onChange={(e) => setScopeDraft(e.target.value as ExampleScope)}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
        >
          <option value="workspace">workspace</option>
          <option value="service">service</option>
        </select>
        <button
          type="button"
          onClick={rescope}
          disabled={rescoping || scopeDraft === ex.scope}
          className="rounded border border-slate-300 px-2 py-1 text-xs disabled:opacity-40 dark:border-slate-700"
        >
          {rescoping ? 'Rescoping…' : 'Rescope'}
        </button>
        <button
          type="button"
          onClick={remove}
          disabled={deleting}
          className="rounded border border-red-300 px-2 py-1 text-xs text-red-700 disabled:opacity-40 dark:border-red-900 dark:text-red-400"
        >
          {deleting ? 'Deleting…' : 'Delete'}
        </button>
        <MoveTierControl tier={ex.tier} onMove={(target) => examples.move(ex.id, target)} onMoved={reload} />
        <span className="text-xs text-slate-500">{ex.folder || '(root)'}</span>
        <FolderMovePopover
          currentFolder={ex.folder}
          loadFolders={() => examples.list().then(distinctFolders)}
          onMove={(next) => foldersApi.moveExample(ex.id, next)}
          onMoved={reload}
        />
      </div>
      <p className="mb-3 text-xs text-slate-400">
        Scope says which catalog the example is filed under; tier says where its file is: local until you move it to the team.
      </p>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div>
          <p className="mb-3 text-sm text-slate-500">{ex.description}</p>
          <KeyValue
            pairs={[
              ['operation', ex.operation],
              ['scope', ex.scope],
              ['service', ex.service],
              ['tags', (ex.tags || []).join(', ') || '-'],
              ['created', <Timestamp value={ex.created} />],
              ['updated', <Timestamp value={ex.updated} />],
              ['verified', ex.verified ? `${ex.verified.env || ''} run ${ex.verified.run || ''}` : 'no'],
              ['path', ex.path || '-'],
            ]}
          />
          <h2 className="mb-1 mt-4 text-sm font-semibold">File</h2>
          <YamlView source={dumpYaml(toYamlShape(ex))} />
        </div>
        <div className="space-y-4">
          <div>
            <h2 className="mb-1 text-sm font-semibold">Input</h2>
            <JsonView data={ex.input || {}} />
          </div>
          <div>
            <h2 className="mb-1 text-sm font-semibold">Body</h2>
            <JsonView data={ex.body ?? null} />
          </div>
          {ex.headers && Object.keys(ex.headers).length > 0 && (
            <div>
              <h2 className="mb-1 text-sm font-semibold">Headers</h2>
              <JsonView data={ex.headers} />
            </div>
          )}
          {ex.expect && (
            <div>
              <h2 className="mb-1 text-sm font-semibold">Expected response (observed when saved)</h2>
              <JsonView data={ex.expect} />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
