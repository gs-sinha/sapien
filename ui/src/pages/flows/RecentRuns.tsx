import { useEffect } from 'react';
import { Link } from 'react-router-dom';
import { runs } from '../../api/client';
import { StatusPill } from '../../components/StatusPill';
import { Timestamp } from '../../components/Timestamp';
import { useAsync } from '../../lib/useAsync';
import { subscribe } from '../../state/events';

export function RecentRuns({ flowId }: { flowId: string }) {
  const { data, error, loading, reload } = useAsync(() => runs.list({ flow: flowId, limit: 20 }), [flowId]);

  useEffect(() => {
    const matches = (id?: string) => id === flowId;
    const un1 = subscribe('run.started', (e) => matches(e.ids.flow_id) && reload());
    const un2 = subscribe('run.finished', (e) => matches(e.ids.flow_id) && reload());
    return () => {
      un1();
      un2();
    };
  }, [flowId, reload]);

  if (loading) return <div className="text-xs text-slate-400">Loading…</div>;
  if (error) return <div className="text-xs text-red-600">{error.message}</div>;
  if (!data || data.length === 0) return <div className="text-xs text-slate-400">No runs yet.</div>;

  return (
    <div className="divide-y divide-slate-100 rounded border border-slate-200 text-sm dark:divide-slate-900 dark:border-slate-800">
      {data.map((r) => (
        <Link
          key={r.id}
          to={`/ui/runs/${encodeURIComponent(r.id)}`}
          className="flex items-center gap-3 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-900"
        >
          <StatusPill status={r.status} />
          <span className="font-mono text-xs text-slate-500">{r.environment}</span>
          <span className="ml-auto text-xs text-slate-400">
            <Timestamp value={r.started} />
          </span>
          <span className="text-xs text-slate-400">
            {r.summary.steps_passed}/{r.summary.steps_total}
          </span>
        </Link>
      ))}
    </div>
  );
}
