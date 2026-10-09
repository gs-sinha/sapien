package flow

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
)

// UI steps.
const (
	CodeUIShape  = "UI_SHAPE"  // a ui step also sets call-step or block fields
	CodeUIAction = "UI_ACTION" // a ui action is unknown or has the wrong arguments for its kind
)

// uiNeedsTarget are the action kinds that act on one element.
var uiNeedsTarget = map[string]bool{
	domain.UITap: true, domain.UIType: true, domain.UIClear: true, domain.UIScrollTo: true,
	domain.UIWaitFor: true, domain.UIRead: true, domain.UIAssertVisible: true,
	domain.UIAssertNotVisible: true, domain.UIAssertText: true,
}

var uiOutNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// checkUIStep validates a ui step's shape and each of its actions: no
// call-step-only fields (call/example/input/params/body/headers/until/poll),
// a known kind per action, a single selector where one is needed, and the
// kind's own required arguments.
func checkUIStep(st domain.Step) []domain.Diagnostic {
	var diags []domain.Diagnostic
	shape := func(format string, args ...any) {
		diags = append(diags, domain.Diagnostic{
			Code: CodeUIShape, Severity: domain.SeverityError,
			Message: fmt.Sprintf(format, args...), Line: st.Line, StepID: st.ID,
		})
	}
	if st.Call != "" || st.Example != "" {
		shape("ui step `%s` sets `call`/`example`; a ui step drives an app, not an operation", st.ID)
	}
	if len(st.Input) > 0 || st.Params != nil || st.Body != nil || len(st.Headers) > 0 || st.Until != "" || st.Poll != nil {
		shape("ui step `%s` sets a request field (input/params/body/headers/until/poll); use ui actions like wait_for instead", st.ID)
	}
	if st.UI.App == "" {
		shape("ui step `%s` needs `ui.app`, the name of an app in the environment's `apps:`", st.ID)
	}
	if len(st.UI.Actions) == 0 {
		shape("ui step `%s` has no `ui.actions`", st.ID)
	}

	seenAs := map[string]bool{}
	for i, a := range st.UI.Actions {
		line := a.Line
		if line == 0 {
			line = st.Line
		}
		bad := func(format string, args ...any) {
			diags = append(diags, domain.Diagnostic{
				Code: CodeUIAction, Severity: domain.SeverityError,
				Message: fmt.Sprintf("ui step `%s` action %d (%s): ", st.ID, i+1, a.Kind) + fmt.Sprintf(format, args...),
				Line:    line, StepID: st.ID,
			})
		}
		if !isUIKind(a.Kind) {
			diags = append(diags, domain.Diagnostic{
				Code: CodeUIAction, Severity: domain.SeverityError,
				Message: fmt.Sprintf("ui step `%s` action %d: unknown action `%s`", st.ID, i+1, a.Kind),
				Line:    line, StepID: st.ID, Suggestions: domain.UIActionKinds(),
			})
			continue
		}
		if set := a.Target.Set(); len(set) > 1 {
			bad("set exactly one of id/text/text_contains/hint/xpath, got %s", strings.Join(set, ", "))
		}
		if uiNeedsTarget[a.Kind] && a.Target.IsZero() {
			if a.Kind == domain.UIType {
				bad("needs the element to type into: `id:`/`hint:`/`text_contains:`/`xpath:` inline, or `into: {text: ...}`")
			} else {
				bad("needs an element selector (id/text/text_contains/hint/xpath)")
			}
		}
		// swipe takes an optional selector: with one it drags across that
		// element (a slide-to-confirm button), without one across the screen.
		if !uiNeedsTarget[a.Kind] && a.Kind != domain.UISwipe && !a.Target.IsZero() {
			bad("takes no element selector")
		}
		switch a.Kind {
		case domain.UIDeeplink:
			if a.URL == "" {
				bad("needs `url`")
			}
		case domain.UIType:
			if a.Text == "" {
				bad("needs `text` to type")
			}
		case domain.UISwipe:
			if a.Direction == "" {
				bad("needs `direction` (up/down/left/right)")
			}
		case domain.UIRead:
			switch {
			case a.As == "":
				bad("needs `as`, the out.<name> that receives the element's text")
			case !uiOutNamePattern.MatchString(a.As):
				bad("`as: %s` must be an identifier", a.As)
			case seenAs[a.As]:
				bad("`as: %s` is read more than once in this step", a.As)
			case st.Extract[a.As] != "":
				bad("`as: %s` collides with an extract of the same name", a.As)
			}
			seenAs[a.As] = true
		case domain.UIAssertText:
			n := 0
			for _, v := range []string{a.Eq, a.Contains, a.Matches} {
				if v != "" {
					n++
				}
			}
			if n != 1 {
				bad("set exactly one of eq/contains/matches")
			}
			if a.Matches != "" {
				if _, err := regexp.Compile(a.Matches); err != nil {
					bad("invalid matches regex: %v", err)
				}
			}
		}
		if a.Timeout != "" {
			if _, err := time.ParseDuration(a.Timeout); err != nil {
				diags = append(diags, domain.Diagnostic{
					Code: CodeInvalidDuration, Severity: domain.SeverityError,
					Message: fmt.Sprintf("invalid timeout `%s` on ui step `%s` action %d: %v", a.Timeout, st.ID, i+1, err),
					Line:    line, StepID: st.ID,
				})
			}
		}
	}
	return diags
}

func isUIKind(k string) bool {
	for _, kind := range domain.UIActionKinds() {
		if kind == k {
			return true
		}
	}
	return false
}

// uiReadNames returns the out.<name>s a ui step's `read` actions produce.
func uiReadNames(st domain.Step) []string {
	if st.UI == nil {
		return nil
	}
	var out []string
	for _, a := range st.UI.Actions {
		if a.Kind == domain.UIRead && a.As != "" {
			out = append(out, a.As)
		}
	}
	return out
}

// uiActionTemplates returns every string in a ui step's actions that may
// carry a `${...}` template, paired with the field name for diagnostics.
func uiActionTemplates(st domain.Step) map[string][]string {
	if st.UI == nil {
		return nil
	}
	out := map[string][]string{}
	add := func(field, v string) {
		if v != "" {
			out[field] = append(out[field], v)
		}
	}
	for i, a := range st.UI.Actions {
		f := fmt.Sprintf("ui.actions[%d].", i)
		add(f+"url", a.URL)
		add(f+"text", a.Text)
		add(f+"eq", a.Eq)
		add(f+"contains", a.Contains)
		add(f+"matches", a.Matches)
		if t := a.Target; t != nil {
			add(f+"id", t.ID)
			add(f+"text", t.Text)
			add(f+"text_contains", t.TextContains)
			add(f+"hint", t.Hint)
			add(f+"xpath", t.XPath)
		}
	}
	return out
}
