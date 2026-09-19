// The effective ref (branch/tag) a git-sourced service is read at, with the
// "Change…" control that opens RefPopover (PLAN §34f item 2). Rendered next
// to BindingPanel's team source label; renders nothing for a non-git source
// (a local source has no ref to override). A bound local checkout's own
// branch is a different thing entirely -- BindingPanel/checkoutLabel show
// that as read-only already -- this control is always about the *team*
// source's ref, whichever mode this machine currently reads from.
import { useState } from 'react';
import { serviceRefs } from '../../api/client';
import { pushToast } from '../../state/toast';
import { buttonCls } from './BindingPanel';
import { RefPopover } from './RefPopover';
import type { ServiceRefOverride, Source } from '../../api/types';

export function ServiceRefControl({
  serviceId,
  team,
  refOverride,
  onChanged,
}: {
  serviceId: string;
  team?: Source;
  refOverride?: ServiceRefOverride;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  if (!team || team.type !== 'git') return null;

  const effective = refOverride?.ref || team.ref || 'default branch';

  const reset = async () => {
    setBusy(true);
    try {
      await serviceRefs.clear(serviceId);
      pushToast('success', `${serviceId}: reset to the team ref.`);
      onChanged();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Reset failed.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <span className="relative inline-flex flex-wrap items-center gap-2">
      <span className="font-mono text-xs">{effective}</span>
      {refOverride?.scope === 'local' && (
        <span
          title="A per-machine override of the team ref, from sapien.workspace.local.yaml"
          className="inline-block rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-700 dark:bg-slate-800 dark:text-slate-300"
        >
          this machine: {refOverride.ref}
        </span>
      )}
      <button type="button" onClick={() => setOpen(true)} className={buttonCls}>
        Change…
      </button>
      {refOverride?.scope === 'local' && (
        <button type="button" disabled={busy} onClick={reset} className={buttonCls}>
          {busy ? 'Resetting…' : 'Reset to team ref'}
        </button>
      )}
      {open && (
        <RefPopover
          serviceId={serviceId}
          currentRef={effective}
          onApplied={() => {
            setOpen(false);
            onChanged();
          }}
          onClose={() => setOpen(false)}
        />
      )}
    </span>
  );
}
