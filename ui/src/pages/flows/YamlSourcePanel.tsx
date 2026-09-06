import { useState } from 'react';
import { CopyButton } from '../../components/run/CopyButton';
import { YamlView } from '../../components/YamlView';

// Collapsed by default: a real flow's YAML runs to hundreds of lines, and
// reading it is not what this page is for -- the materialized step list above
// is. The header still says how big it is, and Copy works without expanding.
export function YamlSourcePanel({ source, defaultOpen = false }: { source: string; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const lineCount = source ? source.split('\n').length : 0;

  return (
    <div className="rounded border border-slate-200 dark:border-slate-800">
      <div className="flex items-center gap-2 px-3 py-2">
        <button type="button" onClick={() => setOpen((o) => !o)} className="flex items-center gap-2 text-sm font-semibold">
          <span className="w-3 text-slate-400">{open ? '▾' : '▸'}</span>
          YAML
          {lineCount > 0 && <span className="font-normal text-xs text-slate-400">{lineCount} lines</span>}
        </button>
        <span className="ml-auto">
          <CopyButton text={source} />
        </span>
      </div>
      {open && (
        <div className="border-t border-slate-100 p-3 dark:border-slate-900">
          {source ? <YamlView source={source} /> : <div className="text-xs text-slate-400">No source available for this flow.</div>}
        </div>
      )}
    </div>
  );
}
