// Friction reports: what agents told report_friction was wrong with Sapien
// itself (a tool returned the wrong shape, a capability was missing, docs
// misled it). Reports are queued on disk (internal/friction) and never
// sent anywhere automatically -- a human reviews each one here, previews
// the exact GitHub Discussion `send` would post, and only then posts it.
// This page is the UI's counterpart to `sapien friction list/show/send/drop`.
import { Fragment, useState } from 'react';
import { friction } from '../api/client';
import { EmptyState } from '../components/EmptyState';
import { KeyValue } from '../components/KeyValue';
import { Timestamp } from '../components/Timestamp';
import { useAsync } from '../lib/useAsync';
import { useFrictionCount } from '../state/friction';
import { pushToast } from '../state/toast';
import type { FrictionCategory, FrictionPreview, FrictionReport } from '../api/types';

const categoryColor: Record<FrictionCategory, string> = {
  bug: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300',
  idea: 'bg-violet-100 text-violet-800 dark:bg-violet-900/40 dark:text-violet-300',
  docs: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  missing: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
};

function CategoryPill({ category }: { category: FrictionCategory }) {
  return (
    <span
      className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${
        categoryColor[category] || 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300'
      }`}
    >
      {category}
    </span>
  );
}

function Field({ label, text }: { label: string; text: string }) {
  return (
    <div>
      <h3 className="mb-1 text-xs font-semibold uppercase text-slate-500">{label}</h3>
      <p className="whitespace-pre-wrap text-sm text-slate-700 dark:text-slate-300">{text}</p>
    </div>
  );
}

function PreviewBlock({ preview }: { preview: FrictionPreview }) {
  return (
    <div>
      <h3 className="mb-1 text-xs font-semibold uppercase text-slate-500">Preview</h3>
      <div className="mb-2 text-sm">
        <span className="font-medium">{preview.title}</span>
        <span className="ml-2 text-xs text-slate-500">
          → {preview.repo} ({preview.category})
        </span>
      </div>
      <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded border border-slate-200 bg-white p-2 font-mono text-xs leading-5 dark:border-slate-800 dark:bg-slate-950">
        {preview.body}
      </pre>
    </div>
  );
}

function ReportDetail({ report, preview, previewError }: { report: FrictionReport; preview?: FrictionPreview; previewError?: string }) {
  const meta: Array<[string, string]> = [];
  if (report.tool) meta.push(['tool', report.tool]);
  if (report.client) meta.push(['client', report.client]);
  if (report.workspace) meta.push(['workspace', report.workspace]);
  if (report.version) meta.push(['version', report.version]);

  return (
    <div className="space-y-3 p-3">
      {meta.length > 0 && <KeyValue pairs={meta} />}
      {report.tried && <Field label="Tried" text={report.tried} />}
      <Field label="Happened" text={report.happened} />
      {report.would_help && <Field label="Would help" text={report.would_help} />}
      {previewError && <div className="text-sm text-red-600">{previewError}</div>}
      {preview && <PreviewBlock preview={preview} />}
    </div>
  );
}

type StatusKey = 'pending' | 'sent';

export default function FrictionPage() {
  const { data, error, loading, reload } = useAsync(() => friction.list(), []);

  const [statusOn, setStatusOn] = useState<Record<StatusKey, boolean>>({ pending: true, sent: true });
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [previews, setPreviews] = useState<Record<string, FrictionPreview>>({});
  const [previewedIds, setPreviewedIds] = useState<Set<string>>(new Set());
  const [previewLoadingId, setPreviewLoadingId] = useState<string | null>(null);
  const [previewErrors, setPreviewErrors] = useState<Record<string, string>>({});
  const [sendingId, setSendingId] = useState<string | null>(null);
  const [droppingId, setDroppingId] = useState<string | null>(null);

  const toggleStatus = (s: StatusKey) => setStatusOn((prev) => ({ ...prev, [s]: !prev[s] }));

  const filtered = (data || []).filter((r) => statusOn[r.status]);

  const refreshBadge = () => useFrictionCount.getState().refresh();

  const handlePreview = async (id: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setPreviewLoadingId(id);
    setPreviewErrors((s) => ({ ...s, [id]: '' }));
    try {
      const p = await friction.preview(id);
      setPreviews((s) => ({ ...s, [id]: p }));
      setPreviewedIds((s) => new Set(s).add(id));
    } catch (err) {
      setPreviewErrors((s) => ({ ...s, [id]: err instanceof Error ? err.message : String(err) }));
    } finally {
      setPreviewLoadingId((cur) => (cur === id ? null : cur));
    }
  };

  const handleSend = async (id: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setSendingId(id);
    try {
      const sent = await friction.send(id);
      pushToast('success', sent.sent_url ? `Posted as a GitHub Discussion: ${sent.sent_url}` : 'Posted as a GitHub Discussion.');
      reload();
      refreshBadge();
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : String(err));
    } finally {
      setSendingId((cur) => (cur === id ? null : cur));
    }
  };

  const handleDrop = async (id: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setDroppingId(id);
    try {
      await friction.drop(id);
      pushToast('success', 'Report dropped.');
      reload();
      refreshBadge();
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : String(err));
    } finally {
      setDroppingId((cur) => (cur === id ? null : cur));
    }
  };

  return (
    <div className="p-4">
      <h1 className="mb-1 text-lg font-semibold">Friction</h1>
      <p className="mb-4 text-sm text-slate-500">
        Reports agents filed about Sapien itself. Nothing here has been sent; review a report, then post it as a GitHub Discussion.
      </p>

      {data && data.length > 0 && (
        <div className="mb-3 flex items-center gap-1" role="group" aria-label="Status">
          {(['pending', 'sent'] as StatusKey[]).map((s) => {
            const on = statusOn[s];
            return (
              <button
                key={s}
                type="button"
                aria-pressed={on}
                onClick={() => toggleStatus(s)}
                className={`rounded-full border px-2 py-0.5 text-xs ${
                  on
                    ? 'border-sky-600 bg-sky-50 text-sky-800 dark:border-sky-500 dark:bg-sky-950 dark:text-sky-300'
                    : 'border-slate-300 text-slate-400 dark:border-slate-700 dark:text-slate-500'
                }`}
              >
                {s === 'pending' ? 'Pending' : 'Sent'}
              </button>
            );
          })}
        </div>
      )}

      {loading && <div className="text-sm text-slate-400">Loading…</div>}
      {error && <div className="text-sm text-red-600">{error.message}</div>}
      {!loading && !error && (!data || data.length === 0) && (
        <EmptyState
          title="No friction reports"
          hint="Agents file them with the report_friction MCP tool; you can add one yourself with `sapien friction add`."
        />
      )}
      {!loading && !error && data && data.length > 0 && filtered.length === 0 && (
        <div className="text-sm text-slate-400">No friction reports match the current filter.</div>
      )}
      {!loading && !error && filtered.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-slate-200 text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800">
                <th className="whitespace-nowrap px-3 py-2 font-medium">Title</th>
                <th className="whitespace-nowrap px-3 py-2 font-medium">Category</th>
                <th className="whitespace-nowrap px-3 py-2 font-medium">Filed</th>
                <th className="whitespace-nowrap px-3 py-2 font-medium">Status</th>
                <th className="whitespace-nowrap px-3 py-2 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((r) => {
                const expanded = expandedId === r.id;
                const pending = r.status === 'pending';
                return (
                  <Fragment key={r.id}>
                    <tr
                      onClick={() => setExpandedId(expanded ? null : r.id)}
                      className="cursor-pointer border-b border-slate-100 hover:bg-slate-50 dark:border-slate-900 dark:hover:bg-slate-900"
                    >
                      <td className="px-3 py-2">{r.title}</td>
                      <td className="whitespace-nowrap px-3 py-2">
                        <CategoryPill category={r.category} />
                      </td>
                      <td className="whitespace-nowrap px-3 py-2">
                        <Timestamp value={r.created} />
                      </td>
                      <td className="whitespace-nowrap px-3 py-2">
                        {r.status === 'sent' ? (
                          <a
                            href={r.sent_url}
                            target="_blank"
                            rel="noreferrer"
                            onClick={(e) => e.stopPropagation()}
                            className="text-sky-700 underline dark:text-sky-400"
                          >
                            sent
                          </a>
                        ) : (
                          'pending'
                        )}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2">
                        <div className="flex gap-2">
                          {pending && (
                            <>
                              <button
                                type="button"
                                onClick={(e) => handlePreview(r.id, e)}
                                disabled={previewLoadingId === r.id}
                                className="rounded border border-slate-300 px-2 py-1 text-xs disabled:opacity-50 dark:border-slate-700"
                              >
                                {previewLoadingId === r.id ? 'Previewing…' : 'Preview'}
                              </button>
                              <button
                                type="button"
                                onClick={(e) => handleSend(r.id, e)}
                                disabled={!previewedIds.has(r.id) || sendingId === r.id}
                                title={previewedIds.has(r.id) ? undefined : 'Preview the report before posting it'}
                                className="rounded border border-emerald-300 px-2 py-1 text-xs text-emerald-700 disabled:opacity-40 dark:border-emerald-900 dark:text-emerald-400"
                              >
                                {sendingId === r.id ? 'Posting…' : 'Post as Discussion'}
                              </button>
                            </>
                          )}
                          <button
                            type="button"
                            onClick={(e) => handleDrop(r.id, e)}
                            disabled={droppingId === r.id}
                            className="rounded border border-red-300 px-2 py-1 text-xs text-red-700 disabled:opacity-40 dark:border-red-900 dark:text-red-400"
                          >
                            {droppingId === r.id ? 'Dropping…' : 'Drop'}
                          </button>
                        </div>
                      </td>
                    </tr>
                    {expanded && (
                      <tr className="border-b border-slate-100 bg-slate-50 dark:border-slate-900 dark:bg-slate-900/40">
                        <td colSpan={5}>
                          <ReportDetail report={r} preview={previews[r.id]} previewError={previewErrors[r.id]} />
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
