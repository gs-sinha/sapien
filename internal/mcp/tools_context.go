package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/domain"
)

// GetContextInput is get_context's arguments (PLAN §14).
type GetContextInput struct {
	Intent     string   `json:"intent" jsonschema:"natural-language description of the authoring task"`
	Operations []string `json:"operations,omitempty" jsonschema:"operation ids to seed the bundle with, skipping search"`
	Budget     int      `json:"budget,omitempty" jsonschema:"token budget; default 8000"`
}

// getContext is the recommended first call for any authoring task
// (PLAN §23, §14). Its permission class is the union of the read classes
// ("read_*" in the table): ReadContracts gates the call, and the memories,
// flows, and runs sections of the bundle are stripped if the client lacks
// the matching finer-grained permission.
func (s *server) getContext(ctx context.Context, req *sdkmcp.CallToolRequest, in GetContextInput) (*sdkmcp.CallToolResult, any, error) {
	_, perm, denied := s.checkPermission(req.Session, classReadContracts)
	if denied != nil {
		return denied, nil, nil
	}
	budget := in.Budget
	if budget <= 0 {
		budget = 8000
	}
	bundle, err := s.engine().Context().Build(ctx, domain.ContextRequest{
		Intent:       in.Intent,
		Operations:   in.Operations,
		BudgetTokens: budget,
	})
	if err != nil {
		return errResult(err), nil, nil
	}
	if !perm.ReadMemories {
		bundle.Memories = nil
	}
	if !perm.ReadFlows {
		bundle.Flows = nil
	}
	if !perm.ReadRuns {
		bundle.Runs = nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "context for %q (%d operations, %d docs, %d memories, %d flows, %d runs, ~%d tokens)\n",
		bundle.Intent, len(bundle.Operations), len(bundle.Docs), len(bundle.Memories), len(bundle.Flows), len(bundle.Runs), bundle.EstimatedTokens)
	for _, op := range bundle.Operations {
		fmt.Fprintf(&b, "- %s %s %s — %s\n", op.Method, op.Path, op.ID, op.Summary)
	}
	for _, d := range bundle.Docs {
		fmt.Fprintf(&b, "- doc %s/%s#%s\n", d.Service, d.Path, d.Heading)
	}
	for _, m := range bundle.Memories {
		fmt.Fprintf(&b, "- memory[%s] %s: %s\n", m.Type, m.ID, m.Text)
	}
	for _, f := range bundle.Flows {
		fmt.Fprintf(&b, "- flow %s: %s\n", f.ID, strings.Join(f.Steps, ", "))
	}
	return result(b.String(), bundle), nil, nil
}
