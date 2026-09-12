// Package openapi ingests an OpenAPI 3.0/3.1 document into Sapien's normalized API
// model (PLAN.md §5), using github.com/pb33f/libopenapi for parsing and $ref
// resolution.
package openapi

import (
	"fmt"
	"os"
	"regexp"
	"sort"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// defaultMaxDepth is the schema flattening depth cap used when Options.MaxDepth is
// not set (<= 0).
const defaultMaxDepth = 12

// Options configures Ingest.
type Options struct {
	// ServiceID prefixes every operation id: "<ServiceID>.<operationId>". Required.
	ServiceID string
	// File is the path recorded in SourceLoc.File; callers should pass a
	// workspace-relative path where possible.
	File string
	// Concepts are copied onto every operation, from service.yaml.
	Concepts []string
	// MaxDepth caps schema flattening depth. Default 12.
	MaxDepth int
	// DocParser, when set, splits a Markdown document into sections. When nil,
	// contract-embedded docs (info.description, tag descriptions) are emitted as a
	// single section.
	DocParser func(serviceID, path, title, markdown string, source domain.DocSource) domain.Doc
}

// Result is the normalized output of Ingest.
type Result struct {
	Title       string
	Version     string
	Description string
	Operations  []domain.Operation
	Schemas     []domain.NamedSchema
	Fields      []domain.Field
	Aliases     []domain.Alias
	Docs        []domain.Doc
	Warnings    []domain.LintWarning
}

// builder holds the shared state accumulated while walking one document.
type builder struct {
	serviceID string
	file      string
	concepts  []string
	maxDepth  int
	docParser func(serviceID, path, title, markdown string, source domain.DocSource) domain.Doc

	warnings []domain.LintWarning

	usedIDs map[string]bool // final operation ids assigned so far

	securitySchemeTypes map[string]string // scheme name -> type

	// usedBy accumulates, for every component schema name, the set of operation ids
	// that referenced it (directly or nested).
	usedBy map[string]map[string]bool
}

func (b *builder) addWarning(w domain.LintWarning) {
	b.warnings = append(b.warnings, w)
}

func (b *builder) markUsedBy(opID string) func(component string) {
	return func(component string) {
		set, ok := b.usedBy[component]
		if !ok {
			set = map[string]bool{}
			b.usedBy[component] = set
		}
		set[opID] = true
	}
}

// Ingest turns an OpenAPI 3.0/3.1 document (YAML or JSON bytes) into Sapien's
// normalized model.
func Ingest(src []byte, opts Options) (*Result, error) {
	if opts.ServiceID == "" {
		return nil, errs.New(errs.Invalid, "openapi: Options.ServiceID is required")
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}

	// Everything below builds libopenapi's models, and everything this
	// function returns is a domain type, so libopenapi's process-global
	// memoization has nothing left to serve once we return. Release it, or
	// it pins this document's whole parse tree forever (parsercache.go).
	retainParserCaches()
	defer releaseParserCaches()

	doc, err := libopenapi.NewDocument(src)
	if err != nil {
		return nil, parseError(err, opts.File)
	}

	if info := doc.GetSpecInfo(); info != nil && info.SpecFormat == datamodel.OAS2 {
		return nil, errs.New(errs.ContractParse, "openapi: Swagger 2.0 documents are not supported").
			WithSource(domain.SourceLoc{File: opts.File}).
			WithHint("upgrade the contract to OpenAPI 3.0 or 3.1")
	}

	model, buildErr := doc.BuildV3Model()
	if model == nil {
		return nil, parseError(buildErr, opts.File)
	}

	b := &builder{
		serviceID:           opts.ServiceID,
		file:                opts.File,
		concepts:            opts.Concepts,
		maxDepth:            maxDepth,
		docParser:           opts.DocParser,
		usedIDs:             map[string]bool{},
		securitySchemeTypes: map[string]string{},
		usedBy:              map[string]map[string]bool{},
	}

	for _, e := range splitErrors(buildErr) {
		b.warnBuildError(e)
	}

	highDoc := &model.Model

	result := &Result{}
	if highDoc.Info != nil {
		result.Title = highDoc.Info.Title
		result.Version = highDoc.Info.Version
		result.Description = highDoc.Info.Description
	}

	if highDoc.Components != nil && highDoc.Components.SecuritySchemes != nil {
		for name, scheme := range highDoc.Components.SecuritySchemes.FromOldest() {
			if scheme != nil {
				b.securitySchemeTypes[name] = scheme.Type
			}
		}
	}

	namedSchemas := b.buildComponentSchemas(highDoc)

	operations, fields, aliases := b.buildOperations(highDoc)

	for i := range namedSchemas {
		ns := &namedSchemas[i]
		if set := b.usedBy[ns.Name]; len(set) > 0 {
			ids := make([]string, 0, len(set))
			for id := range set {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			ns.UsedBy = ids
		}
	}

	result.Operations = operations
	result.Schemas = namedSchemas
	result.Fields = fields
	result.Aliases = aliases
	result.Docs = b.buildDocs(highDoc)
	result.Warnings = b.warnings

	return result, nil
}

// IngestFile reads path and ingests it. Options.File defaults to path when unset.
func IngestFile(path string, opts Options) (*Result, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.Wrap(errs.ContractParse, err, "openapi: reading %s", path)
	}
	if opts.File == "" {
		opts.File = path
	}
	return Ingest(src, opts)
}

// buildComponentSchemas normalizes every components/schemas entry into a
// domain.NamedSchema. UsedBy is left empty here; the caller fills it in after
// operations have been walked.
func (b *builder) buildComponentSchemas(highDoc *v3.Document) []domain.NamedSchema {
	if highDoc.Components == nil || highDoc.Components.Schemas == nil {
		return nil
	}
	var out []domain.NamedSchema
	for name, proxy := range highDoc.Components.Schemas.FromOldest() {
		if proxy == nil {
			continue
		}
		s, err := proxy.BuildSchema()
		if err != nil || s == nil {
			b.addWarning(domain.LintWarning{
				Code:    "UNRESOLVED_REF",
				Message: fmt.Sprintf("unable to resolve component schema %s", name),
			})
			out = append(out, domain.NamedSchema{
				ServiceID: b.serviceID,
				Name:      name,
				Schema:    &domain.Schema{Kind: domain.KindAny, Name: name},
			})
			continue
		}
		normalized := b.convertSchema(s, schemaCtx{chain: []string{name}, depth: 1}, nil)
		normalized.Name = name
		out = append(out, domain.NamedSchema{
			ServiceID: b.serviceID,
			Name:      name,
			Schema:    normalized,
			Hash:      hashSchema(normalized),
		})
	}
	return out
}

// splitErrors expands an error possibly created by errors.Join into its members.
func splitErrors(err error) []error {
	if err == nil {
		return nil
	}
	if u, ok := err.(interface{ Unwrap() []error }); ok {
		return u.Unwrap()
	}
	return []error{err}
}

// lineNumberREs matches the line-number formats seen in libopenapi/go-yaml parse
// errors: "L12.C1" (go-yaml's own flow-parser errors) and "line 12" (the
// "yaml: line N: ..." style produced for other parse failures).
var lineNumberREs = []*regexp.Regexp{
	regexp.MustCompile(`[Ll](\d+)\.[Cc]\d+`),
	regexp.MustCompile(`line (\d+)`),
}

// parseError converts a libopenapi parse/build error into an E_CONTRACT_PARSE error,
// extracting a line number from the message when present.
func parseError(err error, file string) error {
	if err == nil {
		err = fmt.Errorf("unknown parse error")
	}
	e := errs.Wrap(errs.ContractParse, err, "openapi: failed to parse contract")
	loc := domain.SourceLoc{File: file}
	for _, re := range lineNumberREs {
		if m := re.FindStringSubmatch(err.Error()); m != nil {
			if n, convErr := parseInt(m[1]); convErr == nil {
				loc.Line = n
				break
			}
		}
	}
	e = e.WithSource(loc).WithHint("check the file is valid YAML/JSON and describes an OpenAPI 3.0 or 3.1 document")
	return e
}

func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
