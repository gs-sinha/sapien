package domain

// Tiers for memories and examples (PLAN §7b), the same ladder flows climb
// through FlowSummary.OwnerKind: local is this machine's
// <workspace>/local/<kind>, ignored by git; workspace is the team's
// <workspace>/<kind>, committed with the workspace repository; service is
// the owning service's api/<kind>, written only when that service is
// bound to a local checkout. Personal memories have no tier: they live in
// SQLite only.
const (
	TierLocal     = "local"
	TierWorkspace = "workspace"
	TierService   = "service"
)
