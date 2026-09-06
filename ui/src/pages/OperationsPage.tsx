import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { IntentBundle } from './operations/IntentBundle';
import { KeywordSearch } from './operations/KeywordSearch';

function wordCount(s: string): number {
  return s.trim().split(/\s+/).filter(Boolean).length;
}

// looksLikePath: a pasted URL or path ("/v1/orders", "POST /v1/orders/123",
// "https://host/base/v1/orders?x=1", "v1/orders") is a keyword lookup, never
// an intent, however many words it has; the daemon resolves it against the
// operations' path templates.
export function looksLikePath(s: string): boolean {
  const t = s.trim();
  return /^(?:[A-Za-z]+\s+)?(?:https?:\/\/|\/|[\w.-]+\/[\w{}.-])/.test(t) && !/\s.*\s/.test(t.replace(/^[A-Za-z]+\s+/, ''));
}

// Operations page with intent search (build brief item 3): one search box.
// More than two words (or an explicit ?intent=, e.g. from the layout's own
// SearchBox) builds a context bundle; otherwise it's a plain keyword/catalog
// search. The URL is the source of truth for which mode is active and what
// was searched, so a result is always a shareable link.
export default function OperationsPage() {
  const [params, setParams] = useSearchParams();
  const intentParam = params.get('intent') || '';
  const qParam = params.get('q') || '';
  const serviceParam = params.get('service') || '';
  const methodParam = params.get('method') || '';
  const pathLike = looksLikePath(intentParam);
  const isIntent = intentParam !== '' && !pathLike;
  const effectiveQ = qParam || (pathLike ? intentParam : '');

  const [text, setText] = useState(isIntent ? intentParam : effectiveQ);
  const [service, setService] = useState(serviceParam);
  const [method, setMethod] = useState(methodParam);

  // Keep the form in sync when the URL changes from outside this form
  // (back/forward navigation, or the layout's SearchBox setting ?intent=).
  useEffect(() => {
    setText(isIntent ? intentParam : effectiveQ);
    setService(serviceParam);
    setMethod(methodParam);
    // isIntent is derived from intentParam; only the params themselves
    // should retrigger this sync.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intentParam, qParam, serviceParam, methodParam]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = text.trim();
    if (wordCount(trimmed) > 2 && !looksLikePath(trimmed)) {
      setParams({ intent: trimmed });
      return;
    }
    const next: Record<string, string> = {};
    if (trimmed) next.q = trimmed;
    if (service.trim()) next.service = service.trim();
    if (method.trim()) next.method = method.trim();
    setParams(next);
  };

  return (
    <div className="flex h-full flex-col p-4">
      <h1 className="mb-4 text-lg font-semibold">Operations</h1>
      <form className="mb-1 flex flex-wrap gap-2" onSubmit={submit}>
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder='keyword, or an intent like "allocate a rider to an order"'
          className="w-80 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        />
        {!isIntent && (
          <>
            <input
              value={service}
              onChange={(e) => setService(e.target.value)}
              placeholder="service filter"
              className="w-40 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
            <input
              value={method}
              onChange={(e) => setMethod(e.target.value)}
              placeholder="method filter, e.g. POST"
              className="w-40 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
          </>
        )}
        <button type="submit" className="rounded border border-slate-300 px-3 py-1 text-sm dark:border-slate-700">
          Search
        </button>
      </form>
      <p className="mb-3 text-xs text-slate-400">
        {isIntent
          ? 'Intent mode: building an agent-ready context bundle.'
          : 'Keyword mode. Type more than two words to search by intent instead.'}
      </p>

      {isIntent ? <IntentBundle intent={intentParam} /> : <KeywordSearch query={effectiveQ} service={serviceParam} method={methodParam} />}
    </div>
  );
}
