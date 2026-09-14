// Memory detail: full text, subject/source/tags/status, and the actions a
// person doing memory hygiene needs (rescope, promote/deprecate, and "where
// does this belong" against the catalog/docs). Not yet reachable from
// App.tsx -- see the build report for why (route-edit budget).
import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { memories } from '../api/client';
import { KeyValue } from '../components/KeyValue';
import { Timestamp } from '../components/Timestamp';
import { CommitButton, ItemTierBadge, MoveTierControl, PushButton, ShippedBadge } from '../components/tiers';
import { useAsync } from '../lib/useAsync';
import { MarkdownLite } from './memories/MarkdownLite';
import { pushToast } from '../state/toast';
import type { Memory, MemoryScope, MemoryStatus, PromotionTarget } from '../api/types';

function subjectPairs(m: Memory): Array<[string, string]> {
  const s = m.subject;
  const pairs: Array<[string, string]> = [];
  if (s.service) pairs.push(['service', s.service]);
  if (s.operation) pairs.push(['operation', s.operation]);
  if (s.field) pairs.push(['field', s.field]);
  if (s.schema) pairs.push(['schema', s.schema]);
  if (s.flow) pairs.push(['flow', s.flow]);
  if (s.step) pairs.push(['step', s.step]);
  if (s.run) pairs.push(['run', s.run]);
  if (s.environment) pairs.push(['environment', s.environment]);
  if (s.concept) pairs.push(['concept', s.concept]);
  if (s.error) pairs.push(['error', `${s.error.operation || ''} ${s.error.status || ''} ${s.error.code || ''}`.trim()]);
  return pairs;
}

export default function MemoryDetailPage() {
  const { id = '' } = useParams();
  const { data: mem, error, loading, reload } = useAsync(() => memories.get(id), [id]);

  const [scopeDraft, setScopeDraft] = useState<MemoryScope>('workspace');
  const [busy, setBusy] = useState(false);
  const [promotion, setPromotion] = useState<PromotionTarget | null>(null);
  const [promotionLoading, setPromotionLoading] = useState(false);
  const [promotionError, setPromotionError] = useState<string | null>(null);

  useEffect(() => {
    if (mem) setScopeDraft(mem.scope);
  }, [mem]);

  if (loading) return <div className="p-4 text-sm text-slate-400">Loading…</div>;
  if (error) return <div className="p-4 text-sm text-red-600">{error.message}</div>;
  if (!mem) return null;

  const rescope = async (next: MemoryScope) => {
    setBusy(true);
    try {
      await memories.patch(mem.id, { ...mem, scope: next });
      pushToast('success', `Rescoped to ${next}`);
      reload();
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const setStatus = async (status: MemoryStatus) => {
    setBusy(true);
    try {
      await memories.patch(mem.id, { ...mem, status });
      pushToast('success', `Marked ${status}`);
      reload();
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const findPromotion = async () => {
    setPromotionLoading(true);
    setPromotionError(null);
    try {
      const target = await memories.promotion(mem.id);
      setPromotion(target);
    } catch (err) {
      setPromotionError(err instanceof Error ? err.message : String(err));
    } finally {
      setPromotionLoading(false);
    }
  };

  return (
    <div className="p-4">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h1 className="font-mono text-lg font-semibold">{mem.id}</h1>
        <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-300">{mem.type}</span>
        <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-300">{mem.status}</span>
        <ItemTierBadge tier={mem.tier} />
        {mem.tier === 'workspace' && (
          <>
            <ShippedBadge shipped={mem.shipped} />
            <CommitButton id={mem.id} shipped={mem.shipped} onCommit={() => memories.commit(mem.id)} onCommitted={reload} />
            {mem.shipped === 'unpushed' && <PushButton onPushed={reload} />}
          </>
        )}
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div>
          <KeyValue
            pairs={[
              ['scope', mem.scope],
              ['status', mem.status],
              ['source', `${mem.source.kind}${mem.source.client ? ` (${mem.source.client})` : ''}`],
              ['tags', (mem.tags || []).join(', ') || '-'],
              ['created', <Timestamp value={mem.created} />],
              ['updated', <Timestamp value={mem.updated} />],
              ['file', mem.file_path || '(personal, SQLite only)'],
            ]}
          />

          <h2 className="mb-1 mt-4 text-xs font-semibold uppercase text-slate-500">Subject</h2>
          {subjectPairs(mem).length === 0 ? (
            <div className="text-sm text-slate-400">No subject set.</div>
          ) : (
            <KeyValue pairs={subjectPairs(mem)} />
          )}

          <h2 className="mb-1 mt-4 text-xs font-semibold uppercase text-slate-500">Actions</h2>
          <div className="flex flex-wrap items-center gap-2">
            <select
              value={scopeDraft}
              onChange={(e) => setScopeDraft(e.target.value as MemoryScope)}
              className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
            >
              <option value="personal">personal (this machine)</option>
              <option value="workspace">workspace (team repo)</option>
              <option value="service">service (owning repo)</option>
              <option value="flow">flow</option>
            </select>
            <button
              type="button"
              disabled={busy || scopeDraft === mem.scope}
              onClick={() => rescope(scopeDraft)}
              className="rounded border border-slate-300 px-2 py-1 text-xs disabled:opacity-40 dark:border-slate-700"
            >
              Rescope
            </button>
            <button
              type="button"
              disabled={busy || mem.status === 'promoted'}
              onClick={() => setStatus('promoted')}
              className="rounded border border-emerald-300 px-2 py-1 text-xs text-emerald-700 disabled:opacity-40 dark:border-emerald-900 dark:text-emerald-400"
            >
              Mark promoted
            </button>
            <button
              type="button"
              disabled={busy || mem.status === 'deprecated'}
              onClick={() => setStatus('deprecated')}
              className="rounded border border-amber-300 px-2 py-1 text-xs text-amber-700 disabled:opacity-40 dark:border-amber-900 dark:text-amber-400"
            >
              Deprecate
            </button>
            <button
              type="button"
              disabled={promotionLoading}
              onClick={findPromotion}
              className="rounded border border-slate-300 px-2 py-1 text-xs disabled:opacity-40 dark:border-slate-700"
            >
              {promotionLoading ? 'Looking…' : 'Where does this belong'}
            </button>
            <MoveTierControl tier={mem.tier} onMove={(target) => memories.move(mem.id, target)} onMoved={reload} />
          </div>
          <p className="mt-1 text-xs text-slate-400">
            Scope says who the memory is about; tier says where its file is: local until you move it to the team.
          </p>

          {promotionError && <div className="mt-2 text-xs text-red-600">{promotionError}</div>}
          {promotion && (
            <div className="mt-2 rounded border border-slate-200 p-2 text-xs dark:border-slate-800">
              <div>
                <span className="text-slate-500">kind: </span>
                {promotion.kind}
              </div>
              <div>
                <span className="text-slate-500">file: </span>
                <span className="font-mono">{promotion.file}</span>
                {promotion.line ? `:${promotion.line}` : ''}
              </div>
              {promotion.section && (
                <div>
                  <span className="text-slate-500">section: </span>
                  {promotion.section}
                </div>
              )}
              {promotion.current && (
                <div className="mt-1">
                  <div className="text-slate-500">current text</div>
                  <pre className="mt-0.5 whitespace-pre-wrap rounded bg-slate-50 p-1.5 dark:bg-slate-900">{promotion.current}</pre>
                </div>
              )}
              {promotion.suggested && (
                <div className="mt-1">
                  <div className="text-slate-500">suggested</div>
                  <pre className="mt-0.5 whitespace-pre-wrap rounded bg-slate-50 p-1.5 dark:bg-slate-900">{promotion.suggested}</pre>
                </div>
              )}
            </div>
          )}
        </div>

        <div>
          <h2 className="mb-1 text-xs font-semibold uppercase text-slate-500">Text</h2>
          <div className="rounded border border-slate-200 p-3 dark:border-slate-800">
            <MarkdownLite text={mem.text} />
          </div>
          {mem.resolved?.unresolved && (
            <div className="mt-2 text-xs text-amber-700 dark:text-amber-400">
              This memory's subject could not be resolved against the current catalog.
            </div>
          )}
        </div>
      </div>

      <div className="mt-4">
        <Link to="/ui/memories" className="text-xs text-sky-700 underline dark:text-sky-400">
          Back to memories
        </Link>
      </div>
    </div>
  );
}
