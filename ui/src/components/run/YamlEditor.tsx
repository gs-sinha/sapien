import { useRef } from 'react';
import type { ReactNode } from 'react';

// A plain <textarea> (per the "no CodeMirror" budget) with a light-weight
// syntax-colour illusion: a non-interactive <pre> with the same font,
// padding, and line height sits behind it and is kept in scroll sync, while
// the textarea's own text is transparent (only its caret shows) so the
// user reads and edits through the highlighted layer. Deliberately not a
// shared component/YamlView.tsx variant -- that component renders static
// text only, this one owns an editable value, so the two aren't rewrites
// of each other.
const keyRe = /^(\s*(?:-\s+)?)([A-Za-z0-9_.-]+)(:)(\s.*|)$/;

function colorizeValue(value: string, key: number): ReactNode {
  const trimmed = value.trim();
  if (trimmed === '') return value;
  if (/^\$\{[^}]*\}/.test(trimmed)) {
    return (
      <span key={key} className="text-sky-700 dark:text-sky-400">
        {value}
      </span>
    );
  }
  if (/^(true|false|null|~)$/i.test(trimmed)) {
    return (
      <span key={key} className="text-violet-700 dark:text-violet-400">
        {value}
      </span>
    );
  }
  if (/^-?\d+(\.\d+)?$/.test(trimmed)) {
    return (
      <span key={key} className="text-violet-700 dark:text-violet-400">
        {value}
      </span>
    );
  }
  return value;
}

function highlightLine(line: string, key: number): ReactNode {
  const m = line.match(keyRe);
  if (m) {
    const [, indent, k, colon, rest] = m;
    return (
      <span key={key}>
        {indent}
        <span className="text-blue-700 dark:text-blue-300">{k}</span>
        {colon}
        {colorizeValue(rest, key)}
      </span>
    );
  }
  return line;
}

export function YamlEditor({ value, onChange, rows = 22 }: { value: string; onChange: (v: string) => void; rows?: number }) {
  const preRef = useRef<HTMLPreElement>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const lines = value.split('\n');

  const syncScroll = () => {
    if (preRef.current && taRef.current) {
      preRef.current.scrollTop = taRef.current.scrollTop;
      preRef.current.scrollLeft = taRef.current.scrollLeft;
    }
  };

  const lineHeightPx = 18;
  return (
    <div className="relative overflow-hidden rounded border border-slate-300 dark:border-slate-700" style={{ height: rows * lineHeightPx }}>
      <pre
        ref={preRef}
        aria-hidden
        className="pointer-events-none absolute inset-0 overflow-auto whitespace-pre-wrap break-words bg-slate-50 p-2 font-mono text-xs leading-[18px] text-slate-800 dark:bg-slate-900 dark:text-slate-200"
      >
        {lines.map((l, i) => (
          <div key={i}>{highlightLine(l, i) || ' '}</div>
        ))}
      </pre>
      <textarea
        ref={taRef}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onScroll={syncScroll}
        spellCheck={false}
        aria-label="Flow YAML"
        className="absolute inset-0 h-full w-full resize-none overflow-auto whitespace-pre-wrap break-words bg-transparent p-2 font-mono text-xs leading-[18px] text-transparent caret-slate-900 outline-none dark:caret-slate-100"
      />
    </div>
  );
}
