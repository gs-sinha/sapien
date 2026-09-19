// Small hand-written unified-diff renderer for the Changes page's right
// pane: monospace, +/- line colouring, no highlighter dependency (same
// "no Monaco/CodeMirror" budget as components/YamlView.tsx). Horizontal
// scroll is contained to this pane, never the page.
function classifyLine(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'text-slate-400';
  if (line.startsWith('@@')) return 'text-sky-700 dark:text-sky-400';
  if (line.startsWith('+')) return 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300';
  if (line.startsWith('-')) return 'bg-red-50 text-red-800 dark:bg-red-950 dark:text-red-300';
  return 'text-slate-700 dark:text-slate-300';
}

export function DiffView({ diff }: { diff: string }) {
  const lines = diff.split('\n');
  return (
    <div className="overflow-x-auto rounded border border-slate-200 dark:border-slate-800">
      <pre className="min-w-max py-1 font-mono text-xs leading-5">
        {lines.map((line, i) => (
          <div key={i} className={`whitespace-pre px-2 ${classifyLine(line)}`}>
            {line || ' '}
          </div>
        ))}
      </pre>
    </div>
  );
}
