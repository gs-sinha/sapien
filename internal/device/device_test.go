package device

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// fakeAppium is a scripted WebDriver server: elements maps a locator value
// to the element ids it matches (appearing only after appearAfter finds,
// to exercise polling), texts maps an element id to its text.
type fakeAppium struct {
	mu          sync.Mutex
	calls       []string
	elements    map[string][]string
	appearAfter map[string]int
	seen        map[string]int
	texts       map[string]string
	descs       map[string]string
	children    map[string][]string
	disabled    map[string]bool
	staleOnce   map[string]bool
	caps        map[string]any
}

func newFakeAppium() *fakeAppium {
	return &fakeAppium{elements: map[string][]string{}, appearAfter: map[string]int{}, seen: map[string]int{}, texts: map[string]string{}, descs: map[string]string{}, children: map[string][]string{}, disabled: map[string]bool{}, staleOnce: map[string]bool{}}
}

func (f *fakeAppium) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	p := strings.TrimPrefix(r.URL.Path, "/session/s1")
	reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"value": v}) }

	switch {
	case r.URL.Path == "/status":
		reply(map[string]any{"ready": true})
		return
	case r.URL.Path == "/session" && r.Method == http.MethodPost:
		f.caps = body["capabilities"].(map[string]any)["alwaysMatch"].(map[string]any)
		reply(map[string]any{"sessionId": "s1"})
		return
	}
	switch {
	case p == "/elements":
		v := body["value"].(string)
		f.calls = append(f.calls, "find "+body["using"].(string)+" "+v)
		f.seen[v]++
		ids := f.elements[v]
		if f.seen[v] <= f.appearAfter[v] {
			ids = nil
		}
		out := []map[string]string{}
		for _, id := range ids {
			out = append(out, map[string]string{elementKeyForTest: id})
		}
		reply(out)
	case strings.HasSuffix(p, "/elements") && strings.HasPrefix(p, "/element/"):
		parent := strings.Split(p, "/")[2]
		out := []map[string]string{}
		for _, id := range f.children[parent] {
			out = append(out, map[string]string{elementKeyForTest: id})
		}
		reply(out)
	case strings.HasSuffix(p, "/click"):
		id := strings.Split(p, "/")[2]
		if f.staleOnce[id] {
			delete(f.staleOnce, id)
			f.calls = append(f.calls, "stale "+id)
			w.WriteHeader(404)
			reply(map[string]any{"error": "stale element reference", "message": "Cached elements do not exist in DOM anymore"})
			return
		}
		f.calls = append(f.calls, "click "+id)
		reply(nil)
	case strings.HasSuffix(p, "/value"):
		f.calls = append(f.calls, "keys "+strings.Split(p, "/")[2]+" "+body["text"].(string))
		reply(nil)
	case strings.HasSuffix(p, "/clear"):
		f.calls = append(f.calls, "clear "+strings.Split(p, "/")[2])
		reply(nil)
	case strings.HasSuffix(p, "/text"):
		reply(f.texts[strings.Split(p, "/")[2]])
	case strings.HasSuffix(p, "/attribute/content-desc"):
		reply(f.descs[strings.Split(p, "/")[2]])
	case strings.HasSuffix(p, "/attribute/enabled"):
		if f.disabled[strings.Split(p, "/")[2]] {
			reply("false")
		} else {
			reply("true")
		}
	case p == "/screenshot":
		reply(base64.StdEncoding.EncodeToString([]byte("PNGDATA")))
	case p == "/source":
		reply("<hierarchy/>")
	case p == "/back":
		f.calls = append(f.calls, "back")
		reply(nil)
	case p == "/window/rect":
		reply(map[string]int{"x": 0, "y": 0, "width": 1000, "height": 2000})
	case p == "/execute/sync":
		args, _ := json.Marshal(body["args"])
		f.calls = append(f.calls, body["script"].(string)+" "+string(args))
		reply(nil)
	case r.Method == http.MethodDelete:
		f.calls = append(f.calls, "delete-session")
		reply(nil)
	default:
		w.WriteHeader(404)
		reply(map[string]any{"error": "unknown command", "message": p})
	}
}

const elementKeyForTest = "element-6066-11e4-a52e-4f735466cecf"

func (f *fakeAppium) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fakeExec answers adb and records every command.
type fakeExec struct {
	mu       sync.Mutex
	cmds     []string
	logLines []string
	started  []string
}

func (f *fakeExec) record(name string, args []string) string {
	c := filepath.Base(name) + " " + strings.Join(args, " ")
	f.mu.Lock()
	f.cmds = append(f.cmds, c)
	f.mu.Unlock()
	return c
}

func (f *fakeExec) Run(_ context.Context, _, name string, args ...string) (string, error) {
	c := f.record(name, args)
	switch {
	case strings.HasSuffix(c, "adb devices"):
		return "List of devices attached\nemulator-5554\tdevice\n\n", nil
	case strings.Contains(c, "pm path"):
		return "package:/data/app/base.apk\n", nil
	case strings.Contains(c, "dumpsys activity"):
		return "  mResumedActivity: ActivityRecord{abc u0 com.blitznow.kaptaan/.MainActivity t12}\n", nil
	}
	return "", nil
}

func (f *fakeExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) error {
	f.record(name, args)
	for _, l := range f.logLines {
		onLine(l)
	}
	<-ctx.Done()
	return nil
}

func (f *fakeExec) Start(name string, args []string, _ string) (func(), error) {
	f.mu.Lock()
	f.started = append(f.started, filepath.Base(name)+" "+strings.Join(args, " "))
	f.mu.Unlock()
	return func() {}, nil
}

func (f *fakeExec) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

func newTestManager(t *testing.T, fa *fakeAppium, fe *fakeExec, apps map[string]domain.AppConfig, attach ...string) (*Manager, string) {
	t.Helper()
	srv := httptest.NewServer(fa)
	t.Cleanup(srv.Close)
	ws := t.TempDir()
	return NewManager(Options{
		WorkspaceDir: ws,
		StateDir:     filepath.Join(ws, ".sapien"),
		EnvName:      "stage",
		Apps:         apps,
		Local:        LocalConfig{AppiumURL: srv.URL, AndroidHome: "/nonexistent"},
		Attach:       attach,
		Exec:         fe,
		Poll:         time.Millisecond,
	}), ws
}

func riderApp(apk string) map[string]domain.AppConfig {
	return map[string]domain.AppConfig{"rider": {
		Package: "com.blitznow.kaptaan", Repo: "rider", APK: apk, LogFormat: "rider-box",
		Build: "fvm flutter build apk --debug", Permissions: []string{"CAMERA"},
	}}
}

func TestManager_LaunchTypeReadAndLogs(t *testing.T) {
	fa := newFakeAppium()
	fa.elements[`new UiSelector().resourceId("phone")`] = []string{"e1"}
	fa.elements[`new UiSelector().description("Ravi")`] = []string{"e2"}
	fa.appearAfter[`new UiSelector().resourceId("phone")`] = 2 // found on the third poll
	fa.descs["e2"] = "Ravi"
	fe := &fakeExec{logLines: []string{"I/flutter ( 1): hello"}}

	m, ws := newTestManager(t, fa, fe, riderApp("build/app.apk"))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "rider", "build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "rider", "build", "app.apk"), []byte("apk-v1"), 0o644))

	ctx := context.Background()
	app, err := m.App(ctx, "rider")
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close(ctx) })

	assert.Equal(t, "emulator-5554", fa.caps["appium:udid"])
	assert.Equal(t, "UiAutomator2", fa.caps["appium:automationName"])
	assert.NotContains(t, fa.caps, "appium:appPackage", "device-wide session; launch is an action")

	cmds := strings.Join(fe.commands(), "\n")
	assert.Contains(t, cmds, "adb -s emulator-5554 install -r -t -d "+filepath.Join(ws, "rider", "build", "app.apk"))
	assert.Contains(t, cmds, "settings put global animator_duration_scale 0")
	assert.NotContains(t, cmds, "sh -c", "APK exists: no build")

	_, err = app.Do(ctx, domain.UIAction{Kind: domain.UILaunch, ClearState: true})
	require.NoError(t, err)
	cmds = strings.Join(fe.commands(), "\n")
	assert.Contains(t, cmds, "pm clear com.blitznow.kaptaan")
	assert.Contains(t, cmds, "pm grant com.blitznow.kaptaan android.permission.CAMERA")
	assert.Contains(t, cmds, "appops set com.blitznow.kaptaan SYSTEM_ALERT_WINDOW allow")

	_, err = app.Do(ctx, domain.UIAction{Kind: domain.UIType, Target: &domain.Selector{ID: "phone"}, Text: "8445544024"})
	require.NoError(t, err)

	out, err := app.Do(ctx, domain.UIAction{Kind: domain.UIRead, Target: &domain.Selector{Text: "Ravi"}, As: "n"})
	require.NoError(t, err)
	assert.Equal(t, "Ravi", out.Text, "falls back to content-desc, where Flutter puts most text")

	calls := fa.callLog()
	assert.Contains(t, calls, `mobile: activateApp [{"appId":"com.blitznow.kaptaan"}]`)
	assert.Contains(t, calls, "click e1", "type focuses the field first")
	assert.Contains(t, calls, "keys e1 8445544024")

	assert.Eventually(t, func() bool { lines, _ := app.Logs(0); return len(lines) == 1 }, time.Second, time.Millisecond)
	lines, _ := app.Logs(0)
	assert.Equal(t, []string{"hello"}, lines)
	assert.Equal(t, "com.blitznow.kaptaan/.MainActivity", app.Activity(ctx))

	// A second App call reuses everything; an unchanged APK is not reinstalled.
	_, err = m.App(ctx, "rider")
	require.NoError(t, err)
	installs := 0
	for _, c := range fe.commands() {
		if strings.Contains(c, " install ") {
			installs++
		}
	}
	assert.Equal(t, 1, installs)
}

func TestManager_InstallStateSkipsUnchangedAPKAcrossRuns(t *testing.T) {
	fa := newFakeAppium()
	fe := &fakeExec{}
	m, ws := newTestManager(t, fa, fe, riderApp("app.apk"))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "rider"), 0o755))
	apk := filepath.Join(ws, "rider", "app.apk")
	require.NoError(t, os.WriteFile(apk, []byte("v1"), 0o644))
	ctx := context.Background()
	_, err := m.App(ctx, "rider")
	require.NoError(t, err)
	_ = m.Close(ctx)

	// Next run, same APK: no install. Changed APK: install.
	m2 := NewManager(m.opts)
	_, err = m2.App(ctx, "rider")
	require.NoError(t, err)
	_ = m2.Close(ctx)
	require.NoError(t, os.WriteFile(apk, []byte("v2"), 0o644))
	m3 := NewManager(m.opts)
	_, err = m3.App(ctx, "rider")
	require.NoError(t, err)
	_ = m3.Close(ctx)

	installs := 0
	for _, c := range fe.commands() {
		if strings.Contains(c, " install ") {
			installs++
		}
	}
	assert.Equal(t, 2, installs)
}

func TestManager_AttachSkipsInstallAndClear(t *testing.T) {
	fa := newFakeAppium()
	fe := &fakeExec{}
	m, _ := newTestManager(t, fa, fe, riderApp("missing.apk"), "rider")
	ctx := context.Background()
	app, err := m.App(ctx, "rider")
	require.NoError(t, err, "attach never needs the APK")
	out, err := app.Do(ctx, domain.UIAction{Kind: domain.UILaunch, ClearState: true})
	require.NoError(t, err)
	assert.Contains(t, out.Warning, "attached")
	cmds := strings.Join(fe.commands(), "\n")
	assert.NotContains(t, cmds, "pm clear")
	assert.NotContains(t, cmds, " install ")
	assert.NotContains(t, cmds, "sh -c")
}

func TestManager_MissingAPKBuildsFirst(t *testing.T) {
	fa := newFakeAppium()
	fe := &fakeExec{}
	m, ws := newTestManager(t, fa, fe, riderApp("never-built.apk"))
	_, err := m.App(context.Background(), "rider")
	require.Error(t, err, "the fake build produces no APK")
	assert.Contains(t, errs.As(err).Message, "build succeeded but")
	assert.Contains(t, fe.commands(), "sh -c fvm flutter build apk --debug")
	_ = ws
}

func TestManager_UnknownApp(t *testing.T) {
	m, _ := newTestManager(t, newFakeAppium(), &fakeExec{}, riderApp("x.apk"))
	_, err := m.App(context.Background(), "overwatch")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `app "overwatch" is not in environment "stage"'s apps: (have: rider)`)
}

func TestSession_WaitForTimesOutAndAsserts(t *testing.T) {
	fa := newFakeAppium()
	fa.elements[`new UiSelector().text("Home")`] = []string{"h"}
	fa.texts["h"] = "Home"
	m, _ := newTestManager(t, fa, &fakeExec{}, map[string]domain.AppConfig{"rider": {Package: "p"}}, "rider")
	ctx := context.Background()
	app, err := m.App(ctx, "rider")
	require.NoError(t, err)

	_, err = app.Do(ctx, domain.UIAction{Kind: domain.UIWaitFor, Target: &domain.Selector{ID: "nope"}, Timeout: "20ms"})
	require.Error(t, err)
	assert.Equal(t, ErrUIAction, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "id=nope not found within 20ms")

	out, err := app.Do(ctx, domain.UIAction{Kind: domain.UIAssertVisible, Target: &domain.Selector{Text: "Home"}})
	require.NoError(t, err)
	assert.True(t, out.Assert.Passed)

	out, err = app.Do(ctx, domain.UIAction{Kind: domain.UIAssertNotVisible, Target: &domain.Selector{Text: "Home"}, Timeout: "10ms"})
	require.NoError(t, err)
	assert.False(t, out.Assert.Passed)
	assert.Contains(t, out.Assert.Actual, "still on screen")

	out, err = app.Do(ctx, domain.UIAction{Kind: domain.UIAssertText, Target: &domain.Selector{Text: "Home"}, Matches: "^Ho"})
	require.NoError(t, err)
	assert.True(t, out.Assert.Passed)
	out, err = app.Do(ctx, domain.UIAction{Kind: domain.UIAssertText, Target: &domain.Selector{Text: "Home"}, Eq: "Away", Timeout: "10ms"})
	require.NoError(t, err)
	assert.False(t, out.Assert.Passed)
	assert.Equal(t, "Home", out.Assert.Actual)

	out, err = app.Do(ctx, domain.UIAction{Kind: domain.UIScreenshot})
	require.NoError(t, err)
	assert.Equal(t, []byte("PNGDATA"), out.PNG)
}

func TestSession_ScrollToSwipesUntilFound(t *testing.T) {
	fa := newFakeAppium()
	sel := `new UiSelector().resourceId("card")`
	fa.elements[sel] = []string{"c"}
	fa.appearAfter[sel] = 2
	m, _ := newTestManager(t, fa, &fakeExec{}, map[string]domain.AppConfig{"rider": {Package: "p"}}, "rider")
	ctx := context.Background()
	app, err := m.App(ctx, "rider")
	require.NoError(t, err)
	_, err = app.Do(ctx, domain.UIAction{Kind: domain.UIScrollTo, Target: &domain.Selector{ID: "card"}})
	require.NoError(t, err)
	scrolls := 0
	for _, c := range fa.callLog() {
		if strings.HasPrefix(c, "mobile: scrollGesture") {
			scrolls++
			assert.Contains(t, c, `"direction":"down"`)
		}
	}
	assert.Equal(t, 2, scrolls, "found on the third look: two scrolls in between")
}

func TestStrategies(t *testing.T) {
	assert.Equal(t, []Strategy{
		{"-android uiautomator", `new UiSelector().resourceId("a\"b")`},
		{"accessibility id", `a"b`},
	}, Strategies(&domain.Selector{ID: `a"b`}))
	assert.Equal(t, `//*[@hint="Enter mobile number"]`, Strategies(&domain.Selector{Hint: "Enter mobile number"})[0].Value)
	assert.Equal(t, `//*[@hint='say "hi"']`, Strategies(&domain.Selector{Hint: `say "hi"`})[0].Value)
	assert.Equal(t, []Strategy{{"xpath", "//x"}}, Strategies(&domain.Selector{XPath: "//x"}))
	assert.Nil(t, Strategies(nil))
}

func TestManager_UnknownAppPointsAtEnvironmentsDefiningIt(t *testing.T) {
	m, _ := newTestManager(t, newFakeAppium(), &fakeExec{}, nil)
	m.opts.EnvName = "local"
	m.opts.EnvsWithApp = func(app string) []string { return []string{"local", "stage"} }
	_, err := m.App(context.Background(), "rider")
	require.Error(t, err)
	e := errs.As(err)
	assert.Contains(t, e.Message, `environment "local"'s apps: (have: ); it is defined in stage, run with that environment`)
	assert.Contains(t, e.Hint, `app "rider" is defined in stage: run against that environment (--env stage)`)
	assert.Equal(t, []string{"stage"}, e.Details["environments"])
}

func TestSession_SwipeAcrossElementAndHideKeyboard(t *testing.T) {
	fa := newFakeAppium()
	fa.elements[`new UiSelector().resourceId("early_salary_swipe_withdraw")`] = []string{"sw"}
	m, _ := newTestManager(t, fa, &fakeExec{}, map[string]domain.AppConfig{"rider": {Package: "p"}}, "rider")
	ctx := context.Background()
	app, err := m.App(ctx, "rider")
	require.NoError(t, err)

	_, err = app.Do(ctx, domain.UIAction{Kind: domain.UIHideKeyboard})
	require.NoError(t, err)
	_, err = app.Do(ctx, domain.UIAction{Kind: domain.UISwipe, Direction: "right", Target: &domain.Selector{ID: "early_salary_swipe_withdraw"}})
	require.NoError(t, err)

	calls := fa.callLog()
	assert.Contains(t, calls, `mobile: hideKeyboard [{}]`)
	assert.Contains(t, calls, `mobile: swipeGesture [{"direction":"right","elementId":"sw","percent":0.95,"speed":1500}]`)
}

// hangingBuildExec answers adb like fakeExec but blocks on the build command
// until its context ends, as a build tool waiting at a prompt does.
type hangingBuildExec struct{ fakeExec }

func (h *hangingBuildExec) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "sh" {
		<-ctx.Done()
		return "Flutter SDK 3.44.6 is not installed. Install it? (y/n)", ctx.Err()
	}
	return h.fakeExec.Run(ctx, dir, name, args...)
}

func TestManager_BuildThatHangsTimesOut(t *testing.T) {
	old := buildTimeout
	buildTimeout = 20 * time.Millisecond
	t.Cleanup(func() { buildTimeout = old })

	m, _ := newTestManager(t, newFakeAppium(), &fakeExec{}, riderApp("never-built.apk"))
	m.ex = &hangingBuildExec{}
	_, err := m.App(context.Background(), "rider")
	require.Error(t, err)
	assert.Contains(t, errs.As(err).Message, "took longer than 20ms; is the build command waiting for input?")
}

func TestSession_ReadFallsBackToChildText(t *testing.T) {
	fa := newFakeAppium()
	fa.elements[`new UiSelector().resourceId("early_salary_available_amount")`] = []string{"box"}
	fa.children["box"] = []string{"txt"}
	fa.texts["box"] = "null" // what UiAutomator2 reports for a text-less node
	fa.descs["txt"] = "₹56.00"
	m, _ := newTestManager(t, fa, &fakeExec{}, map[string]domain.AppConfig{"rider": {Package: "p"}}, "rider")
	ctx := context.Background()
	app, err := m.App(ctx, "rider")
	require.NoError(t, err)
	out, err := app.Do(ctx, domain.UIAction{Kind: domain.UIRead, Target: &domain.Selector{ID: "early_salary_available_amount"}, As: "amt"})
	require.NoError(t, err)
	assert.Equal(t, "₹56.00", out.Text, "an id on its own semantics node reads its child's label")
}

func TestSession_TypeIntoDisabledFieldFails(t *testing.T) {
	fa := newFakeAppium()
	fa.elements[`new UiSelector().resourceId("amount")`] = []string{"f"}
	fa.disabled["f"] = true
	m, _ := newTestManager(t, fa, &fakeExec{}, map[string]domain.AppConfig{"rider": {Package: "p"}}, "rider")
	ctx := context.Background()
	app, err := m.App(ctx, "rider")
	require.NoError(t, err)
	_, err = app.Do(ctx, domain.UIAction{Kind: domain.UIType, Target: &domain.Selector{ID: "amount"}, Text: "10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the field is disabled")
	assert.NotContains(t, fa.callLog(), "keys f 10")
}

func TestSession_TapRetriesStaleElement(t *testing.T) {
	fa := newFakeAppium()
	fa.elements[`new UiSelector().resourceId("get_started_skip_btn")`] = []string{"skip"}
	fa.staleOnce["skip"] = true // the carousel rebuilt between lookup and tap
	m, _ := newTestManager(t, fa, &fakeExec{}, map[string]domain.AppConfig{"rider": {Package: "p"}}, "rider")
	ctx := context.Background()
	app, err := m.App(ctx, "rider")
	require.NoError(t, err)
	_, err = app.Do(ctx, domain.UIAction{Kind: domain.UITap, Target: &domain.Selector{ID: "get_started_skip_btn"}})
	require.NoError(t, err)
	calls := fa.callLog()
	assert.Contains(t, calls, "stale skip")
	assert.Contains(t, calls, "click skip", "looked up again and tapped")
}
