package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// This file implements the example tools (PLAN §34b): list_examples,
// get_example, create_example, rescope_example, delete_example. All five
// are backed by engine.ExampleAPI (s.engine().Examples()); the fake in
// fake_engine_test.go is a complete in-memory implementation, so every test
// here exercises the real tool/permission/merge logic end to end.

// --- list_examples -----------------------------------------------------

// ListExamplesInput is list_examples' arguments.
type ListExamplesInput struct {
	Operation string `json:"operation,omitempty" jsonschema:"restrict to examples of this operation id"`
	Service   string `json:"service,omitempty" jsonschema:"restrict to examples of this service"`
	Tag       string `json:"tag,omitempty" jsonschema:"restrict to examples carrying this tag"`
	Text      string `json:"text,omitempty" jsonschema:"substring match over id, description, tags, and folder"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum number of results; default 20"`
	// Folder restricts to that folder and everything below it (PLAN §34f
	// item 4).
	Folder string `json:"folder,omitempty" jsonschema:"restrict to this folder and everything below it"`
}

// ExampleListItem is one row of list_examples' output.
type ExampleListItem struct {
	ID          string `json:"id"`
	Operation   string `json:"operation"`
	Scope       string `json:"scope"`
	Verified    bool   `json:"verified"`
	Env         string `json:"env,omitempty"`
	Description string `json:"description,omitempty"`
	// Folder is the subfolder of the scope's examples directory the
	// example sits in (PLAN §34f item 6); "" at the root.
	Folder string `json:"folder,omitempty"`
	// Tier is where the example's file sits (PLAN §7b): local, workspace,
	// or service. Shipped is the workspace tier's ship state; both empty
	// for a scope this fake/engine hasn't tiered yet.
	Tier    string `json:"tier,omitempty"`
	Shipped string `json:"shipped,omitempty"`
}

// ListExamplesOutput is list_examples' structured output.
type ListExamplesOutput struct {
	Examples []ExampleListItem `json:"examples"`
}

func exampleListItem(ex domain.SavedExample) ExampleListItem {
	item := ExampleListItem{
		ID: ex.ID, Operation: ex.Operation, Scope: string(ex.Scope),
		Verified: ex.Verified != nil, Description: ex.Description,
		Folder: ex.Folder, Tier: ex.Tier, Shipped: ex.Shipped,
	}
	if ex.Verified != nil {
		item.Env = ex.Verified.Env
	}
	return item
}

func (s *server) listExamples(ctx context.Context, req *sdkmcp.CallToolRequest, in ListExamplesInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	results, err := s.engine().Examples().List(ctx, domain.ExampleQuery{
		Operation: in.Operation, Service: in.Service, Tag: in.Tag, Text: in.Text, Limit: limit, Folder: in.Folder,
	})
	if err != nil {
		return errResult(err), nil, nil
	}
	out := ListExamplesOutput{}
	var b strings.Builder
	for _, ex := range results {
		item := exampleListItem(ex)
		out.Examples = append(out.Examples, item)
		verified := "unverified"
		if item.Verified {
			verified = "verified/" + item.Env
		}
		folderNote := ""
		if item.Folder != "" {
			folderNote = " folder:" + item.Folder
		}
		fmt.Fprintf(&b, "- %s [%s] %s scope%s (%s): %s%s\n",
			ex.ID, ex.Operation, item.Scope, folderNote, verified, ex.Description, tierShipSuffix(ex.Tier, ex.Shipped))
	}
	if len(out.Examples) == 0 {
		b.WriteString("no matches\n")
	}
	return result(b.String(), out), nil, nil
}

// --- get_example -----------------------------------------------------

// GetExampleInput is get_example's arguments.
type GetExampleInput struct {
	ID string `json:"id" jsonschema:"example id, as returned by list_examples"`
}

// GetExampleOutput is get_example's structured output.
type GetExampleOutput struct {
	Example domain.SavedExample `json:"example"`
	YAML    string              `json:"yaml"`
}

func (s *server) getExample(ctx context.Context, req *sdkmcp.CallToolRequest, in GetExampleInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	ex, err := s.engine().Examples().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	y, err := yaml.Marshal(ex)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := GetExampleOutput{Example: *ex, YAML: string(y)}
	return result(string(y), out), nil, nil
}

// --- create_example -----------------------------------------------------

// CreateExampleInput is create_example's arguments. Exactly one of RunID or
// Operation must be given: RunID (+ optional StepID) saves a verified
// example from a recorded run via ExampleAPI.FromRun; Operation (with
// optional Input/Body/Headers) saves a hand-written one via
// ExampleAPI.Create. There is deliberately no Verified field here: PLAN
// §34b requires that only a save-from-run path can ever set it.
type CreateExampleInput struct {
	ID string `json:"id" jsonschema:"example id (file stem)"`

	RunID  string `json:"run_id,omitempty" jsonschema:"run id to save a verified example from; mutually exclusive with operation"`
	StepID string `json:"step_id,omitempty" jsonschema:"step within run_id; default the run's only (or first) step"`

	Operation string            `json:"operation,omitempty" jsonschema:"operation id for a hand-written example; mutually exclusive with run_id. verified cannot be set by hand"`
	Input     map[string]any    `json:"input,omitempty" jsonschema:"path/query/header parameter values by name (hand-written only)"`
	Body      any               `json:"body,omitempty" jsonschema:"request body (hand-written only)"`
	Headers   map[string]string `json:"headers,omitempty" jsonschema:"extra request headers (hand-written only)"`

	Description string   `json:"description,omitempty" jsonschema:"what makes this example useful"`
	Scope       string   `json:"scope,omitempty" jsonschema:"workspace|service; default workspace. Scope decides storage and sharing, not subject, exactly as for create_memory: service is committed in <service>/api/examples and shared with everyone who clones it; workspace is local to this workspace unless the workspace itself is a git repo."`
	Tags        []string `json:"tags,omitempty" jsonschema:"free-form tags"`
	// Folder places the example in a subfolder of the scope's examples
	// directory (PLAN §34f item 6); "" (the default) is the root.
	Folder string `json:"folder,omitempty" jsonschema:"subfolder of the scope's examples directory to save into; default the root"`
}

// CreateExampleOutput is create_example's structured output.
type CreateExampleOutput struct {
	Example domain.SavedExample `json:"example"`
}

// createExample records source: {kind: agent, client: <name>} for the
// FromRun path, per PLAN §23/§24 (the same attribution create_memory uses).
func (s *server) createExample(ctx context.Context, req *sdkmcp.CallToolRequest, in CreateExampleInput) (*sdkmcp.CallToolResult, any, error) {
	client, _, denied := s.checkPermission(req.Session, classWriteExamples)
	if denied != nil {
		return denied, nil, nil
	}

	if in.ID == "" {
		return errResult(errs.New(errs.Invalid, "create_example: id is required")), nil, nil
	}
	haveRun, haveOp := in.RunID != "", in.Operation != ""
	if haveRun == haveOp {
		return errResult(errs.New(errs.Invalid, "create_example: give exactly one of run_id or operation, not both or neither")), nil, nil
	}
	if haveRun && in.Folder != "" {
		return errResult(errs.New(errs.Invalid, "create_example: folder is not supported with run_id").
			WithHint("create the example first, then call rescope_example(id, folder=...) to place it")), nil, nil
	}

	scope := domain.ExampleScope(in.Scope)
	if scope == "" {
		scope = domain.ExampleScopeWorkspace
	}

	var created *domain.SavedExample
	var err error
	if haveRun {
		created, err = s.engine().Examples().FromRun(ctx, engine.ExampleFromRun{
			RunID: in.RunID, StepID: in.StepID, ID: in.ID, Description: in.Description,
			Scope: scope, Tags: in.Tags, Source: &domain.MemorySource{Kind: "agent", Client: client},
		})
	} else {
		created, err = s.engine().Examples().Create(ctx, domain.SavedExample{
			ID: in.ID, Operation: in.Operation, Description: in.Description, Scope: scope,
			Input: in.Input, Body: in.Body, Headers: in.Headers, Tags: in.Tags, Folder: in.Folder,
		})
	}
	if err != nil {
		return errResult(err), nil, nil
	}

	out := CreateExampleOutput{Example: *created}
	verified := "unverified (hand-written)"
	if created.Verified != nil {
		verified = fmt.Sprintf("verified against %s (run %s)", created.Verified.Env, created.Verified.RunID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "created example %s for %s (%s scope, %s) in workspace %s\nstored: %s\n",
		created.ID, created.Operation, created.Scope, verified, s.workspaceName(), examplePathText(created.Path))
	if line := tierLandedLine("rescope_example", created.Tier); line != "" {
		fmt.Fprintf(&b, "%s\n", line)
	}
	fmt.Fprintf(&b, "use it with execute_api(example=%q) or a flow step `example: %s`\n", created.ID, created.ID)
	return result(b.String(), out), nil, nil
}

// examplePathText renders a SavedExample.Path for tool output, falling back
// to an honest placeholder when the engine didn't report one rather than
// guessing a file layout this package doesn't own.
func examplePathText(path string) string {
	if path == "" {
		return "(path not reported)"
	}
	return path
}

// --- rescope_example -----------------------------------------------------

// RescopeExampleInput is rescope_example's arguments. Scope used to be
// required; it is optional now (PLAN §34f item 6) so a call can change only
// Folder (or Tier) and leave the example's scope exactly where it is --
// existing callers that always pass scope see no change in behavior.
type RescopeExampleInput struct {
	ID    string `json:"id" jsonschema:"example id"`
	Scope string `json:"scope,omitempty" jsonschema:"workspace|service; omit to keep the current scope and only change tier and/or folder"`
	// Tier is applied after the scope change, via Examples().Move, so one
	// call can both rescope and place the file in a tier (PLAN §7b).
	// Meaningful only when the example ends up at workspace scope.
	Tier string `json:"tier,omitempty" jsonschema:"local|workspace; optional: also move the file to this tier, after the scope change"`
	// Folder is applied last, via Examples().MoveFolder, so one call can
	// rescope, retier, and change folder together (PLAN §34f item 6). A
	// pointer so "move to the root" (an explicit "") can be told apart from
	// "leave the folder alone" (the key absent).
	Folder *string `json:"folder,omitempty" jsonschema:"also move the file to this folder within its (new or current) directory, after the scope/tier change; \"\" moves it to the root"`
}

// RescopeExampleOutput is rescope_example's structured output.
type RescopeExampleOutput struct {
	Example domain.SavedExample `json:"example"`
}

func (s *server) rescopeExample(ctx context.Context, req *sdkmcp.CallToolRequest, in RescopeExampleInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteExamples); denied != nil {
		return denied, nil, nil
	}
	if strings.TrimSpace(in.Scope) == "" && in.Tier == "" && in.Folder == nil {
		return errResult(errs.New(errs.Invalid, "rescope_example requires scope, tier, or folder")), nil, nil
	}

	ex, err := s.engine().Examples().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}

	oldPath := ex.Path
	moved := ex
	text := ""
	if strings.TrimSpace(in.Scope) != "" {
		updated := *ex
		updated.Scope = domain.ExampleScope(in.Scope)
		moved, err = s.engine().Examples().Update(ctx, updated)
		if err != nil {
			return errResult(err), nil, nil
		}
		text += fmt.Sprintf("rescoped example %s to %s scope (%s -> %s)\n",
			moved.ID, moved.Scope, examplePathText(oldPath), examplePathText(moved.Path))
	}

	if in.Tier != "" {
		moved, err = s.engine().Examples().Move(ctx, moved.ID, in.Tier)
		if err != nil {
			return errResult(err), nil, nil
		}
		text += fmt.Sprintf("moved to the %s tier: %s\n", in.Tier, examplePathText(moved.Path))
	}
	if in.Folder != nil {
		moved, err = s.engine().Examples().MoveFolder(ctx, moved.ID, *in.Folder)
		if err != nil {
			return errResult(err), nil, nil
		}
		text += fmt.Sprintf("moved to folder %q: %s\n", moved.Folder, examplePathText(moved.Path))
	}

	out := RescopeExampleOutput{Example: *moved}
	return result(text, out), nil, nil
}

// --- delete_example -----------------------------------------------------

// DeleteExampleInput is delete_example's arguments.
type DeleteExampleInput struct {
	ID string `json:"id" jsonschema:"example id"`
}

// DeleteExampleOutput is delete_example's structured output.
type DeleteExampleOutput struct {
	ID string `json:"id"`
}

func (s *server) deleteExample(ctx context.Context, req *sdkmcp.CallToolRequest, in DeleteExampleInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteExamples); denied != nil {
		return denied, nil, nil
	}
	if err := s.engine().Examples().Delete(ctx, in.ID); err != nil {
		return errResult(err), nil, nil
	}
	out := DeleteExampleOutput{ID: in.ID}
	return result(fmt.Sprintf("deleted example %s\n", in.ID), out), nil, nil
}

// --- commit_example -----------------------------------------------------
//
// commit_example is commit_memory's twin for saved examples (PLAN §7b):
// the standalone way to record a workspace-tier example's file in the
// workspace repository over MCP. Never pushes; there is no push tool over
// MCP, since pushing what an agent wrote is the human's call.

// CommitExampleInput is commit_example's arguments.
type CommitExampleInput struct {
	ID string `json:"id" jsonschema:"example id; must already be at the workspace tier"`
	// Message overrides the engine's own default.
	Message string `json:"message,omitempty" jsonschema:"commit message; default depends on whether the file was ever added to git"`
}

// CommitExampleOutput is commit_example's structured output.
type CommitExampleOutput struct {
	Example domain.SavedExample `json:"example"`
}

// commitExample commits a workspace-tier example's file in the workspace
// repository: one commit of that file, never a push. Refused by the
// engine for any other tier, a workspace not in git, or a file with
// nothing to commit.
func (s *server) commitExample(ctx context.Context, req *sdkmcp.CallToolRequest, in CommitExampleInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteExamples); denied != nil {
		return denied, nil, nil
	}
	ex, err := s.engine().Examples().Commit(ctx, in.ID, in.Message)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := CommitExampleOutput{Example: *ex}
	text := fmt.Sprintf("committed %s; not pushed\n", examplePathText(ex.Path))
	return result(text, out), nil, nil
}
