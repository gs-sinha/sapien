package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yosida95/uritemplate/v3"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/spec"
)

// Resource templates (PLAN §23): service and catalog data is served as JSON;
// docs, flows, memories, and Sapien's own reference material are served as
// Markdown (flow.schema.json is the one JSON exception among the reference
// resources).
var (
	tmplService    = uritemplate.MustNew("sapien://services/{name}")
	tmplServiceDoc = uritemplate.MustNew("sapien://services/{name}/docs/{+path}")
	tmplOperation  = uritemplate.MustNew("sapien://operations/{id}")
	tmplSchema     = uritemplate.MustNew("sapien://schemas/{service}/{name}")
	tmplFlow       = uritemplate.MustNew("sapien://flows/{id}")
	tmplMemory     = uritemplate.MustNew("sapien://memories/{id}")
	tmplReference  = uritemplate.MustNew("sapien://reference/{topic}")
)

func (srv *server) registerResources(s *sdkmcp.Server) {
	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: tmplService.Raw(),
		Name:        "service",
		Description: "A registered service's metadata (description, owners, concepts, environments, operation count).",
		MIMEType:    "application/json",
	}, srv.readService)

	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: tmplServiceDoc.Raw(),
		Name:        "service-doc",
		Description: "One documentation file belonging to a service, as Markdown.",
		MIMEType:    "text/markdown",
	}, srv.readServiceDoc)

	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: tmplOperation.Raw(),
		Name:        "operation",
		Description: "One normalized API operation.",
		MIMEType:    "application/json",
	}, srv.readOperation)

	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: tmplSchema.Raw(),
		Name:        "schema",
		Description: "One named component schema.",
		MIMEType:    "application/json",
	}, srv.readSchema)

	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: tmplFlow.Raw(),
		Name:        "flow",
		Description: "One flow's YAML source.",
		MIMEType:    "text/markdown",
	}, srv.readFlow)

	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: tmplMemory.Raw(),
		Name:        "memory",
		Description: "One memory, as Markdown with its front-matter fields.",
		MIMEType:    "text/markdown",
	}, srv.readMemory)

	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: tmplReference.Raw(),
		Name:        "reference",
		Description: "Sapien's own reference material: flow-dsl, memory, expressions, service (the api/ package layout for onboarding), or flow.schema.json.",
		MIMEType:    "text/markdown",
	}, srv.readReference)
}

func jsonContents(uri string, v any) (*sdkmcp.ReadResourceResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &sdkmcp.ReadResourceResult{
		Contents: []*sdkmcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}},
	}, nil
}

func textContents(uri, mimeType, text string) *sdkmcp.ReadResourceResult {
	return &sdkmcp.ReadResourceResult{
		Contents: []*sdkmcp.ResourceContents{{URI: uri, MIMEType: mimeType, Text: text}},
	}
}

func (srv *server) readService(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	uri := req.Params.URI
	name := tmplService.Match(uri).Get("name").String()
	svc, err := srv.eng.Services().Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return jsonContents(uri, svc)
}

func (srv *server) readServiceDoc(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	uri := req.Params.URI
	vals := tmplServiceDoc.Match(uri)
	name := vals.Get("name").String()
	path := vals.Get("path").String()
	doc, err := srv.eng.Catalog().GetDoc(ctx, name, path)
	if err != nil {
		return nil, err
	}
	return textContents(uri, "text/markdown", renderDocMarkdown(doc, "")), nil
}

func (srv *server) readOperation(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	uri := req.Params.URI
	id := tmplOperation.Match(uri).Get("id").String()
	op, err := srv.eng.Catalog().GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	return jsonContents(uri, op)
}

func (srv *server) readSchema(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	uri := req.Params.URI
	vals := tmplSchema.Match(uri)
	service := vals.Get("service").String()
	name := vals.Get("name").String()
	named, err := srv.eng.Catalog().GetSchema(ctx, service, name)
	if err != nil {
		return nil, err
	}
	return jsonContents(uri, named)
}

func (srv *server) readFlow(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	uri := req.Params.URI
	id := tmplFlow.Match(uri).Get("id").String()
	flow, err := srv.eng.Flows().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	text := flow.Source
	if text == "" {
		text = fmt.Sprintf("# flow %s has no stored YAML source\n", flow.ID)
	}
	return textContents(uri, "text/markdown", fmt.Sprintf("```yaml\n%s\n```\n", text)), nil
}

func (srv *server) readMemory(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	uri := req.Params.URI
	id := tmplMemory.Match(uri).Get("id").String()
	mem, err := srv.eng.Memories().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return textContents(uri, "text/markdown", renderMemoryMarkdown(mem)), nil
}

func renderMemoryMarkdown(m *domain.Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\ntype: %s\nscope: %s\nstatus: %s\ntags: %s\n---\n\n%s\n",
		m.ID, m.Type, m.Scope, m.Status, strings.Join(m.Tags, ", "), m.Text)
	return b.String()
}

// referenceTopics maps a resource topic name to the DSL reference topic
// Engine.Flows().Reference expects (PLAN §23: the resource is named
// "flow-dsl", the tool/engine topic is "flow").
var referenceTopics = map[string]string{
	"flow-dsl":    "flow",
	"memory":      "memory",
	"expressions": "expressions",
	"service":     "service",
}

func (srv *server) readReference(_ context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	uri := req.Params.URI
	topic := tmplReference.Match(uri).Get("topic").String()

	if topic == "flow.schema.json" {
		return textContents(uri, "application/json", string(spec.Schema(spec.Flow))), nil
	}

	engineTopic, ok := referenceTopics[topic]
	if !ok {
		return nil, sdkmcp.ResourceNotFoundError(uri)
	}
	text, err := srv.reference(engineTopic)
	if err != nil {
		return nil, err
	}
	return textContents(uri, "text/markdown", text), nil
}
