package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/friction"
)

// reportFrictionDescription is report_friction's tool description. It is a
// named constant so the text an agent reads and the behaviour it promises
// live in one file with the handler that has to keep the promise.
const reportFrictionDescription = "File friction about Sapien itself: a tool returned the wrong shape, a capability was missing, documentation misled you, or a workflow needed a workaround. " +
	"The report is queued on this machine for a human to review (sapien friction list/show) and is never posted automatically; if they agree it is published as a GitHub Discussion on the Sapien repo, so it will be public: never include secrets, hostnames, request or response payloads, or anything from the user's data. " +
	"Say what you tried, what happened, and what would have helped. Do not file the same friction twice in one session."

// ReportFrictionInput is report_friction's arguments.
type ReportFrictionInput struct {
	Title     string `json:"title" jsonschema:"one line: what was hard"`
	Happened  string `json:"what_happened" jsonschema:"what happened instead of what you expected; include the tool name and the shape of the response, never secrets, hostnames or payloads"`
	Tried     string `json:"what_i_tried,omitempty" jsonschema:"what you were trying to do"`
	WouldHelp string `json:"what_would_help,omitempty" jsonschema:"the change that would have removed the friction"`
	Tool      string `json:"tool,omitempty" jsonschema:"the Sapien tool or command involved"`
	Category  string `json:"category,omitempty" jsonschema:"bug|idea|docs|missing (default bug)"`
}

// ReportFrictionOutput is report_friction's structured output.
type ReportFrictionOutput struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

// reportFriction queues a friction report for human review. There is
// deliberately no permission class on it: it writes one Markdown file
// under the user's own ~/.sapien/friction and nothing else, and refusing
// feedback is the one thing this tool must never do. Publishing is a
// separate, human-driven step (internal/cli/friction.go).
func (s *server) reportFriction(ctx context.Context, req *sdkmcp.CallToolRequest, in ReportFrictionInput) (*sdkmcp.CallToolResult, any, error) {
	r := friction.Report{
		Title:     in.Title,
		Category:  friction.Category(in.Category),
		Tool:      in.Tool,
		Tried:     in.Tried,
		Happened:  in.Happened,
		WouldHelp: in.WouldHelp,
		Client:    clientName(req.Session),
		Version:   s.version,
	}
	if eng := s.engine(); eng != nil {
		if ws := eng.Workspace(); ws != nil {
			r.Workspace = ws.Name
		}
	}

	created, err := friction.New("").Create(ctx, r)
	if err != nil {
		return errResult(err), nil, nil
	}

	out := ReportFrictionOutput{ID: created.ID, Path: created.Path, Status: created.Status}
	text := fmt.Sprintf("queued friction report %s; nothing was sent. A human reviews it with `sapien friction show %s` and posts it with `sapien friction send %s`.\n",
		created.ID, created.ID, created.ID)
	return result(text, out), nil, nil
}
