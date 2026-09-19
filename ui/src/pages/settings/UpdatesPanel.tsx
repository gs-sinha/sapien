// Settings > Updates (PLAN §34f item 4). The daemon itself checks the
// latest release at most once a day; this panel shows what it found, an
// on-demand check, and -- when a self-upgrade build is running it -- an
// Upgrade action that waits out the same restart sequence as the Daemon
// panel's Restart and then reloads the page once the successor answers, so
// the reload feels like part of the upgrade rather than a leftover banner
// to notice later.
import { useState } from 'react';
import { updates } from '../../api/client';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { KeyValue } from '../../components/KeyValue';
import { relative } from '../../components/Timestamp';
import { useAsync } from '../../lib/useAsync';
import { waitForRestart } from '../../state/daemon';
import { pushToast } from '../../state/toast';

const buttonCls = 'rounded border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-50 dark:border-slate-700';

export default function UpdatesPanel() {
  const { data, error, loading, reload } = useAsync(() => updates.get(), []);
  const [checking, setChecking] = useState(false);
  const [applying, setApplying] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [copied, setCopied] = useState(false);

  const checkNow = async () => {
    setChecking(true);
    try {
      await updates.check();
      pushToast('success', 'Checked for updates.');
      reload();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Check failed.');
    } finally {
      setChecking(false);
    }
  };

  const toggleDaily = async (check: boolean) => {
    try {
      await updates.setCheckEnabled(check);
      reload();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Could not change the setting.');
    }
  };

  const upgrade = async () => {
    setShowConfirm(false);
    setApplying(true);
    try {
      await updates.apply();
      pushToast('info', 'Upgrading…');
      const health = await waitForRestart();
      if (health) {
        // The successor answered: reload now, intentionally, rather than
        // leaving the existing ReplacedBanner to catch it on its own.
        window.location.reload();
      } else {
        pushToast('error', 'Upgrade did not finish within 30s; check it manually.');
        setApplying(false);
      }
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Upgrade failed.');
      setApplying(false);
    }
  };

  const copyCommand = async (command: string) => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // No clipboard access (permissions, non-secure context, jsdom): the
      // command is still selectable text in the code box.
    }
  };

  return (
    <section id="updates" className="mb-6 rounded border border-slate-200 p-4 dark:border-slate-800">
      <h2 className="mb-2 text-sm font-semibold">Updates</h2>

      {loading && <p className="text-sm text-slate-400">Loading…</p>}
      {error && <p className="text-sm text-red-600">{error.message}</p>}

      {data && (
        <div className="space-y-3">
          <KeyValue
            pairs={[
              ['current', data.current],
              ['latest', data.latest || '-'],
              ['checked', data.checked_at ? relative(new Date(data.checked_at)) : 'never'],
            ]}
          />

          <div className="flex flex-wrap items-center gap-3">
            <button type="button" disabled={checking} onClick={checkNow} className={buttonCls}>
              {checking ? 'Checking…' : 'Check now'}
            </button>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={data.check_enabled} onChange={(e) => toggleDaily(e.target.checked)} />
              Check for updates daily
            </label>
          </div>

          {data.error && <p className="text-xs text-red-600">{data.error}</p>}

          {data.available && data.latest && (
            <div className="rounded border border-sky-300 bg-sky-50 p-3 dark:border-sky-900 dark:bg-sky-950">
              <p className="mb-2 text-sm">
                Sapien {data.latest} is available (running {data.current}).{' '}
                {data.release_url && (
                  <a href={data.release_url} target="_blank" rel="noreferrer" className="text-sky-700 underline dark:text-sky-400">
                    Release notes
                  </a>
                )}
              </p>
              {data.can_self_upgrade ? (
                <button type="button" disabled={applying} onClick={() => setShowConfirm(true)} className={buttonCls}>
                  {applying ? 'Upgrading…' : `Upgrade to ${data.latest}`}
                </button>
              ) : (
                <div>
                  <p className="mb-1 text-xs text-slate-600 dark:text-slate-400">
                    Installed via {data.install_method || 'an unmanaged build'}; run this to upgrade:
                  </p>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 select-all overflow-x-auto rounded bg-slate-900 px-2 py-1 text-xs text-slate-100">
                      {data.command}
                    </code>
                    {data.command && (
                      <button type="button" onClick={() => copyCommand(data.command!)} className={buttonCls}>
                        {copied ? 'Copied' : 'Copy'}
                      </button>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {showConfirm && data && (
        <ConfirmDialog
          title={`Upgrade to ${data.latest}?`}
          message={<p>Restarts the daemon on the new build. Your session stays signed in, and this tab reloads automatically once it's back.</p>}
          confirmLabel="Upgrade"
          onConfirm={upgrade}
          onClose={() => setShowConfirm(false)}
        />
      )}
    </section>
  );
}
