// Plain <pre> block with light, regex-based syntax colouring. No highlighter
// dependency: this is deliberately a ~100-line component, not a language
// grammar, per the "no Monaco/CodeMirror in the shell" budget.
import { Fragment } from 'react';
import type { Key, ReactNode } from 'react';

const keyRe = /^(\s*(?:-\s+)?)([A-Za-z0-9_.\-]+)(:)(\s.*|)$/;
const commentRe = /(#.*)$/;

function colorizeValue(value: string, key: Key): ReactNode {
  const trimmed = value.trim();
  if (trimmed === '') return value;
  if (/^\$\{[^}]*\}$/.test(trimmed) || trimmed.includes('${')) {
    return (
      <span key={key} className="text-fuchsia-700 dark:text-fuchsia-400">
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
      <span key={key} className="text-amber-700 dark:text-amber-400">
        {value}
      </span>
    );
  }
  if (/^["'].*["']$/.test(trimmed)) {
    return (
      <span key={key} className="text-emerald-700 dark:text-emerald-400">
        {value}
      </span>
    );
  }
  return value;
}

function colorizeLine(line: string, key: Key): ReactNode {
  const commentMatch = line.match(commentRe);
  let code = line;
  let comment = '';
  if (commentMatch && commentMatch.index !== undefined) {
    // Naive: doesn't special-case '#' inside quoted strings, which is fine
    // for a light hint of colour, not a parser.
    code = line.slice(0, commentMatch.index);
    comment = commentMatch[1];
  }

  if (code.trim() === '') {
    return (
      <Fragment key={key}>
        {code}
        {comment && <span className="text-slate-400">{comment}</span>}
      </Fragment>
    );
  }

  const m = code.match(keyRe);
  if (m) {
    const [, indent, k, colon, rest] = m;
    return (
      <Fragment key={key}>
        {indent}
        <span className="text-sky-700 dark:text-sky-400">{k}</span>
        {colon}
        {colorizeValue(rest, `${key}-v`)}
        {comment && <span className="text-slate-400">{comment}</span>}
      </Fragment>
    );
  }

  const dashMatch = code.match(/^(\s*)(-\s+)(.*)$/);
  if (dashMatch) {
    const [, indent, dash, rest] = dashMatch;
    return (
      <Fragment key={key}>
        {indent}
        <span className="text-slate-400">{dash}</span>
        {colorizeValue(rest, `${key}-v`)}
        {comment && <span className="text-slate-400">{comment}</span>}
      </Fragment>
    );
  }

  return (
    <Fragment key={key}>
      {code}
      {comment && <span className="text-slate-400">{comment}</span>}
    </Fragment>
  );
}

export function YamlView({ source }: { source: string }) {
  const lines = source.split('\n');
  return (
    <pre className="overflow-x-auto rounded bg-slate-50 p-3 font-mono text-xs leading-5 text-slate-800 dark:bg-slate-900 dark:text-slate-200">
      <code>
        {lines.map((line, i) => (
          <div key={i}>{colorizeLine(line, i) || ' '}</div>
        ))}
      </code>
    </pre>
  );
}
