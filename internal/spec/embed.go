package spec

import "embed"

// schemaFS embeds a copy of the JSON Schemas kept under the repository's
// spec/ directory. go:embed cannot reach outside this package's directory
// (patterns may not contain ".."), so schemas/*.schema.json here must stay
// byte-identical to spec/*.schema.json; TestSchemasSyncedWithSpecDir enforces
// that.
//
//go:embed schemas/*.schema.json
var schemaFS embed.FS

// Schema returns the raw JSON Schema (draft 2020-12) document for kind. The
// MCP layer uses this to serve, e.g., flow.schema.json to hosts that read
// sapien://reference/flow.schema.json.
//
// Schema panics if kind is not one of the package's Kind constants, since
// that indicates a programming error rather than a runtime condition.
func Schema(kind Kind) []byte {
	b, err := schemaFS.ReadFile("schemas/" + kind.fileName())
	if err != nil {
		panic("spec: unknown kind " + string(kind))
	}
	return b
}
