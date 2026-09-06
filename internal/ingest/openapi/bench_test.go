package openapi

import (
	"fmt"
	"strings"
	"testing"
)

// generateLargeSpec builds an in-memory OpenAPI 3.0 document with n operations
// (one GET per path, each returning a small object body), used as a benchmark guard
// for Ingest's performance on a realistically large catalog.
func generateLargeSpec(t *testing.T, n int) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString("openapi: 3.0.3\n")
	b.WriteString("info:\n  title: Bench\n  version: \"1.0.0\"\n")
	b.WriteString("paths:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  /v1/items/%d:\n", i)
		fmt.Fprintf(&b, "    get:\n")
		fmt.Fprintf(&b, "      operationId: getItem%d\n", i)
		fmt.Fprintf(&b, "      summary: Get item %d\n", i)
		b.WriteString("      responses:\n")
		b.WriteString("        \"200\":\n")
		b.WriteString("          description: OK\n")
		b.WriteString("          content:\n")
		b.WriteString("            application/json:\n")
		b.WriteString("              schema:\n")
		b.WriteString("                type: object\n")
		b.WriteString("                properties:\n")
		b.WriteString("                  id:\n")
		b.WriteString("                    type: string\n")
		b.WriteString("                  count:\n")
		b.WriteString("                    type: integer\n")
	}
	return []byte(b.String())
}
