import { useEffect } from 'react';
import type { ReactNode } from 'react';

// Minimal backdrop + centered panel, used by SaveExampleDialog and (as an
// inline alternative) nowhere else yet. Deliberately not a full focus-trap
// implementation -- Escape-to-close and a backdrop click are enough for a
// small form dialog, and keeps this dependency-free.
export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="max-h-[85vh] w-full max-w-lg overflow-auto rounded bg-white p-4 shadow-xl dark:bg-slate-900"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-semibold">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            className="text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
            aria-label="close"
          >
            &times;
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
