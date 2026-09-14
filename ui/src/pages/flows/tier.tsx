// The flow tier ladder as the UI names it: local (this machine only,
// <workspace>/local/flows) -> team (the workspace's git repo,
// <workspace>/flows) -> service (the owning repo's api/flows). The wire
// value is domain.FlowOwnerLocal/Workspace/Service; "team" is the label for
// the workspace tier because that is who reads it.
import type { FlowOwnerKind } from '../../api/types';

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
