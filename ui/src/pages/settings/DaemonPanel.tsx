// Settings > Daemon (PLAN §34f item 3): what's running, and a Restart
// button that confirms first (with a warning when something would be
// interrupted), waits out the restart with state/daemon.ts's
// waitForRestart, and then refetches this panel.
import { useState } from 'react';
import { daemon as daemonApi } from '../../api/client';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { KeyValue } from '../../components/KeyValue';
import { relative } from '../../components/Timestamp';
import { useAsync } from '../../lib/useAsync';
import { waitForRestart } from '../../state/daemon';
import { pushToast } from '../../state/toast';

const buttonCls = 'rounded border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-50 dark:border-slate-700';

function warningText(activeRuns: number, terminals: number): string | null {
  const parts: string[] = [];
  if (activeRuns > 0) parts.push(`${activeRuns} run${activeRuns === 1 ? '' : 's'} in flight will be cancelled`);
  if (terminals > 0) parts.push(`${terminals} terminal${terminals === 1 ? '' : 's'} will be closed`);
  return parts.length === 0 ? null : parts.join(' and ');
}

export default function DaemonPanel() {
  const { data, error, loading, reload } = useAsync(() => daemonApi.get(), []);
  const [showConfirm, setShowConfirm] = useState(false);
  const [restarting, setRestarting] = useState(false);

  const warning = data ? warningText(data.active_runs, data.terminals) : null;

  const restart = async () => {
    setRestarting(true);
    setShowConfirm(false);
    try {
      await daemonApi.restart(warning ? true : undefined);
      pushToast('info', 'Restarting…');
      const health = await waitForRestart();
      if (health) {
        pushToast('success', 'Daemon restarted');
        reload();
      } else {
        pushToast('error', 'Daemon did not come back within 30s; check it manually.');
      }
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Restart failed.');
    } finally {
      setRestarting(false);
    }
  };

  return (
    <section id="daemon" className="mb-6 rounded border border-slate-200 p-4 dark:border-slate-800">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-sm font-semibold">Daemon</h2>
        <button type="button" disabled={!data || restarting} onClick={() => setShowConfirm(true)} className={buttonCls}>
          {restarting ? 'Restarting…' : 'Restart'}
        </button>
      </div>

      {loading && <p className="text-sm text-slate-400">Loading…</p>}
      {error && <p className="text-sm text-red-600">{error.message}</p>}
      {data && (
        <KeyValue
          pairs={[
            ['version', data.version],
            ['commit', data.commit ? data.commit.slice(0, 7) : '-'],
            ['uptime', relative(new Date(data.started))],
            ['pid', String(data.pid)],
            ['port', String(data.port)],
            ['executable', <span className="break-all font-mono text-xs">{data.executable || '-'}</span>],
            ['install method', data.install_method || '-'],
            ['workspaces open', String(data.workspaces_open)],
            ['active runs', String(data.active_runs)],
            ['terminals', String(data.terminals)],
          ]}
        />
      )}

      {showConfirm && (
        <ConfirmDialog
          title="Restart the daemon?"
          message={
            <>
              <p>This restarts the Sapien daemon on this machine. Your session stays signed in.</p>
              {warning && <p className="mt-2 font-medium text-amber-700 dark:text-amber-400">{warning}.</p>}
            </>
          }
          confirmLabel="Restart"
          danger={!!warning}
          onConfirm={restart}
          onClose={() => setShowConfirm(false)}
        />
      )}
    </section>
  );
}
