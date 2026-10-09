package domain

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// UIStep drives a mobile app on a local device instead of calling an
// operation (a `ui:` step). App names an entry in the environment's `apps:`
// map; Actions run in order against one device session that lives for the
// whole run, so a later ui step continues from the screen an earlier one left.
type UIStep struct {
	App     string     `yaml:"app" json:"app"`
	Actions []UIAction `yaml:"actions" json:"actions"`
}

// UI action kinds. Each action in a flow is a one-key mapping whose key is
// one of these (`- tap: {id: login_button}`), or a bare string for the
// kinds that take no arguments (`- back`).
const (
	UILaunch           = "launch"
	UIStop             = "stop"
	UIDeeplink         = "deeplink"
	UITap              = "tap"
	UIType             = "type"
	UIClear            = "clear"
	UISwipe            = "swipe"
	UIScrollTo         = "scroll_to"
	UIBack             = "back"
	UIHideKeyboard     = "hide_keyboard"
	UIWaitFor          = "wait_for"
	UIRead             = "read"
	UIScreenshot       = "screenshot"
	UIAssertVisible    = "assert_visible"
	UIAssertNotVisible = "assert_not_visible"
	UIAssertText       = "assert_text"
)

// uiArgless are the kinds that may be written as a bare string.
var uiArgless = map[string]bool{UIStop: true, UIBack: true, UILaunch: true, UIScreenshot: true, UIHideKeyboard: true}

// UIActionKinds lists every action kind, sorted, for diagnostics.
func UIActionKinds() []string {
	out := []string{UILaunch, UIStop, UIDeeplink, UITap, UIType, UIClear, UISwipe, UIScrollTo,
		UIBack, UIHideKeyboard, UIWaitFor, UIRead, UIScreenshot, UIAssertVisible, UIAssertNotVisible, UIAssertText}
	sort.Strings(out)
	return out
}

// Selector finds one element on screen. Exactly one of ID/Text/TextContains/
// Hint/XPath is set; Index picks the n-th match (0-based) when several match.
//
// ID matches a Flutter `Semantics(identifier: ...)` (Android resource-id) or,
// failing that, an accessibility label -- the string rider_app_fe's
// AutomationLabel writes.
type Selector struct {
	ID           string `yaml:"id,omitempty" json:"id,omitempty"`
	Text         string `yaml:"text,omitempty" json:"text,omitempty"`
	TextContains string `yaml:"text_contains,omitempty" json:"text_contains,omitempty"`
	Hint         string `yaml:"hint,omitempty" json:"hint,omitempty"`
	XPath        string `yaml:"xpath,omitempty" json:"xpath,omitempty"`
	Index        int    `yaml:"index,omitempty" json:"index,omitempty"`
}

// IsZero reports whether no locator field is set.
func (s *Selector) IsZero() bool { return s == nil || len(s.Set()) == 0 }

// Set lists the locator fields that are set (more than one is invalid).
func (s *Selector) Set() []string {
	if s == nil {
		return nil
	}
	var out []string
	if s.ID != "" {
		out = append(out, "id")
	}
	if s.Text != "" {
		out = append(out, "text")
	}
	if s.TextContains != "" {
		out = append(out, "text_contains")
	}
	if s.Hint != "" {
		out = append(out, "hint")
	}
	if s.XPath != "" {
		out = append(out, "xpath")
	}
	return out
}

// String renders the selector for logs and diagnostics, e.g. `id=login`.
func (s *Selector) String() string {
	if s == nil {
		return ""
	}
	var v string
	switch {
	case s.ID != "":
		v = "id=" + s.ID
	case s.Text != "":
		v = "text=" + s.Text
	case s.TextContains != "":
		v = "text_contains=" + s.TextContains
	case s.Hint != "":
		v = "hint=" + s.Hint
	case s.XPath != "":
		v = "xpath=" + s.XPath
	}
	if s.Index > 0 {
		v += fmt.Sprintf("[%d]", s.Index)
	}
	return v
}

// UIAction is one decoded action. Kind says which; the remaining fields are
// the union of every kind's arguments (validated per kind by the flow
// validator). Target is the element most kinds act on; it is written inline
// with the arguments in YAML (`tap: {id: x}`, `type: {id: x, text: hi}`).
type UIAction struct {
	Kind   string    `json:"kind"`
	Target *Selector `json:"target,omitempty"`

	// launch
	ClearState       bool  `json:"clear_state,omitempty"`
	GrantPermissions *bool `json:"grant_permissions,omitempty"`
	// deeplink
	URL string `json:"url,omitempty"`
	// type
	Text  string `json:"text,omitempty"`
	Clear bool   `json:"clear,omitempty"`
	// swipe / scroll_to
	Direction string `json:"direction,omitempty"` // up|down|left|right
	MaxSwipes int    `json:"max_swipes,omitempty"`
	// wait_for / assert_* / scroll_to / tap / ...: how long to keep looking
	Timeout string `json:"timeout,omitempty"`
	// wait_for: wait for the element to disappear instead
	Gone bool `json:"gone,omitempty"`
	// read: out.<As> receives the element's text
	As string `json:"as,omitempty"`
	// screenshot: artifact name
	Name string `json:"name,omitempty"`
	// assert_text
	Eq       string `json:"eq,omitempty"`
	Contains string `json:"contains,omitempty"`
	Matches  string `json:"matches,omitempty"`
	Message  string `json:"message,omitempty"`

	Line int `json:"line,omitempty"`
}

// uiActionArgs is the YAML shape of an action's argument mapping: selector
// fields inline next to the kind-specific ones.
type uiActionArgs struct {
	Selector         `yaml:",inline"`
	ClearState       bool      `yaml:"clear_state,omitempty"`
	GrantPermissions *bool     `yaml:"grant_permissions,omitempty"`
	URL              string    `yaml:"url,omitempty"`
	Into             *Selector `yaml:"into,omitempty"`
	Clear            bool      `yaml:"clear,omitempty"`
	Direction        string    `yaml:"direction,omitempty"`
	MaxSwipes        int       `yaml:"max_swipes,omitempty"`
	Timeout          string    `yaml:"timeout,omitempty"`
	Gone             bool      `yaml:"gone,omitempty"`
	As               string    `yaml:"as,omitempty"`
	Name             string    `yaml:"name,omitempty"`
	Eq               string    `yaml:"eq,omitempty"`
	Contains         string    `yaml:"contains,omitempty"`
	Matches          string    `yaml:"matches,omitempty"`
	Message          string    `yaml:"message,omitempty"`
}

// UnmarshalYAML decodes `- back` or `- tap: {id: x}`. For `type`, the text
// field's own `text:` collides with the text selector, so the element to
// type into is given as `into: {text: ...}` (or inline `id:`/`hint:`/...);
// for `deeplink`, a bare string value is the URL; for `screenshot`, the
// name.
func (a *UIAction) UnmarshalYAML(value *yaml.Node) error {
	a.Line = value.Line
	if value.Kind == yaml.ScalarNode {
		if !uiArgless[value.Value] {
			return fmt.Errorf("line %d: ui action `%s` needs arguments (or is not one of %s)", value.Line, value.Value, strings.Join(UIActionKinds(), ", "))
		}
		a.Kind = value.Value
		return nil
	}
	if value.Kind != yaml.MappingNode || len(value.Content) != 2 {
		return fmt.Errorf("line %d: a ui action is a one-key mapping like `tap: {id: x}`", value.Line)
	}
	a.Kind = value.Content[0].Value
	arg := value.Content[1]
	if arg.Kind == yaml.ScalarNode {
		switch a.Kind {
		case UIDeeplink:
			a.URL = arg.Value
		case UIScreenshot:
			a.Name = arg.Value
		case UIBack, UIStop, UILaunch, UIHideKeyboard:
		default:
			return fmt.Errorf("line %d: ui action `%s` takes a mapping of arguments", value.Line, a.Kind)
		}
		return nil
	}
	var args uiActionArgs
	if err := arg.Decode(&args); err != nil {
		return err
	}
	sel := args.Selector
	if a.Kind == UIType {
		// For type, `text:` is what to type; the target is `into:` or the
		// inline id/hint/text_contains/xpath.
		a.Text = sel.Text
		sel.Text = ""
		if args.Into != nil {
			sel = *args.Into
		}
	}
	if !sel.IsZero() {
		s := sel
		a.Target = &s
	}
	a.ClearState = args.ClearState
	a.GrantPermissions = args.GrantPermissions
	a.URL = args.URL
	a.Clear = args.Clear
	a.Direction = args.Direction
	a.MaxSwipes = args.MaxSwipes
	a.Timeout = args.Timeout
	a.Gone = args.Gone
	a.As = args.As
	a.Name = args.Name
	a.Eq = args.Eq
	a.Contains = args.Contains
	a.Matches = args.Matches
	a.Message = args.Message
	return nil
}

// AppConfig is one entry of an environment's `apps:` map: how to obtain,
// install, and observe a mobile app for ui steps.
type AppConfig struct {
	Platform string `yaml:"platform,omitempty" json:"platform,omitempty"` // android (default; the only one today)
	Package  string `yaml:"package" json:"package"`
	// Activity is the launcher activity; empty resolves it from the device.
	Activity string `yaml:"activity,omitempty" json:"activity,omitempty"`
	// Repo is the app's source checkout, relative to the workspace root (or
	// absolute); Build runs there, and APK is relative to it.
	Repo  string `yaml:"repo,omitempty" json:"repo,omitempty"`
	Build string `yaml:"build,omitempty" json:"build,omitempty"`
	APK   string `yaml:"apk,omitempty" json:"apk,omitempty"`
	// LogFormat picks the logcat parser that turns the app's network logs
	// into body.logs.api records: rider-box, overwatch-api-logger, or raw.
	LogFormat string `yaml:"log_format,omitempty" json:"log_format,omitempty"`
	// LogTag filters logcat to this tag (default "flutter").
	LogTag      string   `yaml:"log_tag,omitempty" json:"log_tag,omitempty"`
	Permissions []string `yaml:"permissions,omitempty" json:"permissions,omitempty"`
}

// StepArtifact is a file a step left behind (a screenshot, a logcat slice,
// the screen's page source), stored under the workspace's
// .sapien/artifacts/<run_id>/<step_id>/.
type StepArtifact struct {
	Kind string `json:"kind"` // screenshot | logcat | source
	Name string `json:"name"`
	Path string `json:"path"`
}
