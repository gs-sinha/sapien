import { useToasts } from '../state/toast';

const kindClass: Record<string, string> = {
  info: 'bg-slate-800 text-white',
  error: 'bg-red-700 text-white',
  success: 'bg-emerald-700 text-white',
};

export function Toasts() {
  const { toasts, dismiss } = useToasts();
  if (toasts.length === 0) return null;
  return (
    <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
      {toasts.map((t) => (
        <div
          key={t.id}
          className={`flex items-center gap-3 rounded px-3 py-2 text-sm shadow-lg ${kindClass[t.kind]}`}
          role="status"
        >
          <span>{t.message}</span>
          <button type="button" className="opacity-70 hover:opacity-100" onClick={() => dismiss(t.id)}>
            &times;
          </button>
        </div>
      ))}
    </div>
  );
}
