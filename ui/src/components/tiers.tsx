// Shared local -> committed -> pushed controls for anything that tiers:
// flows (pages/flows/tier.tsx re-exports ShippedBadge/CommitButton/PushButton
// from here so its own flow-specific TierBadge/tierOf/tierLabel keep living
// alongside them), memories, and examples. Defined once so every item kind
// and the status bar's repo segment all show the same badges and buttons.
import { useState } from 'react';
import { repo as repoApi } from '../api/client';
import { useRepo } from '../state/repo';
import { pushToast } from '../state/toast';
import type { ItemTier, ShipStatus } from '../api/types';
import { plural } from '../lib/plural';

export const ITEM_TIER_NAMES: Record<ItemTier, string> = { local: 'local', workspace: 'team', service: 'service' };

const itemTierTitles: Record<ItemTier, string> = {
  local: 'This machine only, until moved to the team',
  workspace: "The team's workspace repo",
  service: "The owning service's repo",
};

// The memory/example Tier badge: blank for a personal-scope memory (or any
// item with no tier at all), otherwise local/team/service.
export function ItemTierBadge({ tier }: { tier?: ItemTier }) {
  if (!tier) return null;
  return (
    <span
      title={itemTierTitles[tier]}
      className="inline-block rounded-full bg-slate-100 px-2 py-0.5 font-mono text-xs text-slate-700 dark:bg-slate-800 dark:text-slate-300"
    >
      {ITEM_TIER_NAMES[tier]}
    </span>
  );
}

// How far a workspace-tier item's file has travelled toward the team, from
// a read-only git status of the workspace repo: untracked (never added),
// modified (tracked, uncommitted changes), unpushed (committed, not on the
// remote yet), shipped (committed and on the upstream). Shared by flows,
// memories, and examples.
const shippedMeta: Record<ShipStatus, { label: string; cls: string }> = {
  untracked: { label: 'not committed', cls: 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300' },
  modified: { label: 'modified', cls: 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300' },
  unpushed: { label: 'committed, not pushed', cls: 'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-300' },
  shipped: { label: 'shipped', cls: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300' },
};

// Renders nothing for an absent/unknown value: local and service tiers, a
// personal memory, and a workspace not under git, all carry no `shipped`.
export function ShippedBadge({ shipped }: { shipped?: ShipStatus }) {
  if (!shipped) return null;
  const meta = shippedMeta[shipped];
  if (!meta) return null;
  return (
    <span
      title="How far this file has travelled toward the team, from the workspace repo's git status"
      className={`inline-block rounded-full px-2 py-0.5 text-xs ${meta.cls}`}
    >
      {meta.label}
    </span>
  );
}

const buttonCls = 'rounded border border-slate-300 px-1.5 py-0.5 text-xs disabled:opacity-50 dark:border-slate-700';

// Only for the two states a commit actually fixes: untracked (never added)
// and modified (uncommitted changes). `onCommit` runs the item's own
// git-add-plus-commit call (flows.commit / memories.commit / examples.commit)
// on the file in the workspace repo -- never a push -- and the daemon
// refuses it outright for any other tier, workspace not in git, or nothing
// to commit, so those cases just don't get a button.
export function CommitButton({
  id,
  shipped,
  onCommit,
  onCommitted,
}: {
  id: string;
  shipped?: ShipStatus;
  onCommit: () => Promise<unknown>;
  onCommitted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  if (shipped !== 'untracked' && shipped !== 'modified') return null;

  const commit = async () => {
    setBusy(true);
    try {
      await onCommit();
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
      className={buttonCls}
    >
      {busy ? 'Committing…' : 'Commit'}
    </button>
  );
}

// Local <-> workspace only: memories and examples have no rescope-to-service
// endpoint the way flows do, so this offers nothing for the service tier
// (or for an absent tier, i.e. a personal memory).
export function MoveTierControl({
  tier,
  onMove,
  onMoved,
}: {
  tier?: ItemTier;
  onMove: (target: 'local' | 'workspace') => Promise<unknown>;
  onMoved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  if (tier !== 'local' && tier !== 'workspace') return null;

  const target: 'local' | 'workspace' = tier === 'workspace' ? 'local' : 'workspace';
  const label = tier === 'workspace' ? 'Move to local' : 'Move to team';

  const move = async () => {
    setBusy(true);
    try {
      await onMove(target);
      pushToast('success', `moved to ${ITEM_TIER_NAMES[target]}`);
      onMoved();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Move failed.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <button type="button" disabled={busy} onClick={move} className={buttonCls}>
      {busy ? 'Moving…' : label}
    </button>
  );
}

// Pushing ships every unpushed commit on the workspace repo's current
// branch at once -- it's per branch, not per file -- so the same button
// shows up next to any `unpushed` ShippedBadge (flows/memories/examples)
// and in the status bar. `onPushed` lets a page reload its own list; the
// repo store update this always does is what keeps every other PushButton
// and the status bar in sync without it.
export function PushButton({ onPushed }: { onPushed?: () => void }) {
  const ahead = useRepo((s) => s.status?.ahead);
  const [busy, setBusy] = useState(false);

  const push = async () => {
    setBusy(true);
    try {
      const next = await repoApi.push();
      useRepo.getState().setStatus(next);
      pushToast('success', `pushed ${plural(next.pushed_count ?? 0, 'commit')}`);
      onPushed?.();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Push failed.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <button
      type="button"
      disabled={busy}
      onClick={push}
      title="pushes every unpushed commit in the workspace repository"
      className={buttonCls}
    >
      {busy ? 'Pushing…' : ahead ? `Push ${plural(ahead, 'commit')}` : 'Push'}
    </button>
  );
}
