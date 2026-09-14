package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// WorkspaceSwitcher is what list_workspaces and switch_workspace act on: a
// directory of workspaces, and a way to get one's engine. *workspaces.Manager
// implements it for the daemon; internal/cli implements it over HTTP for a
// stdio server bridging to that daemon.
//
// Switching is a session-level mode change, deliberately not a per-tool
// argument: every other tool's schema stays free of a workspace field, and an
// agent cannot half-switch by forgetting it on one call in ten.
type WorkspaceSwitcher interface {
	List() []workspaces.Info
	Engine(dir string) (engine.Engine, error)
}

// ListWorkspacesInput is list_workspaces' (empty) arguments.
type ListWorkspacesInput struct{}

// ListWorkspacesOutput is list_workspaces' structured output.
type ListWorkspacesOutput struct {
	Current    string            `json:"current"`
	Workspaces []workspaces.Info `json:"workspaces"`
}

func (s *server) listWorkspaces(ctx context.Context, req *sdkmcp.CallToolRequest, in ListWorkspacesInput) (*sdkmcp.CallToolResult, any, error) {
	if s.switcher == nil {
		return errResult(errNoSwitcher()), nil, nil
	}

	current := s.workspaceDir()
	list := s.switcher.List()

	var b strings.Builder
	for _, w := range list {
		marker := " "
		if w.Dir == current {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s %s  %s", marker, w.Name, w.Dir)
		switch {
		case w.Error != "":
			fmt.Fprintf(&b, "  (unavailable: %s)", w.Error)
		case w.Services > 0:
			fmt.Fprintf(&b, "  (%d services)", w.Services)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		b.WriteString("no workspaces registered\n")
	}
	b.WriteString("* is the current workspace; switch_workspace(dir) changes it for this session.\n")

	return result(b.String(), ListWorkspacesOutput{Current: current, Workspaces: list}), nil, nil
}

// SwitchWorkspaceInput is switch_workspace's arguments.
type SwitchWorkspaceInput struct {
	Dir string `json:"dir" jsonschema:"workspace directory, or its name as reported by list_workspaces"`
}

// SwitchWorkspaceOutput is switch_workspace's structured output.
type SwitchWorkspaceOutput struct {
	Workspace workspaces.Info `json:"workspace"`
	Previous  string          `json:"previous,omitempty"`
}

func (s *server) switchWorkspace(ctx context.Context, req *sdkmcp.CallToolRequest, in SwitchWorkspaceInput) (*sdkmcp.CallToolResult, any, error) {
	if s.switcher == nil {
		return errResult(errNoSwitcher()), nil, nil
	}
	if strings.TrimSpace(in.Dir) == "" {
		return errResult(errs.New(errs.Invalid, "switch_workspace: dir is required").
			WithHint("call list_workspaces to see the choices")), nil, nil
	}

	dir := s.resolveWorkspaceRef(in.Dir)
	previous := s.workspaceDir()

	eng, err := s.switcher.Engine(dir)
	if err != nil {
		// A failed switch leaves the session where it was, and the agent
		// must hear that in the same breath as the failure: the first
		// friction report filed against Sapien was a create_memory that
		// landed in the wrong workspace after a switch had failed.
		e := errs.As(err)
		e.Message = fmt.Sprintf("switch to %s failed: %s; this session is still bound to %s and every tool keeps acting there", dir, e.Message, previous)
		e.WithDetail("still_bound_to", previous)
		return errResult(e), nil, nil
	}
	s.bind(eng)

	info := workspaces.Info{Dir: dir, Open: true}
	if ws := eng.Workspace(); ws != nil {
		info.Dir, info.Name, info.Services = ws.Dir, ws.Name, len(ws.Services)
	}

	text := fmt.Sprintf("switched to workspace %s (%s); %d services\n", info.Name, info.Dir, info.Services)
	if previous != "" && previous != info.Dir {
		text += fmt.Sprintf("previous workspace was %s\n", previous)
	}
	text += "Every tool now acts on this workspace. Catalog, flows, memories and runs are per-workspace, so search results and ids from before the switch do not apply here.\n"

	return result(text, SwitchWorkspaceOutput{Workspace: info, Previous: previous}), nil, nil
}

// resolveWorkspaceRef accepts either a directory or a workspace name as
// reported by list_workspaces, so an agent can pass back what it was shown
// without reconstructing a path.
func (s *server) resolveWorkspaceRef(ref string) string {
	for _, w := range s.switcher.List() {
		if w.Dir == ref {
			return w.Dir
		}
	}
	for _, w := range s.switcher.List() {
		if w.Name == ref {
			return w.Dir
		}
	}
	return ref
}

func errNoSwitcher() error {
	return errs.New(errs.Invalid, "this server is bound to a single workspace and cannot switch").
		WithHint("start it through `sapien mcp` against a daemon that serves multiple workspaces")
}
