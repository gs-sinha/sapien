package openapi

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/index"
	"github.com/pb33f/libopenapi/orderedmap"

	"github.com/growsimplee/sapien/internal/domain"
)

// buildOperations walks every path/method in document order and returns the
// normalized operations, their flattened fields, and the method/path alias table.
func (b *builder) buildOperations(highDoc *v3.Document) ([]domain.Operation, []domain.Field, []domain.Alias) {
	if highDoc.Paths == nil || highDoc.Paths.PathItems == nil {
		return nil, nil, nil
	}

	var operations []domain.Operation
	var fields []domain.Field
	var aliases []domain.Alias

	for path, item := range highDoc.Paths.PathItems.FromOldest() {
		if item == nil {
			continue
		}
		for method, op := range item.GetOperations().FromOldest() {
			if op == nil {
				continue
			}
			operation, opFields := b.buildOperation(highDoc, path, method, item, op)
			operations = append(operations, operation)
			fields = append(fields, opFields...)
			aliases = append(aliases, domain.Alias{
				Method:      operation.HTTP.Method,
				Path:        path,
				OperationID: operation.ID,
			})
		}
	}
	return operations, fields, aliases
}

func (b *builder) buildOperation(highDoc *v3.Document, path, method string, item *v3.PathItem, op *v3.Operation) (domain.Operation, []domain.Field) {
	rawID := op.OperationId
	synthesized := rawID == ""
	localID := rawID
	if synthesized {
		localID = synthesizeOpID(method, path)
	}
	finalID, collided := dedupeID(b.serviceID+"."+localID, b.usedIDs)

	line := 0
	if lowOp := op.GoLow(); lowOp != nil && lowOp.KeyNode != nil {
		line = lowOp.KeyNode.Line
	}
	source := domain.SourceLoc{File: b.file, Pointer: operationPointer(path, method), Line: line}

	if synthesized {
		b.addWarning(domain.LintWarning{
			Code:    "SYNTHESIZED_OPERATION_ID",
			Message: fmt.Sprintf("%s %s has no operationId; synthesized %q", strings.ToUpper(method), path, finalID),
			Source:  &source,
		})
	}
	if collided {
		b.addWarning(domain.LintWarning{
			Code:    "DUPLICATE_OPERATION_ID",
			Message: fmt.Sprintf("duplicate operation id for %s %s; renamed to %s", strings.ToUpper(method), path, finalID),
			Source:  &source,
		})
	}
	if strings.TrimSpace(op.Summary) == "" {
		b.addWarning(domain.LintWarning{
			Code:    "MISSING_SUMMARY",
			Message: fmt.Sprintf("%s has no summary", finalID),
			Source:  &source,
		})
	}

	markUsedBy := b.markUsedBy(finalID)

	params := b.mergeParams(item.Parameters, op.Parameters, markUsedBy)

	var body *domain.Body
	if op.RequestBody != nil {
		body = b.buildRequestBody(op.RequestBody, &source, finalID, markUsedBy)
	}

	responses, hasSuccess := b.buildResponses(op.Responses, &source, finalID, markUsedBy)
	if !hasSuccess {
		b.addWarning(domain.LintWarning{
			Code:    "NO_SUCCESS_RESPONSE",
			Message: fmt.Sprintf("%s has no documented 2xx/default success response", finalID),
			Source:  &source,
		})
	}

	security := b.buildSecurity(op.Security, highDoc.Security)

	deprecated := op.Deprecated != nil && *op.Deprecated

	operation := domain.Operation{
		ID:          finalID,
		ServiceID:   b.serviceID,
		Protocol:    domain.ProtocolHTTP,
		HTTP:        &domain.HTTPBinding{Method: strings.ToUpper(method), Path: path},
		RawOpID:     rawID,
		Synthesized: synthesized,
		Summary:     op.Summary,
		Description: op.Description,
		Tags:        append([]string{}, op.Tags...),
		Concepts:    append([]string{}, b.concepts...),
		Params:      params,
		RequestBody: body,
		Responses:   responses,
		Security:    security,
		Deprecated:  deprecated,
		Source:      source,
	}
	operation.Hash = hashOperation(operation)

	opFields := flattenOperationFields(finalID, &operation)

	return operation, opFields
}

type paramKey struct{ name, in string }

// mergeParams merges path-level and operation-level parameters; the operation wins
// on name+in collisions. Unique path params are kept in their own order, followed by
// every operation param in its own order.
func (b *builder) mergeParams(pathParams, opParams []*v3.Parameter, usedBy func(string)) []domain.Param {
	opSet := make(map[paramKey]bool, len(opParams))
	for _, p := range opParams {
		if p == nil {
			continue
		}
		opSet[paramKey{p.Name, p.In}] = true
	}

	var out []domain.Param
	for _, p := range pathParams {
		if p == nil {
			continue
		}
		if opSet[paramKey{p.Name, p.In}] {
			continue
		}
		out = append(out, b.convertParam(p, usedBy))
	}
	for _, p := range opParams {
		if p == nil {
			continue
		}
		out = append(out, b.convertParam(p, usedBy))
	}
	return out
}

func (b *builder) convertParam(p *v3.Parameter, usedBy func(string)) domain.Param {
	in := domain.ParamLocation(strings.ToLower(p.In))
	required := p.Required != nil && *p.Required
	if in == domain.InPath {
		required = true
	}
	var schema *domain.Schema
	if p.Schema != nil {
		schema = b.resolveSchemaProxy(p.Schema, schemaCtx{}, usedBy)
	}
	var example any
	if p.Example != nil {
		example = nodeToValue(p.Example)
	}
	return domain.Param{
		Name:        p.Name,
		In:          in,
		Required:    required,
		Description: p.Description,
		Schema:      schema,
		Example:     example,
		Deprecated:  p.Deprecated,
	}
}

// pickMediaType chooses application/json when present, else the first media type in
// document order.
func pickMediaType(content *orderedmap.Map[string, *v3.MediaType]) (string, *v3.MediaType) {
	if content == nil {
		return "", nil
	}
	if mt := content.GetOrZero("application/json"); mt != nil {
		return "application/json", mt
	}
	for name, mt := range content.FromOldest() {
		return name, mt
	}
	return "", nil
}

func (b *builder) buildExamples(mt *v3.MediaType) []domain.Example {
	if mt == nil {
		return nil
	}
	var out []domain.Example
	if mt.Example != nil {
		out = append(out, domain.Example{Value: nodeToValue(mt.Example)})
	}
	if mt.Examples != nil {
		for name, ex := range mt.Examples.FromOldest() {
			if ex == nil {
				continue
			}
			out = append(out, domain.Example{Name: name, Summary: ex.Summary, Value: nodeToValue(ex.Value)})
		}
	}
	return out
}

func (b *builder) buildRequestBody(rb *v3.RequestBody, source *domain.SourceLoc, opID string, usedBy func(string)) *domain.Body {
	ct, mt := pickMediaType(rb.Content)
	var schema *domain.Schema
	if mt != nil && mt.Schema != nil {
		schema = b.resolveSchemaProxy(mt.Schema, schemaCtx{}, usedBy)
	}
	if ct != "" && ct != "application/json" {
		b.addWarning(domain.LintWarning{
			Code:    "UNSUPPORTED_MEDIA_TYPE",
			Message: fmt.Sprintf("%s request body has no application/json content (using %q)", opID, ct),
			Source:  source,
		})
	}
	return &domain.Body{
		ContentType: ct,
		Required:    rb.Required != nil && *rb.Required,
		Description: rb.Description,
		Schema:      schema,
		Examples:    b.buildExamples(mt),
	}
}

type responseEntry struct {
	code string
	resp *v3.Response
}

// classifyCode ranks a response code for the stable ordering rule: numeric codes
// ascending, then "NXX"-style ranges (ordered by leading digit), then "default".
func classifyCode(code string) (tier, num int) {
	if code == "default" {
		return 2, 0
	}
	if n, err := strconv.Atoi(code); err == nil {
		return 0, n
	}
	if len(code) > 0 && code[0] >= '1' && code[0] <= '9' {
		return 1, int(code[0] - '0')
	}
	return 3, 0
}

func isSuccessCode(code string) bool {
	if code == "2XX" || code == "2xx" {
		return true
	}
	if n, err := strconv.Atoi(code); err == nil {
		return n >= 200 && n < 300
	}
	return false
}

func (b *builder) buildResponses(responses *v3.Responses, source *domain.SourceLoc, opID string, usedBy func(string)) ([]domain.Response, bool) {
	if responses == nil {
		return nil, false
	}
	var entries []responseEntry
	if responses.Codes != nil {
		for code, r := range responses.Codes.FromOldest() {
			entries = append(entries, responseEntry{code, r})
		}
	}
	if responses.Default != nil {
		entries = append(entries, responseEntry{"default", responses.Default})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ti, ni := classifyCode(entries[i].code)
		tj, nj := classifyCode(entries[j].code)
		if ti != tj {
			return ti < tj
		}
		return ni < nj
	})

	hasSuccess := false
	out := make([]domain.Response, 0, len(entries))
	for _, e := range entries {
		if e.resp == nil {
			continue
		}
		if isSuccessCode(e.code) {
			hasSuccess = true
		}
		ct, mt := pickMediaType(e.resp.Content)
		var schema *domain.Schema
		if mt != nil && mt.Schema != nil {
			schema = b.resolveSchemaProxy(mt.Schema, schemaCtx{}, usedBy)
		}
		var headers map[string]*domain.Schema
		if e.resp.Headers != nil {
			for hname, h := range e.resp.Headers.FromOldest() {
				if h == nil || h.Schema == nil {
					continue
				}
				if headers == nil {
					headers = map[string]*domain.Schema{}
				}
				headers[hname] = b.resolveSchemaProxy(h.Schema, schemaCtx{}, usedBy)
			}
		}
		if ct != "" && ct != "application/json" {
			b.addWarning(domain.LintWarning{
				Code:    "UNSUPPORTED_MEDIA_TYPE",
				Message: fmt.Sprintf("%s response %s has no application/json content (using %q)", opID, e.code, ct),
				Source:  source,
			})
		}
		out = append(out, domain.Response{
			Status:      e.code,
			Description: e.resp.Description,
			ContentType: ct,
			Schema:      schema,
			Headers:     headers,
			Examples:    b.buildExamples(mt),
		})
	}
	return out, hasSuccess
}

// buildSecurity resolves operation-level security, falling back to document-level
// security when the operation does not declare its own (nil slice, per libopenapi's
// convention of using a non-nil empty slice for an explicit "no security" override).
func (b *builder) buildSecurity(opSecurity, docSecurity []*base.SecurityRequirement) []domain.SecurityRequirement {
	reqs := opSecurity
	if reqs == nil {
		reqs = docSecurity
	}
	var out []domain.SecurityRequirement
	for _, alt := range reqs {
		if alt == nil || alt.Requirements == nil {
			continue
		}
		for name, scopes := range alt.Requirements.FromOldest() {
			out = append(out, domain.SecurityRequirement{
				Scheme: name,
				Type:   b.securitySchemeTypes[name],
				Scopes: append([]string{}, scopes...),
			})
		}
	}
	return out
}

// warnBuildError converts a libopenapi build-time error (which, once a model has
// been produced, can only be a circular-reference resolution error) into a
// CIRCULAR_REF lint warning.
func (b *builder) warnBuildError(err error) {
	if err == nil {
		return
	}
	var refErr *index.ResolvingError
	if errors.As(err, &refErr) {
		var loc *domain.SourceLoc
		if refErr.Node != nil {
			loc = &domain.SourceLoc{File: b.file, Line: refErr.Node.Line}
		}
		b.addWarning(domain.LintWarning{Code: "CIRCULAR_REF", Message: refErr.Error(), Source: loc})
		return
	}
	b.addWarning(domain.LintWarning{Code: "CIRCULAR_REF", Message: err.Error()})
}
