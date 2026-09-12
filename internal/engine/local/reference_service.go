package local

import _ "embed"

// serviceReferenceText is served by Reference("service"): how a service's
// api/ package must be laid out (contract, service.yaml, docs) so Sapien
// can index it, and how an agent registers it. It is the text an MCP host
// receives from get_dsl_reference("service") when a user asks to onboard a
// service, so it is kept as a standalone Markdown file that humans can
// read too.
//
//go:embed reference_service.md
var serviceReferenceText string

// sapienReferenceText is served by Reference("sapien"): what Sapien is and
// what an agent can do with it -- the orientation an agent needs before the
// per-format references make sense. Agents were reading schemas out of
// Sapien and then calling services from throwaway scripts, and onboarding
// agents were writing docs for a reviewer of the repo rather than for the
// agent in another repo who is the actual reader; both are failures of
// framing that no amount of per-tool description fixes.
//
//go:embed reference_sapien.md
var sapienReferenceText string
