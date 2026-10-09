package device

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/device/appium"
	"github.com/gs-sinha/sapien/internal/device/logs"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// DefaultActionTimeout bounds how long an action looks for its element.
const DefaultActionTimeout = 10 * time.Second

// ErrUIAction is the error code of an action that could not be carried out
// (element never appeared, tap rejected, ...).
const ErrUIAction errs.Code = "UI_ACTION_FAILED"

type appSession struct {
	m      *Manager
	name   string
	cfg    domain.AppConfig
	attach bool
	logBuf *logs.Buffer
}

func (s *appSession) sess() *appium.Session { return s.m.session }

// LogMark implements App.
func (s *appSession) LogMark() int { return s.logBuf.Mark() }

// Logs implements App.
func (s *appSession) Logs(since int) ([]string, []map[string]any) {
	raw := s.logBuf.Since(since)
	calls := logs.Parse(s.cfg.LogFormat, raw)
	api := make([]map[string]any, len(calls))
	for i, c := range calls {
		api[i] = c.Map()
	}
	return logs.CleanLines(raw), api
}

// Screenshot implements App.
func (s *appSession) Screenshot(ctx context.Context) ([]byte, error) { return s.sess().Screenshot(ctx) }

// Source implements App.
func (s *appSession) Source(ctx context.Context) (string, error) { return s.sess().Source(ctx) }

// Activity implements App.
func (s *appSession) Activity(ctx context.Context) string { return s.m.adb.ForegroundActivity(ctx) }

func actionTimeout(a domain.UIAction) time.Duration {
	if a.Timeout != "" {
		if d, err := time.ParseDuration(a.Timeout); err == nil {
			return d
		}
	}
	return DefaultActionTimeout
}

func failf(format string, args ...any) error {
	return errs.New(ErrUIAction, format, args...)
}

// Do implements App.
func (s *appSession) Do(ctx context.Context, a domain.UIAction) (Outcome, error) {
	sess := s.sess()
	pkg := s.cfg.Package
	switch a.Kind {
	case domain.UILaunch:
		var out Outcome
		if a.ClearState {
			if s.attach {
				out.Warning = fmt.Sprintf("launch: clear_state skipped, app %q is attached (clearing would kill your flutter run)", s.name)
			} else {
				_ = sess.Mobile(ctx, "terminateApp", map[string]any{"appId": pkg}, nil)
				if err := s.m.adb.Clear(ctx, pkg); err != nil {
					return out, failf("launch: clearing %s: %v", pkg, err)
				}
			}
		}
		if a.GrantPermissions == nil || *a.GrantPermissions {
			s.m.adb.Grant(ctx, pkg, s.cfg.Permissions)
		}
		if err := sess.Mobile(ctx, "activateApp", map[string]any{"appId": pkg}, nil); err != nil {
			return out, failf("launch: activating %s: %v", pkg, err)
		}
		return out, nil

	case domain.UIStop:
		if err := sess.Mobile(ctx, "terminateApp", map[string]any{"appId": pkg}, nil); err != nil {
			return Outcome{}, failf("stop: %v", err)
		}
		return Outcome{}, nil

	case domain.UIDeeplink:
		if err := sess.Mobile(ctx, "deepLink", map[string]any{"url": a.URL, "package": pkg}, nil); err != nil {
			return Outcome{}, failf("deeplink %s: %v", a.URL, err)
		}
		return Outcome{}, nil

	case domain.UIHideKeyboard:
		// A keyboard that is already down is the state asked for, not a failure.
		if err := sess.Mobile(ctx, "hideKeyboard", map[string]any{}, nil); err != nil && !strings.Contains(strings.ToLower(err.Error()), "keyboard") {
			return Outcome{}, failf("hide_keyboard: %v", err)
		}
		return Outcome{}, nil

	case domain.UIBack:
		if err := sess.Back(ctx); err != nil {
			return Outcome{}, failf("back: %v", err)
		}
		return Outcome{}, nil

	case domain.UITap:
		err := s.withElement(ctx, a.Target, actionTimeout(a), func(el string) error {
			return sess.Click(ctx, el)
		})
		if err != nil {
			return Outcome{}, s.actionErr("tap", a.Target, err)
		}
		return Outcome{}, nil

	case domain.UIType:
		err := s.withElement(ctx, a.Target, actionTimeout(a), func(el string) error {
			// A disabled field accepts nothing but reports no error either, so
			// typing into one would "pass" and fail somewhere less clear later.
			if en, err := sess.Attribute(ctx, el, "enabled"); err == nil && en == "false" {
				return failf("type into %s: the field is disabled", a.Target)
			}
			// Focus first: a Flutter TextField only takes text while focused.
			if err := sess.Click(ctx, el); err != nil {
				return err
			}
			if a.Clear {
				if err := sess.Clear(ctx, el); err != nil {
					return err
				}
			}
			return sess.SendKeys(ctx, el, a.Text)
		})
		if err != nil {
			return Outcome{}, s.actionErr("type into", a.Target, err)
		}
		return Outcome{}, nil

	case domain.UIClear:
		err := s.withElement(ctx, a.Target, actionTimeout(a), func(el string) error {
			return sess.Clear(ctx, el)
		})
		if err != nil {
			return Outcome{}, s.actionErr("clear", a.Target, err)
		}
		return Outcome{}, nil

	case domain.UISwipe:
		if a.Target != nil {
			// Across one element: a slide-to-confirm button wants nearly its
			// full width at an unhurried speed, or it springs back.
			el, err := s.find(ctx, a.Target, actionTimeout(a))
			if err != nil {
				return Outcome{}, err
			}
			args := map[string]any{"elementId": el, "direction": a.Direction, "percent": 0.95, "speed": 1500}
			if err := sess.Mobile(ctx, "swipeGesture", args, nil); err != nil {
				return Outcome{}, failf("swipe %s across %s: %v", a.Direction, a.Target, err)
			}
			return Outcome{}, nil
		}
		r, err := s.region(ctx)
		if err != nil {
			return Outcome{}, err
		}
		args := r
		args["direction"] = a.Direction
		args["percent"] = 0.75
		if err := sess.Mobile(ctx, "swipeGesture", args, nil); err != nil {
			return Outcome{}, failf("swipe %s: %v", a.Direction, err)
		}
		return Outcome{}, nil

	case domain.UIScrollTo:
		dir := a.Direction
		if dir == "" {
			dir = "down"
		}
		max := a.MaxSwipes
		if max <= 0 {
			max = 10
		}
		r, err := s.region(ctx)
		if err != nil {
			return Outcome{}, err
		}
		for i := 0; i <= max; i++ {
			if ids, _ := s.findNow(ctx, a.Target); len(ids) > 0 {
				return Outcome{}, nil
			}
			if i == max {
				break
			}
			args := map[string]any{}
			for k, v := range r {
				args[k] = v
			}
			args["direction"] = dir
			args["percent"] = 0.7
			_ = sess.Mobile(ctx, "scrollGesture", args, nil)
		}
		return Outcome{}, failf("scroll_to %s: not found after %d swipes %s", a.Target, max, dir)

	case domain.UIWaitFor:
		if a.Gone {
			if !s.waitGone(ctx, a.Target, actionTimeout(a)) {
				return Outcome{}, failf("wait_for %s gone: still on screen after %s", a.Target, actionTimeout(a))
			}
			return Outcome{}, nil
		}
		_, err := s.find(ctx, a.Target, actionTimeout(a))
		return Outcome{}, err

	case domain.UIRead:
		var text string
		err := s.withElement(ctx, a.Target, actionTimeout(a), func(el string) error {
			t, err := s.textErr(ctx, el)
			text = t
			return err
		})
		if err != nil {
			return Outcome{}, s.actionErr("read", a.Target, err)
		}
		return Outcome{Text: text}, nil

	case domain.UIScreenshot:
		png, err := sess.Screenshot(ctx)
		if err != nil {
			return Outcome{}, failf("screenshot: %v", err)
		}
		return Outcome{PNG: png}, nil

	case domain.UIAssertVisible:
		_, err := s.find(ctx, a.Target, actionTimeout(a))
		as := &Assertion{Passed: err == nil, Expr: "visible(" + a.Target.String() + ")", Message: a.Message}
		if err != nil {
			as.Actual = "not on screen after " + actionTimeout(a).String()
		}
		return Outcome{Assert: as}, nil

	case domain.UIAssertNotVisible:
		ok := s.waitGone(ctx, a.Target, actionTimeout(a))
		as := &Assertion{Passed: ok, Expr: "not_visible(" + a.Target.String() + ")", Message: a.Message}
		if !ok {
			as.Actual = "still on screen after " + actionTimeout(a).String()
		}
		return Outcome{Assert: as}, nil

	case domain.UIAssertText:
		return Outcome{Assert: s.assertText(ctx, a)}, nil
	}
	return Outcome{}, failf("unknown ui action %q", a.Kind)
}

// assertText polls the element's text until it matches or the timeout ends.
func (s *appSession) assertText(ctx context.Context, a domain.UIAction) *Assertion {
	var op, want string
	var match func(string) bool
	switch {
	case a.Eq != "":
		op, want, match = "==", a.Eq, func(t string) bool { return t == a.Eq }
	case a.Contains != "":
		op, want, match = "contains", a.Contains, func(t string) bool { return strings.Contains(t, a.Contains) }
	default:
		re, err := regexp.Compile(a.Matches)
		if err != nil {
			return &Assertion{Expr: "text(" + a.Target.String() + ") matches " + a.Matches, Actual: err.Error(), Message: a.Message}
		}
		op, want, match = "matches", a.Matches, re.MatchString
	}
	as := &Assertion{Expr: fmt.Sprintf("text(%s) %s %q", a.Target, op, want), Message: a.Message}
	deadline := time.Now().Add(actionTimeout(a))
	for {
		if ids, _ := s.findNow(ctx, a.Target); len(ids) > 0 {
			t := s.text(ctx, ids[0])
			as.Actual = t
			if match(t) {
				as.Passed = true
				return as
			}
		} else {
			as.Actual = "element not on screen"
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return as
		}
		s.sleep(ctx)
	}
}

// text returns an element's text, falling back to its content-desc (a
// Flutter Text widget often surfaces as a label, not as text), and then to
// its descendants' text joined by spaces: an id on its own semantics node
// (AutomationLabel with isolateChildren) carries no text itself, its
// child does.
func (s *appSession) text(ctx context.Context, el string) string {
	t, _ := s.textErr(ctx, el)
	return t
}

// textErr is text, reporting a stale element so read can retry it.
func (s *appSession) textErr(ctx context.Context, el string) (string, error) {
	t, err := s.sess().Text(ctx, el)
	if err != nil && isStale(err) {
		return "", err
	}
	if t != "" && t != "null" {
		return t, nil
	}
	if t := s.ownText(ctx, el); t != "" {
		return t, nil
	}
	kids, err := s.sess().FindChildElements(ctx, el, "xpath", ".//*")
	if err != nil {
		return "", nil
	}
	var parts []string
	for _, k := range kids {
		if t := s.ownText(ctx, k); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " "), nil
}

func (s *appSession) ownText(ctx context.Context, el string) string {
	if t, err := s.sess().Text(ctx, el); err == nil && t != "" && t != "null" {
		return t
	}
	t, _ := s.sess().Attribute(ctx, el, "content-desc")
	if t == "null" {
		// UiAutomator2 reports a text-less node's text as the string "null".
		return ""
	}
	return t
}

func (s *appSession) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(s.m.opts.Poll):
	}
}

// withElement finds sel and runs fn on it. A Flutter screen that rebuilds
// between the lookup and the action (an animating carousel, a list that
// refreshes) leaves the id stale; that is retried with a fresh lookup until
// timeout rather than failing the step.
func (s *appSession) withElement(ctx context.Context, sel *domain.Selector, timeout time.Duration, fn func(el string) error) error {
	deadline := time.Now().Add(timeout)
	for {
		el, err := s.find(ctx, sel, time.Until(deadline))
		if err != nil {
			return err
		}
		err = fn(el)
		if err == nil || !isStale(err) || time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		s.sleep(ctx)
	}
}

// actionErr wraps an action's failure, keeping an already-wrapped UI error
// (element not found, field disabled) as it is.
func (s *appSession) actionErr(verb string, sel *domain.Selector, err error) error {
	if c := errs.CodeOf(err); c == ErrUIAction || c == errs.Cancelled {
		return err
	}
	return failf("%s %s: %v", verb, sel, err)
}

func isStale(err error) bool {
	var we *appium.Error
	return errors.As(err, &we) && we.Code == "stale element reference"
}

// find polls until sel matches, returning the Index-th match.
func (s *appSession) find(ctx context.Context, sel *domain.Selector, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ids, err := s.findNow(ctx, sel)
		if err != nil {
			lastErr = err
		}
		if len(ids) > 0 {
			return ids[0], nil
		}
		if ctx.Err() != nil {
			return "", errs.Wrap(errs.Cancelled, ctx.Err(), "looking for %s", sel)
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return "", failf("element %s not found within %s: %v", sel, timeout, lastErr)
			}
			return "", failf("element %s not found within %s", sel, timeout)
		}
		s.sleep(ctx)
	}
}

// waitGone polls until sel no longer matches.
func (s *appSession) waitGone(ctx context.Context, sel *domain.Selector, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		ids, err := s.findNow(ctx, sel)
		if err == nil && len(ids) == 0 {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		s.sleep(ctx)
	}
}

// findNow tries each of sel's strategies once; it returns the Index-th
// match (as a one-element slice) of the first strategy that has one.
func (s *appSession) findNow(ctx context.Context, sel *domain.Selector) ([]string, error) {
	var lastErr error
	for _, st := range Strategies(sel) {
		ids, err := s.sess().FindElements(ctx, st.Using, st.Value)
		if err != nil {
			lastErr = err
			continue
		}
		if len(ids) > sel.Index {
			return []string{ids[sel.Index]}, nil
		}
	}
	return nil, lastErr
}

// region is the screen minus a 10% margin, as Appium gesture arguments.
func (s *appSession) region(ctx context.Context) (map[string]any, error) {
	r, err := s.sess().WindowRect(ctx)
	if err != nil {
		return nil, failf("reading screen size: %v", err)
	}
	return map[string]any{
		"left": r.Width / 10, "top": r.Height / 10,
		"width": r.Width * 8 / 10, "height": r.Height * 8 / 10,
	}, nil
}

// Strategy is one WebDriver locator.
type Strategy struct{ Using, Value string }

// Strategies maps a selector to the locators tried in order. Flutter puts
// a Semantics identifier in resource-id, and many widgets' text in
// content-desc rather than text, so id and text each try both.
func Strategies(sel *domain.Selector) []Strategy {
	ua := func(expr string) Strategy { return Strategy{"-android uiautomator", "new UiSelector()." + expr} }
	switch {
	case sel == nil:
		return nil
	case sel.ID != "":
		return []Strategy{ua("resourceId(" + javaString(sel.ID) + ")"), {"accessibility id", sel.ID}}
	case sel.Text != "":
		return []Strategy{ua("text(" + javaString(sel.Text) + ")"), ua("description(" + javaString(sel.Text) + ")")}
	case sel.TextContains != "":
		return []Strategy{ua("textContains(" + javaString(sel.TextContains) + ")"), ua("descriptionContains(" + javaString(sel.TextContains) + ")")}
	case sel.Hint != "":
		return []Strategy{{"xpath", "//*[@hint=" + xpathString(sel.Hint) + "]"}, ua("text(" + javaString(sel.Hint) + ")")}
	case sel.XPath != "":
		return []Strategy{{"xpath", sel.XPath}}
	}
	return nil
}

func javaString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func xpathString(s string) string {
	if !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	parts := strings.Split(s, `"`)
	return `concat("` + strings.Join(parts, `", '"', "`) + `")`
}
