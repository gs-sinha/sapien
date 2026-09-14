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
	Text      string `json:"text,omitempty" jsonschema:"substring match over id, description, and tags"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum number of results; default 20"`
}

// ExampleListItem is one row of list_examples' output.
type ExampleListItem struct {
	ID          string `json:"id"`
	Operation   string `json:"operation"`
	Scope       string `json:"scope"`
	Verified    bool   `json:"verified"`
	Env         string `json:"env,omitempty"`
	Description string `json:"description,omitempty"`
}

// ListExamplesOutput is list_examples' structured output.
type ListExamplesOutput struct {
	Examples []ExampleListItem `json:"examples"`
}

func exampleListItem(ex domain.SavedExample) ExampleListItem {
	item := ExampleListItem{
		ID: ex.ID, Operation: ex.Operation, Scope: string(ex.Scope),
		Verified: ex.Verified != nil, Description: ex.Description,
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
		Operation: in.Operation, Service: in.Service, Tag: in.Tag, Text: in.Text, Limit: limit,
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
		fmt.Fprintf(&b, "- %s [%s] %s scope (%s): %s\n", ex.ID, ex.Operation, item.Scope, verified, ex.Description)
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
			Input: in.Input, Body: in.Body, Headers: in.Headers, Tags: in.Tags,
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
	fmt.Fprintf(&b, "created example %s for %s (%s scope, %s) in workspace %s\nstored: %s\nuse it with execute_api(example=%q) or a flow step `example: %s`\n",
		created.ID, created.Operation, created.Scope, verified, s.workspaceName(), examplePathText(created.Path), created.ID, created.ID)
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

// RescopeExampleInput is rescope_example's arguments.
type RescopeExampleInput struct {
	ID    string `json:"id" jsonschema:"example id"`
	Scope string `json:"scope" jsonschema:"workspace|service"`
}

// RescopeExampleOutput is rescope_example's structured output.
type RescopeExampleOutput struct {
	Example domain.SavedExample `json:"example"`
}

func (s *server) rescopeExample(ctx context.Context, req *sdkmcp.CallToolRequest, in RescopeExampleInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteExamples); denied != nil {
		return denied, nil, nil
	}

	ex, err := s.engine().Examples().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}

	updated := *ex
	oldPath := ex.Path
	updated.Scope = domain.ExampleScope(in.Scope)

	moved, err := s.engine().Examples().Update(ctx, updated)
	if err != nil {
		return errResult(err), nil, nil
	}

	out := RescopeExampleOutput{Example: *moved}
	text := fmt.Sprintf("rescoped example %s to %s scope (%s -> %s)\n",
		moved.ID, moved.Scope, examplePathText(oldPath), examplePathText(moved.Path))
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
