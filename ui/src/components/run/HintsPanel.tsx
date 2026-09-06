import { Link } from 'react-router-dom';
import type { Hint } from '../../api/types-runs';

const kindLabel: Record<Hint['kind'], string> = {
  contract: 'contract',
  doc: 'doc',
  memory: 'memory',
};

const kindClass: Record<Hint['kind'], string> = {
  contract: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
  doc: 'bg-sky-100 text-sky-800 dark:bg-sky-900/40 dark:text-sky-300',
  memory: 'bg-violet-100 text-violet-800 dark:bg-violet-900/40 dark:text-violet-300',
};

// hintLink returns where a hint's "Ref" can be reopened. There is no
// dedicated docs route in this app yet, so a doc hint links into the
// operations search instead of a docs page (see api/flowsExtra.ts's
// getRunHints doc comment for why the /v1/runs/{id}/hints route itself may
// not exist yet either).
function hintLink(h: Hint): string | undefined {
  if (h.kind === 'memory' && h.ref.memory_id) return `/ui/memories/${encodeURIComponent(h.ref.memory_id)}`;
  if (h.kind === 'doc') {
    const intent = h.matched_on || h.ref.service || h.title;
    return `/ui/operations?intent=${encodeURIComponent(intent)}`;
  }
  return undefined;
}

function HintRow({ hint }: { hint: Hint }) {
  const href = hintLink(hint);
  const body = (
    <>
      <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide ${kindClass[hint.kind]}`}>
        {kindLabel[hint.kind]}
      </span>
      <span className="flex-1 text-sm">{hint.title}</span>
      {hint.step_id && <span className="shrink-0 font-mono text-[11px] text-slate-400">step {hint.step_id}</span>}
    </>
  );
  if (href) {
    return (
      <Link to={href} className="flex items-center gap-2 rounded px-2 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800">
        {body}
      </Link>
    );
  }
  return <div className="flex items-center gap-2 px-2 py-1.5">{body}</div>;
}

export function HintsPanel({ hints }: { hints: Hint[] }) {
  if (hints.length === 0) return null;
  return (
    <div className="rounded border border-amber-200 bg-amber-50/60 dark:border-amber-900 dark:bg-amber-950/30">
      <h2 className="border-b border-amber-200 px-2 py-1.5 text-xs font-semibold uppercase tracking-wide text-amber-800 dark:border-amber-900 dark:text-amber-300">
        Might explain it
      </h2>
      <div className="divide-y divide-amber-100 dark:divide-amber-900/60">
        {hints.map((h, i) => (
          <HintRow key={i} hint={h} />
        ))}
      </div>
    </div>
  );
}
