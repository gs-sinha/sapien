package retrieval

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
)

// RenderOperation compresses op into the compact, agent-ready rendering
// PLAN.md §14 describes: params, request-body fields (top level plus one
// nested level), the lowest 2xx response's fields (same depth), security
// requirements, and (when includeExamples) request/response examples.
//
// fields is the operation's full flattened field list (as catalog.Fields
// returns it: every field, ordered by path); RenderOperation filters and
// depth-limits it rather than re-deriving it from op's schemas.
func RenderOperation(op *domain.Operation, fields []domain.Field, includeExamples bool) domain.OperationContext {
	oc := domain.OperationContext{
		Tier:        domain.TierContract,
		ID:          op.ID,
		Summary:     op.Summary,
		Description: op.Description,
	}
	if op.HTTP != nil {
		oc.Method = op.HTTP.Method
		oc.Path = op.HTTP.Path
	}

	byPath := make(map[string]domain.Field, len(fields))
	for _, f := range fields {
		byPath[f.Path] = f
	}

	for _, p := range op.Params {
		oc.Params = append(oc.Params, renderParam(p, byPath))
	}

	oc.Body = renderFieldList(fields, "request.body.", true)

	if status, ok := lowestSuccessStatus(op.Responses); ok {
		oc.Response = renderFieldList(fields, fmt.Sprintf("response.%s.body.", status), false)
	}

	for _, sec := range op.Security {
		oc.Security = append(oc.Security, fmt.Sprintf("%s (%s)", sec.Scheme, sec.Type))
	}

	if includeExamples {
		oc.Examples = collectExamples(op)
	}

	return oc
}

// renderParam formats one parameter as "<name> (<in>, <type>[, required]) —
// <description>".
func renderParam(p domain.Param, byPath map[string]domain.Field) string {
	typ := "any"
	if f, ok := byPath[fmt.Sprintf("request.%s.%s", p.In, p.Name)]; ok {
		typ = f.Type
	} else if p.Schema != nil {
		typ = string(p.Schema.Kind)
	}

	parts := []string{string(p.In), typ}
	if p.Required {
		parts = append(parts, "required")
	}
	line := fmt.Sprintf("%s (%s)", p.Name, strings.Join(parts, ", "))
	if p.Description != "" {
		line += " — " + p.Description
	}
	return line
}

// renderFieldList renders every field whose path starts with prefix,
// labeled by the path relative to prefix (so an array item field keeps its
// "items[]" segment), limited to depth <= 2 relative to prefix (the field
// itself, plus one nested level). includeRequired controls whether ",
// required" is appended for a required field (body fields show it; response
// fields, per PLAN.md §14, don't).
func renderFieldList(fields []domain.Field, prefix string, includeRequired bool) []string {
	var out []string
	for _, f := range fields {
		rel, ok := strings.CutPrefix(f.Path, prefix)
		if !ok || rel == "" {
			continue
		}
		if fieldDepth(rel) > 2 {
			continue
		}
		line := rel + ": " + f.Type
		if includeRequired && f.Required {
			line += ", required"
		}
		if f.Description != "" {
			line += " — " + f.Description
		}
		out = append(out, line)
	}
	return out
}

// fieldDepth counts the "."-separated segments of a field path relative to
// its request.body./response.<status>.body. prefix ("[]" is part of the
// segment it terminates, not its own level).
func fieldDepth(relPath string) int {
	return strings.Count(relPath, ".") + 1
}

// lowestSuccessStatus returns the numerically-lowest 2xx (or the first
// generic "2XX") response status among responses, or ok=false if there is
// no success response.
func lowestSuccessStatus(responses []domain.Response) (status string, ok bool) {
	bestNum := -1
	generic := ""
	for _, r := range responses {
		if !isSuccessStatus(r.Status) {
			continue
		}
		if n, err := strconv.Atoi(r.Status); err == nil {
			if bestNum == -1 || n < bestNum {
				bestNum = n
				status = r.Status
			}
		} else if generic == "" {
			generic = r.Status
		}
	}
	if bestNum != -1 {
		return status, true
	}
	if generic != "" {
		return generic, true
	}
	return "", false
}

// isSuccessStatus reports whether status is a 2xx code or the generic
// "2XX"/"2xx" range.
func isSuccessStatus(status string) bool {
	if strings.EqualFold(status, "2XX") {
		return true
	}
	n, err := strconv.Atoi(status)
	return err == nil && n >= 200 && n < 300
}

// collectExamples gathers example values worth showing an agent: the
// request body's named examples, then every success response's named
// examples.
func collectExamples(op *domain.Operation) []any {
	var out []any
	if op.RequestBody != nil {
		for _, ex := range op.RequestBody.Examples {
			out = append(out, ex.Value)
		}
	}
	for _, r := range op.Responses {
		if !isSuccessStatus(r.Status) {
			continue
		}
		for _, ex := range r.Examples {
			out = append(out, ex.Value)
		}
	}
	return out
}

// renderExample compresses a domain.SavedExample into the bundle's
// ExampleContext shape (PLAN §14/§34b): Verified/Env are flattened out of
// SavedExample.Verified rather than carrying the whole ExampleVerified
// struct, since the bundle only ever needs to say "verified, in stage" --
// the run and step ids that produced it are for get_example, not context.
func renderExample(ex domain.SavedExample) domain.ExampleContext {
	ec := domain.ExampleContext{
		ID:          ex.ID,
		Operation:   ex.Operation,
		Description: ex.Description,
		Verified:    ex.Verified != nil,
		Input:       ex.Input,
		Body:        ex.Body,
		Tags:        ex.Tags,
	}
	if ex.Verified != nil {
		ec.Env = ex.Verified.Env
	}
	return ec
}

// EstimateTokens approximates bundle's token cost as len(JSON)/4.
func EstimateTokens(bundle *domain.ContextBundle) int {
	data, err := json.Marshal(bundle)
	if err != nil {
		return 0
	}
	return len(data) / 4
}
