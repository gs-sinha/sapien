package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/growsimplee/sapien/internal/domain"
)

// result builds a successful tool result carrying both the compact text
// rendering and the structured value, per PLAN §23.3.
func result(text string, structured any) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: text}},
		StructuredContent: structured,
	}
}

// methodOf and pathOf read an operation's HTTP binding, defaulting to
// placeholders for the (currently unused) non-HTTP protocol case.
func methodOf(op domain.Operation) string {
	if op.HTTP != nil {
		return op.HTTP.Method
	}
	return ""
}

func pathOf(op domain.Operation) string {
	if op.HTTP != nil {
		return op.HTTP.Path
	}
	return ""
}

// paramLine renders one parameter as "name (in, type, required) — description".
func paramLine(p domain.Param) string {
	typ := "any"
	if p.Schema != nil {
		typ = string(p.Schema.Kind)
	}
	req := ""
	if p.Required {
		req = ", required"
	}
	line := fmt.Sprintf("%s (%s, %s%s)", p.Name, p.In, typ, req)
	if p.Description != "" {
		line += " — " + p.Description
	}
	return line
}

// topLevelFields renders the direct properties of an object schema as
// "name: type — description" lines, one level deep (no recursion). Used for
// the "summary" detail level of get_api.
func topLevelFields(sch *domain.Schema) []string {
	if sch == nil {
		return nil
	}
	order := sch.PropertyOrder
	if len(order) == 0 {
		for name := range sch.Properties {
			order = append(order, name)
		}
	}
	reqSet := make(map[string]bool, len(sch.Required))
	for _, r := range sch.Required {
		reqSet[r] = true
	}
	var lines []string
	for _, name := range order {
		p := sch.Properties[name]
		typ := "any"
		desc := ""
		if p != nil {
			typ = string(p.Kind)
			desc = p.Description
		}
		req := ""
		if reqSet[name] {
			req = ", required"
		}
		line := fmt.Sprintf("%s: %s%s", name, typ, req)
		if desc != "" {
			line += " — " + desc
		}
		lines = append(lines, line)
	}
	return lines
}

// primaryResponse picks the first 2xx response, falling back to the first
// response of any status.
func primaryResponse(op domain.Operation) *domain.Response {
	for i := range op.Responses {
		if strings.HasPrefix(op.Responses[i].Status, "2") {
			return &op.Responses[i]
		}
	}
	if len(op.Responses) > 0 {
		return &op.Responses[0]
	}
	return nil
}

// securityLines renders an operation's security requirements.
func securityLines(op domain.Operation) []string {
	var lines []string
	for _, sec := range op.Security {
		line := fmt.Sprintf("%s (%s)", sec.Scheme, sec.Type)
		if len(sec.Scopes) > 0 {
			line += " scopes=" + strings.Join(sec.Scopes, ",")
		}
		lines = append(lines, line)
	}
	return lines
}

// bodyCapBytes is the cap PLAN §23.3 imposes on response bodies embedded in
// tool results (execute_api, run_flow). get_run(include_bodies=true) is
// exempt: it is the "more" endpoint the cap points to.
const bodyCapBytes = 16 * 1024

// capBody returns v (JSON-marshaled and re-parsed so map/slice shapes are
// consistent) unless it is larger than bodyCapBytes, in which case it
// returns a truncated placeholder string and true.
func capBody(v any) (any, bool) {
	if v == nil {
		return nil, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v, false
	}
	if len(b) <= bodyCapBytes {
		return v, false
	}
	return string(b[:bodyCapBytes]) + "...[truncated; use get_run for the full body]", true
}

// FlatField is one flattened field of a standalone named schema (as opposed
// to domain.Field, which is flattened relative to an operation's request and
// response).
type FlatField struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// flattenSchema appends one FlatField per leaf (and per composite/object
// node with a non-empty path) reachable from s, rooted at path.
func flattenSchema(path string, s *domain.Schema, required bool, depth int, out *[]FlatField) {
	if s == nil || depth > 8 {
		return
	}
	switch s.Kind {
	case domain.KindObject:
		if path != "" {
			*out = append(*out, FlatField{Path: path, Type: "object", Description: s.Description, Required: required})
		}
		reqSet := make(map[string]bool, len(s.Required))
		for _, r := range s.Required {
			reqSet[r] = true
		}
		order := s.PropertyOrder
		if len(order) == 0 {
			for name := range s.Properties {
				order = append(order, name)
			}
		}
		for _, name := range order {
			prop := s.Properties[name]
			childPath := name
			if path != "" {
				childPath = path + "." + name
			}
			flattenSchema(childPath, prop, reqSet[name], depth+1, out)
		}
	case domain.KindArray:
		*out = append(*out, FlatField{Path: path, Type: "array", Description: s.Description, Required: required})
		if s.Items != nil {
			flattenSchema(path+"[]", s.Items, false, depth+1, out)
		}
	case domain.KindOneOf, domain.KindAnyOf, domain.KindAllOf:
		*out = append(*out, FlatField{Path: path, Type: string(s.Kind), Description: s.Description, Required: required})
		for i, v := range s.Variants {
			flattenSchema(fmt.Sprintf("%s.#%d", path, i), v, false, depth+1, out)
		}
	case domain.KindRef:
		*out = append(*out, FlatField{Path: path, Type: "ref:" + s.Ref, Description: s.Description, Required: required})
	default:
		typ := string(s.Kind)
		if s.Format != "" {
			typ += ":" + s.Format
		}
		*out = append(*out, FlatField{Path: path, Type: typ, Description: s.Description, Required: required})
	}
}

// RequestView is the redacted request of one run step.
type RequestView struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`
}

// ResponseView is the redacted, possibly-capped response of one run step.
type ResponseView struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      any               `json:"body,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

// StepView is the compact rendering of one run step.
type StepView struct {
	StepID     string                   `json:"step_id"`
	Operation  string                   `json:"operation,omitempty"`
	Status     string                   `json:"status"`
	Request    *RequestView             `json:"request,omitempty"`
	Response   *ResponseView            `json:"response,omitempty"`
	Assertions []domain.AssertionResult `json:"assertions,omitempty"`
	Out        map[string]any           `json:"out,omitempty"`
	Error      *domain.ErrorInfo        `json:"error,omitempty"`
	// Phase is "setup", "steps", or "teardown" (empty means "steps").
	Phase string `json:"phase,omitempty"`
	// Reused marks a step whose result was copied from an earlier run
	// (engine.RunOptions.ResumeFrom) instead of being executed.
	Reused bool `json:"reused,omitempty"`
	// Warnings carries non-fatal notes such as "step definition changed
	// since the reused run".
	Warnings []string `json:"warnings,omitempty"`
}

// RunView is the compact rendering of a run, used by execute_api, run_flow,
// and get_run.
type RunView struct {
	ID          string            `json:"id"`
	FlowID      string            `json:"flow_id,omitempty"`
	Environment string            `json:"environment"`
	Status      string            `json:"status"`
	Summary     domain.RunSummary `json:"summary"`
	Steps       []StepView        `json:"steps"`
	Error       *domain.ErrorInfo `json:"error,omitempty"`
	// ResumedFrom is the earlier run whose results this run reused.
	ResumedFrom string `json:"resumed_from,omitempty"`
}

// buildRunView renders run into a RunView. If stepFilter is non-empty, only
// that step is included. If includeBodies is false, request/response bodies
// are stripped entirely (get_run's default). If capBodies is true, bodies
// that survive are capped at bodyCapBytes (execute_api, run_flow, and
// get_run(include_bodies=true) all cap; only get_run's explicit include is
// exempt, controlled by the caller).
func buildRunView(run *domain.Run, stepFilter string, includeBodies, capBodies bool) RunView {
	rv := RunView{
		ID:          run.ID,
		ResumedFrom: run.ResumedFrom,
		FlowID:      run.FlowID,
		Environment: run.Environment,
		Status:      string(run.Status),
		Summary:     run.Summary,
		Error:       run.Error,
	}
	for _, st := range run.Steps {
		if stepFilter != "" && st.StepID != stepFilter {
			continue
		}
		sv := StepView{
			StepID:     st.StepID,
			Operation:  st.Operation,
			Status:     string(st.Status),
			Assertions: st.Assertions,
			Out:        st.Out,
			Error:      st.Error,
			Phase:      st.Phase,
			Reused:     st.Reused,
			Warnings:   st.Warnings,
		}
		if includeBodies {
			if st.Request != nil {
				body := st.Request.Body
				if capBodies {
					body, _ = capBody(body)
				}
				sv.Request = &RequestView{Method: st.Request.Method, URL: st.Request.URL, Headers: st.Request.Headers, Body: body}
			}
			if st.Response != nil {
				body := st.Response.Body
				truncated := st.Response.Truncated
				if capBodies {
					var t bool
					body, t = capBody(body)
					truncated = truncated || t
				}
				sv.Response = &ResponseView{Status: st.Response.Status, Headers: st.Response.Headers, Body: body, Truncated: truncated}
			}
		}
		rv.Steps = append(rv.Steps, sv)
	}
	return rv
}

// renderRunText is the compact Markdown-ish rendering of a RunView.
func renderRunText(rv RunView) string {
	s := fmt.Sprintf("run %s: %s (%d/%d steps passed)\n", rv.ID, rv.Status, rv.Summary.StepsPassed, rv.Summary.StepsTotal)
	for _, st := range rv.Steps {
		s += fmt.Sprintf("- %s (%s): %s", st.StepID, st.Operation, st.Status)
		if st.Reused {
			s += " (reused)"
		}
		if st.Response != nil {
			s += fmt.Sprintf(" -> %d", st.Response.Status)
		}
		if st.Error != nil {
			s += fmt.Sprintf(" error=%s: %s", st.Error.Code, st.Error.Message)
		}
		for _, w := range st.Warnings {
			s += fmt.Sprintf("\n  warning: %s", w)
		}
		for _, a := range st.Assertions {
			if a.Passed {
				continue
			}
			if a.Soft {
				s += fmt.Sprintf("\n  soft mismatch: %s (%s)", a.Expr, a.Message)
			} else {
				s += fmt.Sprintf("\n  assertion failed: %s (%s)", a.Expr, a.Message)
			}
		}
		s += "\n"
	}
	return s
}
