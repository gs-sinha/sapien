import { useEffect } from 'react';
import { NavLink } from 'react-router-dom';
import { WorkspacePicker } from './WorkspacePicker';
import { useFrictionCount } from '../state/friction';
// PLAN §34f item 1: the Changes page's nav badge (number of changed files,
// hidden at 0 or when the workspace isn't in git). See state/changesCount.ts.
import { ensureChangesCountSubscribed, useChangesCount } from '../state/changesCount';

const links = [
  { to: '/ui/flows', label: 'Flows' },
  { to: '/ui/runs', label: 'Runs' },
  { to: '/ui/services', label: 'Services' },
  { to: '/ui/operations', label: 'Operations' },
  { to: '/ui/examples', label: 'Examples' },
  { to: '/ui/memories', label: 'Memories' },
  { to: '/ui/friction', label: 'Friction' },
  { to: '/ui/events', label: 'Events' },
  // Phase 7b (PLAN §34c): the agent pane (src/pages/AgentPage.tsx).
  { to: '/ui/agent', label: 'Agent' },
  { to: '/ui/changes', label: 'Changes' },
];

export function Nav() {
  const pendingFriction = useFrictionCount((s) => s.pending);
  const changesCount = useChangesCount((s) => (s.inGit ? s.count : 0));

  // One cheap list() call on mount; FrictionPage refreshes this same store
  // after it sends or drops a report so the badge stays in sync without
  // polling. See state/friction.ts.
  useEffect(() => {
    useFrictionCount.getState().refresh();
    ensureChangesCountSubscribed();
    useChangesCount.getState().refresh();
  }, []);

  return (
    <nav className="flex h-full w-44 shrink-0 flex-col gap-0.5 border-r border-slate-200 p-3 dark:border-slate-800">
      <WorkspacePicker />
      {links.map((l) => (
        <NavLink
          key={l.to}
          to={l.to}
          className={({ isActive }) =>
            `rounded px-2 py-1.5 text-sm ${
              isActive
                ? 'bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900'
                : 'text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-900'
            }`
          }
        >
          <span className="flex items-center justify-between gap-2">
            {l.label}
            {l.to === '/ui/friction' && pendingFriction > 0 && (
              <span className="rounded-full bg-amber-500 px-1.5 py-0.5 text-[10px] font-semibold leading-none text-white">
                {pendingFriction}
              </span>
            )}
            {l.to === '/ui/changes' && changesCount > 0 && (
              <span className="rounded-full bg-sky-600 px-1.5 py-0.5 text-[10px] font-semibold leading-none text-white">
                {changesCount}
              </span>
            )}
          </span>
        </NavLink>
      ))}
    </nav>
  );
}
