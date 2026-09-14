import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { getWorkspace, repo as repoApi } from '../api/client';
import { PushButton } from './tiers';
import { relative } from './Timestamp';
import { useDaemon } from '../state/daemon';
import { useEvents } from '../state/events';
import { useRepo } from '../state/repo';
import { useTheme } from '../state/theme';
import { pushToast } from '../state/toast';

function Dot({ ok }: { ok: boolean }) {
  return <span className={`inline-block h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-500' : 'bg-slate-400'}`} />;
}

// The workspace folder is itself the team's git repository: the daemon
// fetches it on a ten-minute tick (read-only) and this segment says what
// that found, with a Pull button when it's safe to fast-forward. Nothing
// renders while the workspace isn't a git checkout at all.
function RepoSegment() {
  const status = useRepo((s) => s.status);
  const [busy, setBusy] = useState(false);
  if (!status?.in_git) return null;

  const parts: ReactNode[] = [`team · ${status.branch || 'repo'}`];
  let flagged = false;
  if (status.behind > 0) {
    parts.push(`↓${status.behind} new`);
    flagged = true;
  }
  if (status.ahead > 0) {
    parts.push(`↑${status.ahead} unpushed`);
    flagged = true;
  }
  if (status.dirty > 0) {
    parts.push(`${status.dirty} uncommitted`);
    flagged = true;
  }
  if (status.fetch_error) {
    parts.push(
      <span key="fetch-failed" title={status.fetch_error}>
        fetch failed
      </span>,
    );
    flagged = true;
  }
  if (!flagged && status.fetched_at) {
    const d = new Date(status.fetched_at);
    if (!Number.isNaN(d.getTime())) parts.push(`fetched ${relative(d)}`);
  }

  const pull = async () => {
    setBusy(true);
    try {
      const next = await repoApi.pull();
      useRepo.getState().setStatus(next);
      pushToast('success', `pulled ${next.pulled_count ?? 0} commits`);
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Pull failed.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <span className="flex items-center gap-1">
      {parts.map((p, i) => (
        <span key={i}>
          {i > 0 ? ' · ' : ''}
          {p}
        </span>
      ))}
      {status.behind > 0 && status.dirty === 0 && (
        <button
          type="button"
          disabled={busy}
          onClick={pull}
          className="ml-1 rounded border border-slate-300 px-1.5 py-0.5 disabled:opacity-50 dark:border-slate-700"
        >
          {busy ? 'Pulling…' : 'Pull'}
        </button>
      )}
      {status.behind > 0 && status.dirty > 0 && (
        <span className="ml-1 text-amber-600 dark:text-amber-400">pull blocked: {status.dirty} uncommitted</span>
      )}
      {status.ahead > 0 && status.behind === 0 && (
        <span className="ml-1">
          <PushButton />
        </span>
      )}
      {status.ahead > 0 && status.behind > 0 && (
        <span className="ml-1 text-amber-600 dark:text-amber-400">pull first</span>
      )}
    </span>
  );
}

// Daemon reachability is checked once on mount (GET /v1/workspace), not
// polled: the event stream's own WebSocket status is the live signal after
// that, per "no polling anywhere; the WebSocket pushes." state/daemon.ts
// re-checks only when reconnecting has already failed, and its verdict
// wins here once it has one.
export function StatusBar() {
  const [workspaceName, setWorkspaceName] = useState<string | null>(null);
  const [initialCheck, setInitialCheck] = useState<'checking' | 'ok' | 'unreachable'>('checking');
  // state/daemon.ts learns later than this component's one-shot check --
  // when reconnecting fails -- so its answer supersedes it. Without this
  // the dot stayed green beside a banner saying the daemon was gone.
  const probed = useDaemon((s) => s.state);
  const daemonState: 'checking' | 'ok' | 'unreachable' =
    probed === 'gone' ? 'unreachable' : probed === 'ok' || probed === 'replaced' ? 'ok' : initialCheck;
  const status = useEvents((s) => s.status);
  const unreadCount = useEvents((s) => s.unreadCount);
  const markRead = useEvents((s) => s.markRead);
  const theme = useTheme((s) => s.theme);
  const toggleTheme = useTheme((s) => s.toggle);

  useEffect(() => {
    let cancelled = false;
    getWorkspace()
      .then((ws) => {
        if (cancelled) return;
        setWorkspaceName(ws.name);
        setInitialCheck('ok');
      })
      .catch(() => {
        if (!cancelled) setInitialCheck('unreachable');
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="flex h-9 items-center justify-between border-b border-slate-200 px-3 text-xs text-slate-500 dark:border-slate-800">
      <div className="flex items-center gap-4">
        <span className="font-medium text-slate-700 dark:text-slate-300">
          {workspaceName ?? (daemonState === 'unreachable' ? 'daemon unreachable' : 'connecting…')}
        </span>
        <span className="flex items-center gap-1">
          <Dot ok={daemonState === 'ok'} /> daemon
        </span>
        <RepoSegment />
        <span className="flex items-center gap-1">
          <Dot ok={status === 'open'} /> events: {status}
        </span>
        <Link to="/ui/events" onClick={markRead} className="hover:underline">
          {unreadCount > 0 ? `${unreadCount} new event${unreadCount === 1 ? '' : 's'}` : 'no new events'}
        </Link>
      </div>
      <button
        type="button"
        onClick={toggleTheme}
        className="rounded border border-slate-300 px-2 py-0.5 dark:border-slate-700"
      >
        {theme === 'dark' ? 'Light' : 'Dark'}
      </button>
    </div>
  );
}
