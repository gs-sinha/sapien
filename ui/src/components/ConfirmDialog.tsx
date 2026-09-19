// A small confirm dialog for actions too consequential for a bare button
// (PLAN §34f item 3: "confirm dialog (NOT window.confirm; build an inline
// confirm or reuse an existing dialog component if one exists)"). Built on
// top of the existing Modal (components/run/Modal.tsx, so far only used by
// SaveExampleDialog) rather than a new backdrop implementation. Used by
// Settings' daemon Restart and update Upgrade actions.
import { Modal } from './run/Modal';
import type { ReactNode } from 'react';

export function ConfirmDialog({
  title,
  message,
  confirmLabel = 'Confirm',
  danger,
  busy,
  onConfirm,
  onClose,
}: {
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  /** Red confirm button, for a destructive-feeling action. */
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Modal title={title} onClose={onClose}>
      <div className="space-y-3 text-sm">
        <div className="text-slate-600 dark:text-slate-400">{message}</div>
        <div className="flex justify-end gap-2 pt-1">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="rounded border border-slate-300 px-3 py-1 text-xs disabled:opacity-50 dark:border-slate-700"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            className={`rounded px-3 py-1 text-xs text-white disabled:opacity-50 ${
              danger ? 'bg-red-600' : 'bg-slate-900 dark:bg-slate-100 dark:text-slate-900'
            }`}
          >
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </Modal>
  );
}
