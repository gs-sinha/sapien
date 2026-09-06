// A drawer showing one doc's full rendered Markdown, with section anchors
// and operation mentions turned into links. Lives inside ServiceDetailPage
// (App.tsx's route table has no nested "/ui/services/:name/docs/*" route,
// so this is state driven, not its own route) but keeps its open doc in the
// URL's `doc`/`section` query params so the view is still shareable and a
// link from OperationDetailPage's Docs section can open it directly.
import { useEffect, useMemo, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { docs as docsApi, findDocSection } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import type { Doc, Operation } from '../../api/types';
import { Markdown } from './Markdown';
import { buildMentionIndex, linkifyMentions } from './mentions';

// The full-text fetch (GET /v1/docs/{service}/{path}) can 404 -- a
// contract-embedded doc's path (e.g. "contract#tag:Awb") doesn't always
// resolve on its own even once percent-encoded -- and this drawer has no
// context-bundle snippet to fall back on the way OperationDetailPage does,
// so on failure it shows a muted note instead of throwing or printing an
// error line.
async function loadDoc(service: string, path: string): Promise<{ doc: Doc | null; unavailable: boolean }> {
  try {
    return { doc: await docsApi.get(service, path), unavailable: false };
  } catch {
    return { doc: null, unavailable: true };
  }
}

function anchorId(sectionId: string): string {
  return 'docsec-' + sectionId.replace(/[^a-zA-Z0-9_-]/g, '-');
}

export function DocViewer({
  service,
  path,
  section,
  operations,
  onClose,
}: {
  service: string;
  path: string;
  section?: string | null;
  operations: Operation[];
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const containerRef = useRef<HTMLDivElement>(null);
  const { data, loading } = useAsync(() => loadDoc(service, path), [service, path]);
  const doc = data?.doc ?? null;
  const unavailable = data?.unavailable ?? false;
  const index = useMemo(() => buildMentionIndex(operations), [operations]);

  useEffect(() => {
    if (!doc || !section) return;
    const sec = findDocSection(doc, section);
    if (!sec) return;
    const el = document.getElementById(anchorId(sec.id));
    el?.scrollIntoView({ block: 'start' });
  }, [doc, section]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  // Event delegation: doc bodies are rendered from raw HTML
  // (dangerouslySetInnerHTML), so operation-mention links are plain <a>
  // tags, not <Link>. Intercept clicks on same-app hrefs to navigate via
  // the router instead of a full page reload.
  const onBodyClick = (e: React.MouseEvent<HTMLDivElement>) => {
    const a = (e.target as HTMLElement).closest('a');
    if (!a) return;
    const href = a.getAttribute('href') || '';
    if (href.startsWith('/ui/')) {
      e.preventDefault();
      navigate(href);
    }
  };

  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/30" onClick={onClose}>
      <div
        ref={containerRef}
        className="h-full w-full max-w-2xl overflow-y-auto bg-white p-4 shadow-xl dark:bg-slate-950"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center justify-between border-b border-slate-200 pb-2 dark:border-slate-800">
          <h2 className="text-sm font-semibold">{doc?.title || path}</h2>
          <button type="button" onClick={onClose} className="rounded border border-slate-300 px-2 py-0.5 text-xs dark:border-slate-700">
            Close
          </button>
        </div>
        {loading && <div className="text-sm text-slate-400">Loading…</div>}
        {!loading && unavailable && <div className="text-sm italic text-slate-400">Full text unavailable for this doc.</div>}
        {doc && (
          <div className="space-y-5" onClick={onBodyClick}>
            {(doc.sections || []).map((sec) => (
              <div key={sec.id} id={anchorId(sec.id)}>
                <h3 className="mb-1 text-sm font-semibold text-slate-800 dark:text-slate-200">{sec.heading}</h3>
                <Markdown text={linkifyMentions(sec.body, index)} />
              </div>
            ))}
            {(doc.sections || []).length === 0 && <div className="text-sm text-slate-400">This doc has no sections.</div>}
          </div>
        )}
      </div>
    </div>
  );
}
