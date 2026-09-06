// Renders Markdown with `marked`, loaded lazily (dynamic import) so the
// dependency only ships in the chunk that actually needs it (docs viewer /
// operation detail's Docs section), never the app shell.
import { useEffect, useState } from 'react';

export function Markdown({ text }: { text: string }) {
  const [html, setHtml] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setHtml(null);
    import('marked').then(({ marked }) => {
      if (cancelled) return;
      const out = marked.parse(text, { async: false }) as string;
      setHtml(out);
    });
    return () => {
      cancelled = true;
    };
  }, [text]);

  if (html === null) {
    return <pre className="whitespace-pre-wrap break-words text-sm text-slate-600 dark:text-slate-400">{text}</pre>;
  }

  return (
    <div
      className="markdown-body max-w-none text-sm leading-6 text-slate-700 [&_a]:text-sky-700 [&_a]:underline [&_code]:rounded [&_code]:bg-slate-100 [&_code]:px-1 [&_code]:py-0.5 [&_code]:text-xs [&_h1]:mt-3 [&_h1]:text-base [&_h1]:font-semibold [&_h2]:mt-3 [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:mt-2 [&_h3]:text-sm [&_h3]:font-semibold [&_li]:ml-4 [&_ol]:list-decimal [&_p]:my-2 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-slate-100 [&_pre]:p-2 [&_pre]:text-xs [&_ul]:list-disc dark:text-slate-300 dark:[&_a]:text-sky-400 dark:[&_code]:bg-slate-800 dark:[&_pre]:bg-slate-900"
      // Doc content comes from the local daemon's own contracts/docs
      // (workspace-trusted, same source the CLI already renders), not
      // third-party user input; no sanitizer dependency is in budget.
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}
