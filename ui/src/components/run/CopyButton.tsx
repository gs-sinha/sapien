import { useState } from 'react';

// Small copy-to-clipboard button, the same look as JsonView's built-in one,
// factored out so YamlSourcePanel (flow detail) and the edit-and-rerun
// editor (run detail) can both use it over plain text that isn't JSON.
export function CopyButton({ text, label = 'Copy' }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard
          ?.writeText(text)
          .then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          })
          .catch(() => {});
      }}
      className="rounded border border-slate-300 px-2 py-0.5 text-[11px] text-slate-500 hover:text-slate-800 dark:border-slate-700 dark:hover:text-slate-200"
    >
      {copied ? 'Copied' : label}
    </button>
  );
}
