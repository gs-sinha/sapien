// The flow tier ladder as the UI names it: local (this machine only,
// <workspace>/local/flows) -> team (the workspace's git repo,
// <workspace>/flows) -> service (the owning repo's api/flows). The wire
// value is domain.FlowOwnerLocal/Workspace/Service; "team" is the label for
// the workspace tier because that is who reads it.
import { useState } from 'react';
import { flows } from '../../api/client';
import { pushToast } from '../../state/toast';
import type { FlowOwnerKind, ShipStatus } from '../../api/types';

export const FLOW_TIERS: FlowOwnerKind[] = ['local', 'workspace', 'service'];

export const TIER_NAMES: Record<FlowOwnerKind, string> = { local: 'Local', workspace: 'Team', service: 'Service' };

export function isFlowOwnerKind(kind?: string): kind is FlowOwnerKind {
  return kind === 'local' || kind === 'workspace' || kind === 'service';
}

// tierOf maps a flow's owner_kind onto the ladder. Flows written before
// tiers existed carry "" and live in <workspace>/flows, so they read as the
// team tier.
export function tierOf(ownerKind?: string): FlowOwnerKind {
  return ownerKind === 'local' || ownerKind === 'service' ? ownerKind : 'workspace';
}

export function tierLabel(ownerKind?: string, ownerId?: string): string {
  const tier = tierOf(ownerKind);
  if (tier === 'local') return 'local';
  if (tier === 'service') return ownerId ? `service:${ownerId}` : 'service';
  return 'team';
}

const tierTitles: Record<FlowOwnerKind, string> = {
  local: 'This machine only (local/flows, gitignored)',
  workspace: "The team's workspace repo (flows/)",
  service: "The owning service's repo (api/flows)",
};

export function TierBadge({ ownerKind, ownerId }: { ownerKind?: string; ownerId?: string }) {
  return (
    <span
      title={tierTitles[tierOf(ownerKind)]}
      className="inline-block rounded-full bg-slate-100 px-2 py-0.5 font-mono text-xs text-slate-700 dark:bg-slate-800 dark:text-slate-300"
    >
      {tierLabel(ownerKind, ownerId)}
    </span>
  );
}

// How far a workspace-tier flow's file has travelled toward the team, from
// a read-only git status of the workspace repo (PLAN §7b): a promotion
// moves the file, but nobody reads it elsewhere until it's committed and
// pushed, so this says which of those still has to happen.
const shippedMeta: Record<ShipStatus, { label: string; cls: string }> = {
  untracked: { label: 'not committed', cls: 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300' },
  modified: { label: 'modified', cls: 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300' },
  unpushed: { label: 'committed, not pushed', cls: 'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-300' },
  shipped: { label: 'shipped', cls: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300' },
};

// Renders nothing for an absent/unknown value: local and service tiers, and
// a workspace not under git, carry no `shipped` at all.
export function ShippedBadge({ shipped }: { shipped?: ShipStatus }) {
  if (!shipped) return null;
  const meta = shippedMeta[shipped];
  if (!meta) return null;
  return (
    <span
      title="How far this flow's file has travelled toward the team, from the workspace repo's git status"
      className={`inline-block rounded-full px-2 py-0.5 text-xs ${meta.cls}`}
    >
      {meta.label}
    </span>
  );
}

// Only for the two states a commit actually fixes: untracked (never added)
// and modified (uncommitted changes). POST /v1/flows/{id}/commit runs `git
// add` + `git commit` on the file in the workspace repo -- never a push --
// and the daemon refuses it outright for any other tier, workspace not in
// git, or nothing to commit, so those cases just don't get a button.
export function CommitButton({ id, shipped, onCommitted }: { id: string; shipped?: ShipStatus; onCommitted: () => void }) {
  const [busy, setBusy] = useState(false);
  if (shipped !== 'untracked' && shipped !== 'modified') return null;

  const commit = async () => {
    setBusy(true);
    try {
      await flows.commit(id);
      pushToast('success', `committed ${id}; not pushed`);
      onCommitted();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Commit failed.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <button
      type="button"
      disabled={busy}
      onClick={commit}
      title="git add + git commit this file in the workspace repo (never pushes)"
      className="rounded border border-slate-300 px-1.5 py-0.5 text-xs disabled:opacity-50 dark:border-slate-700"
    >
      {busy ? 'Committing…' : 'Commit'}
    </button>
  );
}
