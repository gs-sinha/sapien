import type { ReactNode } from 'react';

export function EmptyState({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 rounded border border-dashed border-slate-300 p-10 text-center text-slate-500 dark:border-slate-700">
      <div className="text-sm font-medium text-slate-700 dark:text-slate-300">{title}</div>
      {hint && <div className="max-w-md text-xs">{hint}</div>}
      {action}
    </div>
  );
}
