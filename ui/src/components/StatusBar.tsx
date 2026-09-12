import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { getWorkspace } from '../api/client';
import { useDaemon } from '../state/daemon';
import { useEvents } from '../state/events';
import { useTheme } from '../state/theme';

function Dot({ ok }: { ok: boolean }) {
  return <span className={`inline-block h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-500' : 'bg-slate-400'}`} />;
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
