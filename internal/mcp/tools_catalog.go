package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/example"
)

// --- list_services -----------------------------------------------------

// ListServicesInput has no arguments.
type ListServicesInput struct{}

// ServiceListItem is one row of list_services' output.
type ServiceListItem struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	OperationCount int    `json:"operation_count"`
}

// ListServicesOutput is list_services' structured output.
type ListServicesOutput struct {
	Services []ServiceListItem `json:"services"`
}

func (s *server) listServices(ctx context.Context, req *sdkmcp.CallToolRequest, _ ListServicesInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	services, err := s.engine().Services().List(ctx)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := ListServicesOutput{}
	var b strings.Builder
	b.WriteString("services:\n")
	for _, svc := range services {
		out.Services = append(out.Services, ServiceListItem{Name: svc.Name, Description: svc.Description, OperationCount: svc.OperationCount})
		fmt.Fprintf(&b, "- %s (%d ops): %s\n", svc.Name, svc.OperationCount, svc.Description)
	}
	return result(b.String(), out), nil, nil
}

// --- get_service ---------------------------------------------------------

// GetServiceInput is get_service's arguments.
type GetServiceInput struct {
	Name string `json:"name" jsonschema:"service name, as returned by list_services"`
}

// GetServiceOutput is get_service's structured output. Warnings holds only
// the warnings no accepted_warnings entry in service.yaml matched;
// AcceptedWarnings holds the ones that were, each with the reason it was
// accepted (domain.Service's own split -- see the "Warnings" section of
// reference_service.md).
type GetServiceOutput struct {
	Name             string                       `json:"name"`
	Description      string                       `json:"description,omitempty"`
	Owners           []string                     `json:"owners,omitempty"`
	Concepts         []string                     `json:"concepts,omitempty"`
	Tasks            []domain.Task                `json:"tasks,omitempty"`
	Environments     map[string]domain.EnvHint    `json:"environments,omitempty"`
	Docs             []string                     `json:"docs,omitempty"`
	OperationCount   int                          `json:"operation_count"`
	Coverage         *domain.DocCoverage          `json:"coverage,omitempty"`
	Warnings         []domain.LintWarning         `json:"warnings,omitempty"`
	AcceptedWarnings []domain.AcceptedLintWarning `json:"accepted_warnings,omitempty"`
}

func (s *server) getService(ctx context.Context, req *sdkmcp.CallToolRequest, in GetServiceInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	svc, err := s.engine().Services().Get(ctx, in.Name)
	if err != nil {
		return errResult(err), nil, nil
	}
	docs, err := s.engine().Catalog().ListDocs(ctx, in.Name)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := GetServiceOutput{
		Name: svc.Name, Description: svc.Description, Owners: svc.Owners,
		Concepts: svc.Concepts, Tasks: svc.Tasks, Environments: svc.Environments, OperationCount: svc.OperationCount,
		Coverage: svc.Coverage, Warnings: svc.Warnings, AcceptedWarnings: svc.AcceptedWarnings,
	}
	for _, d := range docs {
		out.Docs = append(out.Docs, d.Path)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n%s\n\nowners: %s\nconcepts: %s\noperations: %d\ndocs: %s\n",
		svc.Name, svc.Description, strings.Join(svc.Owners, ", "), strings.Join(svc.Concepts, ", "),
		svc.OperationCount, strings.Join(out.Docs, ", "))
	b.WriteString(renderCoverage(svc.Coverage))
	for _, w := range svc.Warnings {
		b.WriteString(formatWarningLine(w))
	}
	fmt.Fprintf(&b, "%d warnings accepted (reviewed)\n", len(svc.AcceptedWarnings))
	return result(b.String(), out), nil, nil
}

// --- search_apis -----------------------------------------------------

// SearchAPIsInput is search_apis' arguments.
type SearchAPIsInput struct {
	Query   string `json:"query" jsonschema:"caller intent or full-text query over authored task phrases, operation id, path, summary, description, tags, params, and field names"`
	Service string `json:"service,omitempty" jsonschema:"restrict results to this service name"`
	Method  string `json:"method,omitempty" jsonschema:"restrict results to this HTTP method (GET, POST, ...)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum number of results; default 10"`
}

// APIHit is one search_apis result.
type APIHit struct {
	ID        string             `json:"id"`
	Method    string             `json:"method"`
	Path      string             `json:"path"`
	Summary   string             `json:"summary,omitempty"`
	Score     float64            `json:"score"`
	MatchedOn []string           `json:"matched_on,omitempty"`
	Tasks     []domain.TaskMatch `json:"tasks,omitempty"`
}

// SearchAPIsOutput is search_apis' structured output.
type SearchAPIsOutput struct {
	Results []APIHit `json:"results"`
}

func (s *server) searchAPIs(ctx context.Context, req *sdkmcp.CallToolRequest, in SearchAPIsInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	results, err := s.engine().Search().Operations(ctx, in.Query, domain.SearchOptions{Service: in.Service, Method: in.Method, Limit: limit})
	if err != nil {
		return errResult(err), nil, nil
	}
	out := SearchAPIsOutput{}
	var b strings.Builder
	for _, r := range results {
		out.Results = append(out.Results, APIHit{
			ID: r.Operation.ID, Method: methodOf(r.Operation), Path: pathOf(r.Operation),
			Summary: r.Operation.Summary, Score: r.Score, MatchedOn: r.MatchedOn, Tasks: r.Tasks,
		})
		fmt.Fprintf(&b, "- %s %s %s — %s (score %.2f)\n", methodOf(r.Operation), pathOf(r.Operation), r.Operation.ID, r.Operation.Summary, r.Score)
		for _, task := range r.Tasks {
			fmt.Fprintf(&b, "  task %s: %s", task.ID, task.Phrase)
			if task.When != "" {
				fmt.Fprintf(&b, " (when %s)", task.When)
			}
			b.WriteByte('\n')
		}
	}
	if len(out.Results) == 0 {
		b.WriteString("no matches\n")
	}
	return result(b.String(), out), nil, nil
}

// --- get_api -----------------------------------------------------

// GetAPIInput is get_api's arguments.
type GetAPIInput struct {
	ID     string `json:"id" jsonschema:"operation id (or \"METHOD /path\"), as returned by search_apis"`
	Detail string `json:"detail,omitempty" jsonschema:"summary|fields|full; default summary"`
}

// ExampleSummary is one saved example listed by get_api (detail=fields or
// full), via engine.ExampleAPI.ForOperations -- not to be confused with
// ContractExamples below, which are the OpenAPI document's own embedded
// request/response examples.
type ExampleSummary struct {
	ID          string `json:"id"`
	Verified    bool   `json:"verified"`
	Description string `json:"description,omitempty"`
}

// GetAPIOutput is get_api's structured output. Fields is only populated at
// the fields and full detail levels; Schemas and ContractExamples only at
// full; Examples (saved examples, PLAN §34b) at fields and full both.
type GetAPIOutput struct {
	ID               string                    `json:"id"`
	Method           string                    `json:"method"`
	Path             string                    `json:"path"`
	Summary          string                    `json:"summary,omitempty"`
	Description      string                    `json:"description,omitempty"`
	Params           []string                  `json:"params,omitempty"`
	Body             []string                  `json:"body,omitempty"`
	Response         []string                  `json:"response,omitempty"`
	Security         []string                  `json:"security,omitempty"`
	Fields           []domain.Field            `json:"fields,omitempty"`
	Schemas          map[string]*domain.Schema `json:"schemas,omitempty"`
	ContractExamples []domain.Example          `json:"contract_examples,omitempty"`
	Examples         []ExampleSummary          `json:"examples,omitempty"`
	// RequestExample is a ready-to-send request at every detail level: the
	// best available of a verified example, a saved one, the contract's own
	// `example:`, and one synthesized from the schema. Knowing the schema is
	// not the same as knowing what a call looks like, and an agent that has
	// to compile a body out of a field list tends to go and do it in a
	// throwaway script instead.
	RequestExample *domain.RequestExample `json:"request_example,omitempty"`
}

func (s *server) getAPI(ctx context.Context, req *sdkmcp.CallToolRequest, in GetAPIInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	op, err := s.engine().Catalog().ResolveOperation(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}
	detail := in.Detail
	if detail == "" {
		detail = "summary"
	}

	out := GetAPIOutput{ID: op.ID, Method: methodOf(*op), Path: pathOf(*op), Summary: op.Summary, Description: op.Description}
	for _, p := range op.Params {
		out.Params = append(out.Params, paramLine(p))
	}
	if op.RequestBody != nil {
		out.Body = topLevelFields(op.RequestBody.Schema)
	}
	if pr := primaryResponse(*op); pr != nil {
		out.Response = topLevelFields(pr.Schema)
	}
	out.Security = securityLines(*op)

	saved := s.savedExamples(ctx, op.ID)
	if reqEx := example.Resolve(op, saved); reqEx.Body != nil || len(reqEx.Input) > 0 || len(reqEx.Headers) > 0 {
		// An operation that takes no body, no params and no headers has
		// nothing to show; "request example: {}" would be noise on every
		// parameterless GET.
		out.RequestExample = &reqEx
	}

	if detail == "fields" || detail == "full" {
		fields, err := s.engine().Catalog().Fields(ctx, op.ID)
		if err != nil {
			return errResult(err), nil, nil
		}
		out.Fields = fields
		out.Examples = exampleSummaries(saved)
	}
	if detail == "full" {
		schemas := map[string]*domain.Schema{}
		if op.RequestBody != nil && op.RequestBody.Schema != nil {
			schemas["request_body"] = op.RequestBody.Schema
		}
		for _, r := range op.Responses {
			if r.Schema != nil {
				schemas["response_"+r.Status] = r.Schema
			}
		}
		if len(schemas) > 0 {
			out.Schemas = schemas
		}
		if op.RequestBody != nil {
			out.ContractExamples = append(out.ContractExamples, op.RequestBody.Examples...)
		}
		for _, r := range op.Responses {
			out.ContractExamples = append(out.ContractExamples, r.Examples...)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s  %s\n%s\n", out.Method, out.Path, out.ID, out.Summary)
	if len(out.Params) > 0 {
		b.WriteString("params:\n")
		for _, l := range out.Params {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	if len(out.Body) > 0 {
		b.WriteString("body:\n")
		for _, l := range out.Body {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	if len(out.Response) > 0 {
		b.WriteString("response:\n")
		for _, l := range out.Response {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	if len(out.Security) > 0 {
		fmt.Fprintf(&b, "security: %s\n", strings.Join(out.Security, "; "))
	}
	if out.RequestExample != nil {
		b.WriteString(renderRequestExample(*out.RequestExample))
	}
	if len(out.Fields) > 0 {
		fmt.Fprintf(&b, "fields: %d flattened fields (see structured content)\n", len(out.Fields))
	}
	if len(out.Schemas) > 0 {
		fmt.Fprintf(&b, "schemas: %d component schemas; %d contract examples (see structured content)\n", len(out.Schemas), len(out.ContractExamples))
	}
	if len(out.Examples) > 0 {
		b.WriteString("saved examples:\n")
		for _, ex := range out.Examples {
			verified := "unverified"
			if ex.Verified {
				verified = "verified"
			}
			fmt.Fprintf(&b, "  %s (%s): %s\n", ex.ID, verified, ex.Description)
		}
	}
	return result(b.String(), out), nil, nil
}

// savedExamples returns up to 5 saved examples of opID, verified first, for
// get_api's example summaries and its request example (PLAN §34b).
// Examples() is optional infrastructure still being wired into some engines
// -- any error from it, not just a missing one, degrades to no examples
// rather than failing get_api, which otherwise has nothing to do with the
// example store's readiness.
func (s *server) savedExamples(ctx context.Context, opID string) []domain.SavedExample {
	saved, err := s.engine().Examples().ForOperations(ctx, []string{opID}, 5)
	if err != nil {
		return nil
	}
	return saved
}

func exampleSummaries(saved []domain.SavedExample) []ExampleSummary {
	out := make([]ExampleSummary, 0, len(saved))
	for _, ex := range saved {
		out = append(out, ExampleSummary{ID: ex.ID, Verified: ex.Verified != nil, Description: ex.Description})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// renderRequestExample prints the payload as JSON an agent can copy into a
// flow step, a call, or its own request, labelled with where it came from and
// what it is not.
func renderRequestExample(ex domain.RequestExample) string {
	var b strings.Builder
	fmt.Fprintf(&b, "request example (%s", ex.Source)
	if ex.SourceID != "" {
		fmt.Fprintf(&b, " %q", ex.SourceID)
	}
	b.WriteString(")")
	if ex.Note != "" {
		fmt.Fprintf(&b, " -- %s", ex.Note)
	}
	b.WriteString("\n")
	if len(ex.Input) > 0 {
		fmt.Fprintf(&b, "  input: %s\n", compactJSON(ex.Input))
	}
	if ex.Body != nil {
		if pretty, err := json.MarshalIndent(ex.Body, "  ", "  "); err == nil {
			fmt.Fprintf(&b, "  body: %s\n", pretty)
		}
	}
	if len(ex.Headers) > 0 {
		fmt.Fprintf(&b, "  headers: %s\n", compactJSON(ex.Headers))
	}
	if ex.Source != domain.RequestExampleVerified {
		b.WriteString("  save a working one with create_example(run_id) so the next caller starts from it\n")
	}
	return b.String()
}

func compactJSON(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(out)
}

// --- get_dsl_reference -----------------------------------------------------

// GetDSLReferenceInput is get_dsl_reference's arguments.
type GetDSLReferenceInput struct {
	Topic string `json:"topic,omitempty" jsonschema:"sapien|flow|memory|expressions|service; default flow. sapien = what Sapien is and what you can do with it (read this first in a new session). service = how to lay out a service's api/ package (openapi.yaml, service.yaml, docs/) so Sapien can index it, and how to register it"`
}

// GetDSLReferenceOutput is get_dsl_reference's structured output.
type GetDSLReferenceOutput struct {
	Topic string `json:"topic"`
	Text  string `json:"text"`
}

func (s *server) getDSLReference(_ context.Context, _ *sdkmcp.CallToolRequest, in GetDSLReferenceInput) (*sdkmcp.CallToolResult, any, error) {
	topic := in.Topic
	if topic == "" {
		topic = "flow"
	}
	text, err := s.reference(topic)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := GetDSLReferenceOutput{Topic: topic, Text: text}
	return result(text, out), nil, nil
}

// --- search_docs -----------------------------------------------------

// SearchDocsInput is search_docs' arguments.
type SearchDocsInput struct {
	Query   string `json:"query" jsonschema:"full-text query over documentation section headings and bodies"`
	Service string `json:"service,omitempty" jsonschema:"restrict results to this service name"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum number of results; default 10"`
}

// SearchDocsOutput is search_docs' structured output.
type SearchDocsOutput struct {
	Results []domain.DocSearchResult `json:"results"`
}

func (s *server) searchDocs(ctx context.Context, req *sdkmcp.CallToolRequest, in SearchDocsInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	results, err := s.engine().Search().Docs(ctx, in.Query, domain.SearchOptions{Service: in.Service, Limit: limit})
	if err != nil {
		return errResult(err), nil, nil
	}
	out := SearchDocsOutput{Results: results}
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "- [%s#%s] %s/%s — %s (score %.2f)\n", r.Heading, r.SectionID, r.Service, r.Path, r.Snippet, r.Score)
	}
	if len(results) == 0 {
		b.WriteString("no matches\n")
	}
	return result(b.String(), out), nil, nil
}

// --- get_doc -----------------------------------------------------

// GetDocInput is get_doc's arguments.
type GetDocInput struct {
	Service string `json:"service" jsonschema:"service name"`
	Path    string `json:"path" jsonschema:"doc path, e.g. docs/allocation.md, or contract#info"`
	Section string `json:"section,omitempty" jsonschema:"heading to return just that one section"`
}

// GetDocOutput is get_doc's structured output.
type GetDocOutput struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

func (s *server) getDoc(ctx context.Context, req *sdkmcp.CallToolRequest, in GetDocInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	doc, err := s.engine().Catalog().GetDoc(ctx, in.Service, in.Path)
	if err != nil {
		return errResult(err), nil, nil
	}
	md := renderDocMarkdown(doc, in.Section)
	out := GetDocOutput{ID: doc.ID, Title: doc.Title, Markdown: md}
	return result(md, out), nil, nil
}

// renderDocMarkdown renders a Doc as Markdown, or just one section if
// section is non-empty (matched case-insensitively against the heading).
func renderDocMarkdown(doc *domain.Doc, section string) string {
	if section != "" {
		for _, sec := range doc.Sections {
			if strings.EqualFold(sec.Heading, section) {
				return sec.Body
			}
		}
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", doc.Title)
	for _, sec := range doc.Sections {
		fmt.Fprintf(&b, "%s %s\n\n%s\n\n", strings.Repeat("#", max(sec.Level, 1)), sec.Heading, sec.Body)
	}
	return b.String()
}

// --- get_schema -----------------------------------------------------

// GetSchemaInput is get_schema's arguments.
type GetSchemaInput struct {
	Service string `json:"service" jsonschema:"service name"`
	Name    string `json:"name" jsonschema:"component schema name"`
}

// GetSchemaOutput is get_schema's structured output.
type GetSchemaOutput struct {
	Service string      `json:"service"`
	Name    string      `json:"name"`
	Fields  []FlatField `json:"fields"`
	UsedBy  []string    `json:"used_by,omitempty"`
}

func (s *server) getSchema(ctx context.Context, req *sdkmcp.CallToolRequest, in GetSchemaInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	named, err := s.engine().Catalog().GetSchema(ctx, in.Service, in.Name)
	if err != nil {
		return errResult(err), nil, nil
	}
	var fields []FlatField
	flattenSchema("", named.Schema, false, 0, &fields)
	out := GetSchemaOutput{Service: in.Service, Name: in.Name, Fields: fields, UsedBy: named.UsedBy}
	var b strings.Builder
	fmt.Fprintf(&b, "%s.%s (used by %d operation(s))\n", in.Service, in.Name, len(named.UsedBy))
	for _, f := range fields {
		req := ""
		if f.Required {
			req = ", required"
		}
		fmt.Fprintf(&b, "  %s: %s%s", f.Path, f.Type, req)
		if f.Description != "" {
			fmt.Fprintf(&b, " — %s", f.Description)
		}
		b.WriteString("\n")
	}
	return result(b.String(), out), nil, nil
}
