// Minimal YAML dumper for read-only display. domain.Flow.Source (the exact
// file text) is deliberately excluded from JSON (`yaml:"-" json:"-"`), and
// no HTTP route returns raw flow YAML, so FlowDetailPage reconstructs a
// best-effort YAML rendering from the parsed Flow object instead of showing
// the original file's bytes. Good enough to read; not guaranteed to
// round-trip byte for byte (key order, comments, and quoting style are not
// preserved).
function needsQuote(s: string): boolean {
  if (s === '') return true;
  if (/^[\s]|[\s]$/.test(s)) return true;
  if (/^[-?:,[\]{}#&*!|>'"%@`]/.test(s)) return true;
  if (/^(true|false|null|~|[-+]?\d+(\.\d+)?)$/i.test(s)) return true;
  if (/[:#]/.test(s)) return true;
  return false;
}

function scalar(value: unknown): string {
  if (value === null || value === undefined) return 'null';
  if (typeof value === 'string') return needsQuote(value) ? JSON.stringify(value) : value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  return JSON.stringify(value);
}

function isEmpty(value: unknown): boolean {
  if (Array.isArray(value)) return value.length === 0;
  if (value && typeof value === 'object') return Object.keys(value).length === 0;
  return false;
}

export function dumpYaml(value: unknown, indent = 0): string {
  const pad = '  '.repeat(indent);

  if (Array.isArray(value)) {
    if (value.length === 0) return pad + '[]';
    return value
      .map((item) => {
        if (item !== null && typeof item === 'object' && !isEmpty(item)) {
          const inner = dumpYaml(item, indent + 1);
          const lines = inner.split('\n');
          const first = lines[0].replace(new RegExp(`^ {${(indent + 1) * 2}}`), '');
          return `${pad}- ${first}${lines.length > 1 ? '\n' + lines.slice(1).join('\n') : ''}`;
        }
        return `${pad}- ${scalar(item)}`;
      })
      .join('\n');
  }

  if (value !== null && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>).filter(([, v]) => v !== undefined);
    if (entries.length === 0) return pad + '{}';
    return entries
      .map(([k, v]) => {
        if (v !== null && typeof v === 'object' && !isEmpty(v)) {
          return `${pad}${k}:\n${dumpYaml(v, indent + 1)}`;
        }
        return `${pad}${k}: ${scalar(v)}`;
      })
      .join('\n');
  }

  return pad + scalar(value);
}
