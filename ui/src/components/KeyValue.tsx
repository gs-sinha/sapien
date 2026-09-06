import type { ReactNode } from 'react';

export function KeyValue({ pairs }: { pairs: Array<[string, ReactNode]> }) {
  if (pairs.length === 0) return null;
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-sm">
      {pairs.map(([k, v]) => (
        <div className="contents" key={k}>
          <dt className="text-slate-500">{k}</dt>
          <dd className="break-all">{v}</dd>
        </div>
      ))}
    </dl>
  );
}
