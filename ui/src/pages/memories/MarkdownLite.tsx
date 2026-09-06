// Minimal read-only Markdown rendering for a memory's text body: paragraphs,
// inline code spans, and bullet/numbered lists. Not a Markdown parser
// dependency (per the "no new runtime deps" budget) -- just enough
// structure to make a memory readable.
import { Fragment } from 'react';

function inline(text: string, keyPrefix: string) {
  const parts = text.split(/(`[^`]*`)/g);
  return parts.map((part, i) => {
    if (part.startsWith('`') && part.endsWith('`') && part.length >= 2) {
      return (
        <code key={`${keyPrefix}-${i}`} className="rounded bg-slate-100 px-1 py-0.5 font-mono text-[0.85em] dark:bg-slate-800">
          {part.slice(1, -1)}
        </code>
      );
    }
    return <Fragment key={`${keyPrefix}-${i}`}>{part}</Fragment>;
  });
}

function isBullet(line: string): boolean {
  return /^\s*[-*]\s+/.test(line);
}
function isNumbered(line: string): boolean {
  return /^\s*\d+\.\s+/.test(line);
}
function stripListMarker(line: string): string {
  return line.replace(/^\s*(?:[-*]|\d+\.)\s+/, '');
}

export function MarkdownLite({ text }: { text: string }) {
  const blocks = text.replace(/\r\n/g, '\n').split(/\n{2,}/);

  return (
    <div className="space-y-3 text-sm leading-6">
      {blocks.map((block, bi) => {
        const lines = block.split('\n').filter((l) => l.trim() !== '');
        if (lines.length === 0) return null;

        if (lines.every(isBullet)) {
          return (
            <ul key={bi} className="list-disc space-y-0.5 pl-5">
              {lines.map((l, li) => (
                <li key={li}>{inline(stripListMarker(l), `${bi}-${li}`)}</li>
              ))}
            </ul>
          );
        }
        if (lines.every(isNumbered)) {
          return (
            <ol key={bi} className="list-decimal space-y-0.5 pl-5">
              {lines.map((l, li) => (
                <li key={li}>{inline(stripListMarker(l), `${bi}-${li}`)}</li>
              ))}
            </ol>
          );
        }
        return (
          <p key={bi} className="whitespace-pre-wrap">
            {inline(lines.join('\n'), String(bi))}
          </p>
        );
      })}
    </div>
  );
}
