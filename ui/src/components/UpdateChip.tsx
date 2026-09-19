// StatusBar's "v1.4.0 available" chip (PLAN §34f item 4 / "Also:" note):
// one GET /v1/update on app mount, no polling (the daemon itself only
// checks the release at most once a day), fails silently since this is a
// nice-to-have that must never blank the status bar for an old daemon
// build or an offline machine.
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { updates } from '../api/client';
import type { UpdateInfo } from '../api/types';

export function UpdateChip() {
  const [info, setInfo] = useState<UpdateInfo | null>(null);

  useEffect(() => {
    let cancelled = false;
    updates
      .get()
      .then((u) => {
        if (!cancelled) setInfo(u);
      })
      .catch(() => {
        // A daemon too old for this route, or unreachable: say nothing.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!info?.available || !info.latest) return null;

  return (
    <Link
      to="/ui/settings#updates"
      title={`Sapien ${info.latest} is available (running ${info.current})`}
      className="rounded-full bg-sky-100 px-2 py-0.5 text-xs font-medium text-sky-800 hover:bg-sky-200 dark:bg-sky-950 dark:text-sky-300 dark:hover:bg-sky-900"
    >
      {info.latest.startsWith('v') ? info.latest : `v${info.latest}`} available
    </Link>
  );
}
