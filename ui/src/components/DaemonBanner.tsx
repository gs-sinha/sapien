import { useDaemon } from '../state/daemon';

// Shown only when this tab has outlived the daemon it was served by --
// replaced by an upgrade, idle-exited, or restarted with a new token.
// Each of those used to be silent and to look like a broken app: requests
// failing while the event socket retried forever, or every call refused
// by a daemon that is plainly reachable.
export function DaemonBanner() {
  const state = useDaemon((s) => s.state);
  const loadedVersion = useDaemon((s) => s.loadedVersion);
  const runningVersion = useDaemon((s) => s.runningVersion);
  const sessionStale = useDaemon((s) => s.sessionStale);
  const probe = useDaemon((s) => s.probe);
  // Set for the duration of a Settings-initiated restart/upgrade (PLAN
  // §34f items 3/4, state/daemon.ts's waitForRestart): the old process
  // going down makes a probe briefly see 'gone', which would otherwise
  // flash this banner over a restart the user just asked for.
  const restarting = useDaemon((s) => s.restarting);

  // A gone daemon explains a stale session too, so it is reported first.
  if (state === 'gone') return restarting ? null : <GoneBanner onRetry={() => void probe()} />;
  if (state === 'replaced') {
    return <ReplacedBanner loadedVersion={loadedVersion} runningVersion={runningVersion} />;
  }
  if (sessionStale) return <SessionBanner />;
  return null;
}

function Banner({ tone, children }: { tone: 'warn' | 'error'; children: React.ReactNode }) {
  return (
    <div
      role="status"
      className={`flex items-center justify-between gap-3 px-3 py-2 text-xs ${
        tone === 'warn'
          ? 'bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-100'
          : 'bg-rose-100 text-rose-900 dark:bg-rose-950 dark:text-rose-100'
      }`}
    >
      {children}
    </div>
  );
}

function BannerButton({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="shrink-0 rounded border border-current px-2 py-0.5 font-medium hover:bg-black/5 dark:hover:bg-white/10"
    >
      {children}
    </button>
  );
}

function ReplacedBanner({
  loadedVersion,
  runningVersion,
}: {
  loadedVersion: string | null;
  runningVersion: string | null;
}) {
  return (
    <Banner tone="warn">
      <span>
        Sapien was updated {loadedVersion && runningVersion ? `(${loadedVersion} → ${runningVersion})` : ''}. This tab
        is still running the old UI — reload to match the daemon.
      </span>
      <BannerButton onClick={() => window.location.reload()}>Reload</BannerButton>
    </Banner>
  );
}

function GoneBanner({ onRetry }: { onRetry: () => void }) {
  return (
    <Banner tone="error">
      <span>
        The Sapien daemon isn’t running. It exits after 30 minutes with nothing connected; start it again from the
        Sapien app or <code className="font-mono">sapien ui</code>.
      </span>
      <BannerButton onClick={onRetry}>Check again</BannerButton>
    </Banner>
  );
}

// The daemon is reachable and the right build, but this tab is signed in
// to a previous one: `sapien serve` mints a new bearer token on every
// start. Relaunching mints a cookie every tab at this origin shares, so
// the fix is one command and a reload, not re-authenticating per tab.
function SessionBanner() {
  return (
    <Banner tone="warn">
      <span>
        This tab’s session is from a previous daemon, so its requests are being refused. Run{' '}
        <code className="font-mono">sapien ui</code> (or open the Sapien app) to sign back in, then reload.
      </span>
      <BannerButton onClick={() => window.location.reload()}>Reload</BannerButton>
    </Banner>
  );
}
