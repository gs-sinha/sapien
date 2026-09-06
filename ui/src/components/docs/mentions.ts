// Cheap, client-side mirror of internal/ingest/docs/refs.go's operation
// mention detection, just enough to turn "service.operationId" and
// "METHOD /path" mentions in a doc's Markdown into links to operation
// detail. Not a faithful port (no fenced-code masking subtleties, no
// schema/concept/field refs): good enough for the docs viewer, not a parser.
import type { Operation } from '../../api/types';

export interface MentionIndex {
  opRegex: RegExp | null;
  knownIds: Set<string>;
  methodPath: Array<{ method: string; path: string; id: string }>;
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function buildMentionIndex(operations: Operation[]): MentionIndex {
  const ids = Array.from(new Set(operations.map((o) => o.id).filter(Boolean)));
  const methodPath = operations
    .filter((o): o is Operation & { http: NonNullable<Operation['http']> } => !!o.http)
    .map((o) => ({ method: o.http.method.toUpperCase(), path: o.http.path, id: o.id }));

  if (ids.length === 0) {
    return { opRegex: null, knownIds: new Set(), methodPath };
  }
  // Longest first so a shorter id that happens to be a prefix of a longer
  // one never wins the match.
  const sorted = [...ids].sort((a, b) => b.length - a.length);
  const opRegex = new RegExp('\\b(' + sorted.map(escapeRegExp).join('|') + ')\\b', 'g');
  return { opRegex, knownIds: new Set(ids), methodPath };
}

function pathMatchesTemplate(raw: string, tmpl: string): boolean {
  const rawSegs = raw.replace(/^\/+|\/+$/g, '').split('/');
  const tmplSegs = tmpl.replace(/^\/+|\/+$/g, '').split('/');
  if (rawSegs.length !== tmplSegs.length) return false;
  for (let i = 0; i < tmplSegs.length; i++) {
    const ts = tmplSegs[i];
    if (ts.length >= 2 && ts[0] === '{' && ts[ts.length - 1] === '}') {
      if (rawSegs[i] === '') return false;
      continue;
    }
    if (rawSegs[i] !== ts) return false;
  }
  return true;
}

const methodPathRe = /\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)[ \t]+(\/[A-Za-z0-9_\-./{}]+)/g;

function linkifySegment(segment: string, index: MentionIndex): string {
  // Operation-id mentions first: their replacement text never contains
  // "METHOD /path"-shaped text, so the second pass below can't re-match
  // anything this pass produced. Doing it in the other order would let the
  // method+path pass's own generated href (which embeds the operation id)
  // get re-wrapped by this pass.
  let out = segment;
  if (index.opRegex) {
    out = out.replace(index.opRegex, (m) => `[${m}](/ui/operations/${encodeURIComponent(m)})`);
  }
  out = out.replace(methodPathRe, (whole, method: string, rawPath: string) => {
    const trimmed = rawPath.replace(/[./]+$/, '');
    const hit = index.methodPath.find((mp) => mp.method === method.toUpperCase() && pathMatchesTemplate(trimmed, mp.path));
    return hit ? `[${whole}](/ui/operations/${encodeURIComponent(hit.id)})` : whole;
  });
  return out;
}

// linkifyMentions turns known operation mentions in Markdown `text` into
// links to /ui/operations/:id, leaving fenced (```) code blocks untouched.
export function linkifyMentions(text: string, index: MentionIndex): string {
  if (!index.opRegex && index.methodPath.length === 0) return text;
  const parts = text.split(/(```[\s\S]*?```)/);
  return parts.map((part, i) => (i % 2 === 1 ? part : linkifySegment(part, index))).join('');
}
