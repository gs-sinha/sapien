// Collapsible JSON tree. Performance rules (hard budget from the team):
// - children are only mounted (rendered) once a node is expanded;
// - objects/arrays deeper than DEFAULT_COLLAPSE_DEPTH start collapsed;
// - arrays over ARRAY_PAGE_SIZE items render one page at a time;
// - a string over STRING_TRUNCATE_BYTES is shown truncated with copy/download
//   of the full raw text rather than rendered inline.
import { useMemo, useState } from 'react';

const STRING_TRUNCATE_BYTES = 256 * 1024;
const ARRAY_PAGE_SIZE = 200;
const DEFAULT_COLLAPSE_DEPTH = 2;

function safeStringify(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2) ?? String(v);
  } catch {
    return String(v);
  }
}

function useCopy(text: string) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard
      ?.writeText(text)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => {});
  };
  return { copied, copy };
}

function downloadText(text: string, filename: string) {
  const blob = new Blob([text], { type: 'text/plain' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

export function JsonView({ data, name }: { data: unknown; name?: string }) {
  const text = useMemo(() => safeStringify(data), [data]);
  const { copied, copy } = useCopy(text);
  return (
    <div className="font-mono text-xs leading-5">
      <div className="mb-1 flex justify-end">
        <button
          type="button"
          onClick={copy}
          className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-500 hover:text-slate-800 dark:border-slate-700 dark:hover:text-slate-200"
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <JsonNode value={data} name={name} depth={0} />
    </div>
  );
}

function Toggle({ expanded, onClick }: { expanded: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="mr-1 inline-block w-3 select-none text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
      aria-label={expanded ? 'collapse' : 'expand'}
    >
      {expanded ? '▾' : '▸'}
    </button>
  );
}

function KeyLabel({ name }: { name?: string }) {
  if (name === undefined) return null;
  return <span className="text-blue-700 dark:text-blue-300">{name}: </span>;
}

function JsonNode({ value, name, depth }: { value: unknown; name?: string; depth: number }) {
  if (value === null || value === undefined) {
    return (
      <div>
        <KeyLabel name={name} />
        <span className="text-slate-400">{value === null ? 'null' : 'undefined'}</span>
      </div>
    );
  }
  if (typeof value === 'string') return <StringLeaf name={name} value={value} />;
  if (typeof value === 'number') {
    return (
      <div>
        <KeyLabel name={name} />
        <span className="text-violet-700 dark:text-violet-400">{String(value)}</span>
      </div>
    );
  }
  if (typeof value === 'boolean') {
    return (
      <div>
        <KeyLabel name={name} />
        <span className="text-violet-700 dark:text-violet-400">{String(value)}</span>
      </div>
    );
  }
  if (Array.isArray(value)) return <ArrayNode value={value} name={name} depth={depth} />;
  if (typeof value === 'object') return <ObjectNode value={value as Record<string, unknown>} name={name} depth={depth} />;
  return (
    <div>
      <KeyLabel name={name} />
      <span>{String(value)}</span>
    </div>
  );
}

function StringLeaf({ name, value }: { name?: string; value: string }) {
  const tooLarge = value.length > STRING_TRUNCATE_BYTES;
  const { copied, copy } = useCopy(value);
  if (!tooLarge) {
    return (
      <div className="break-all">
        <KeyLabel name={name} />
        <span className="text-amber-700 dark:text-amber-300">&quot;{value}&quot;</span>
      </div>
    );
  }
  return (
    <div className="break-all">
      <KeyLabel name={name} />
      <span className="text-amber-700 dark:text-amber-300">
        &quot;{value.slice(0, 500)}&hellip;&quot;
      </span>
      <div className="mt-1 flex items-center gap-2 text-[11px] text-slate-500">
        <span>truncated, {Math.round(value.length / 1024)} KB total</span>
        <button type="button" className="underline" onClick={copy}>
          {copied ? 'copied' : 'copy full text'}
        </button>
        <button type="button" className="underline" onClick={() => downloadText(value, `${name || 'value'}.txt`)}>
          download
        </button>
      </div>
    </div>
  );
}

function ObjectNode({ value, name, depth }: { value: Record<string, unknown>; name?: string; depth: number }) {
  const keys = Object.keys(value);
  const [expanded, setExpanded] = useState(depth < DEFAULT_COLLAPSE_DEPTH);

  return (
    <div>
      <div>
        <Toggle expanded={expanded} onClick={() => setExpanded((e) => !e)} />
        <KeyLabel name={name} />
        <span className="text-slate-400">
          {'{'}
          {!expanded && keys.length > 0 ? `…${keys.length}` : ''}
          {!expanded ? '}' : ''}
        </span>
      </div>
      {expanded && (
        <div className="ml-4 border-l border-slate-200 pl-2 dark:border-slate-800">
          {keys.length === 0 ? (
            <div className="text-slate-400">(empty)</div>
          ) : (
            keys.map((k) => <JsonNode key={k} value={value[k]} name={k} depth={depth + 1} />)
          )}
          <div className="text-slate-400">{'}'}</div>
        </div>
      )}
    </div>
  );
}

function ArrayNode({ value, name, depth }: { value: unknown[]; name?: string; depth: number }) {
  const [expanded, setExpanded] = useState(depth < DEFAULT_COLLAPSE_DEPTH);
  const [visibleCount, setVisibleCount] = useState(Math.min(ARRAY_PAGE_SIZE, value.length));

  return (
    <div>
      <div>
        <Toggle expanded={expanded} onClick={() => setExpanded((e) => !e)} />
        <KeyLabel name={name} />
        <span className="text-slate-400">
          {'['}
          {!expanded && value.length > 0 ? `…${value.length}` : ''}
          {!expanded ? ']' : ''}
        </span>
      </div>
      {expanded && (
        <div className="ml-4 border-l border-slate-200 pl-2 dark:border-slate-800">
          {value.length === 0 ? (
            <div className="text-slate-400">(empty)</div>
          ) : (
            value.slice(0, visibleCount).map((item, i) => <JsonNode key={i} value={item} name={`[${i}]`} depth={depth + 1} />)
          )}
          {visibleCount < value.length && (
            <button
              type="button"
              className="my-1 rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-500 hover:text-slate-800 dark:border-slate-700 dark:hover:text-slate-200"
              onClick={() => setVisibleCount((c) => Math.min(c + ARRAY_PAGE_SIZE, value.length))}
            >
              Show {Math.min(ARRAY_PAGE_SIZE, value.length - visibleCount)} more ({visibleCount}/{value.length})
            </button>
          )}
          <div className="text-slate-400">{']'}</div>
        </div>
      )}
    </div>
  );
}
