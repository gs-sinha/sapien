// Colour mapping shared by run status, step status, and service sync
// status -- they're disjoint string sets so one component covers all three.
const colors: Record<string, string> = {
  // "good"
  passed: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300',
  ok: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300',
  // "bad"
  failed: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300',
  errored: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300',
  error: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300',
  // "in progress"
  running: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  requesting: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  polling: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  asserting: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  resolving: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300',
  // "waiting / neutral"
  queued: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
  pending: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
  // "stopped"
  cancelled: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
  skipped: 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400',
};

export function StatusPill({ status }: { status: string }) {
  const cls = colors[status] || 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
  return <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${cls}`}>{status}</span>;
}
