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
