package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/device"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/flow"
)

// fakeDevice records every action and answers from a script: a selector
// string in missing is never found; reads return texts[selector].
type fakeDevice struct {
	mu      sync.Mutex
	opened  int
	closed  int
	actions []string
	missing map[string]bool
	texts   map[string]string
	lines   []string
	api     []map[string]any
}

func (f *fakeDevice) App(_ context.Context, name string) (device.App, error) {
	if name != "rider" {
		return nil, errs.New(errs.Invalid, "app %q is not in environment's apps:", name)
	}
	return f, nil
}
func (f *fakeDevice) Close(context.Context) error { f.closed++; return nil }

func (f *fakeDevice) Do(_ context.Context, a domain.UIAction) (device.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	desc := a.Kind
	if a.Target != nil {
		desc += " " + a.Target.String()
	}
	if a.Text != "" {
		desc += " <" + a.Text + ">"
	}
	f.actions = append(f.actions, desc)
	// Every action "logs" one line, as a running app would.
	f.lines = append(f.lines, "did "+desc)
	if a.Target != nil && f.missing[a.Target.String()] {
		if strings.HasPrefix(a.Kind, "assert_") {
			return device.Outcome{Assert: &device.Assertion{Expr: "visible(" + a.Target.String() + ")", Actual: "not on screen"}}, nil
		}
		return device.Outcome{}, errs.New(device.ErrUIAction, "element %s not found", a.Target)
	}
	switch a.Kind {
	case domain.UIRead:
		return device.Outcome{Text: f.texts[a.Target.String()]}, nil
	case domain.UIScreenshot:
		return device.Outcome{PNG: []byte("png")}, nil
	case domain.UIAssertVisible:
		return device.Outcome{Assert: &device.Assertion{Passed: true, Expr: "visible(" + a.Target.String() + ")"}}, nil
	}
	return device.Outcome{}, nil
}
func (f *fakeDevice) LogMark() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.lines) }
func (f *fakeDevice) Logs(since int) ([]string, []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines[since:]...), f.api
}
func (f *fakeDevice) Screenshot(context.Context) ([]byte, error) { return []byte("png"), nil }
func (f *fakeDevice) Source(context.Context) (string, error)     { return "<hierarchy/>", nil }
func (f *fakeDevice) Activity(context.Context) string            { return "com.blitznow.kaptaan/.MainActivity" }

func parseFlow(t *testing.T, src string) *domain.Flow {
	t.Helper()
	f, err := flow.Parse(src)
	require.NoError(t, err)
	return f
}

func uiOpts(t *testing.T, fd *fakeDevice, riderURL string) Options {
	return Options{
		Env:          buildTestEnv("stage", false, "http://unused", "http://unused", riderURL),
		UIDevice:     func(context.Context) (device.Device, error) { fd.opened++; return fd, nil },
		ArtifactsDir: t.TempDir(),
	}
}

func TestUIStep_RunsActionsAndExposesValue(t *testing.T) {
	// The API step after the ui step uses what the ui step read off the
	// screen: ui and call steps share steps.<id>.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"riderId": "r-42", "name": "Ravi"})
	}))
	defer srv.Close()

	fd := &fakeDevice{
		texts: map[string]string{"id=rider_id": "r-42"},
		api: []map[string]any{
			{"method": "POST", "path": "/v1/auth", "status": 200, "request": map[string]any{"headers": map[string]any{"Authorization": "Bearer s3cret"}}},
		},
	}
	f := parseFlow(t, `
version: 1
id: mixed
inputs:
  phone: {type: string, default: "8445544024"}
steps:
  - id: login
    ui:
      app: rider
      actions:
        - launch: {clear_state: true}
        - type: {hint: Enter mobile number, text: "${inputs.phone}"}
        - read: {id: rider_id, as: rid}
        - screenshot: after-login
        - assert_visible: {text: Home}
    extract:
      auth_ok: "body.logs.api.exists(c, c.path == '/v1/auth' && c.status == 200)"
    assert:
      - "out.rid == 'r-42'"
      - "out.auth_ok"
      - "body.screen.activity.endsWith('MainActivity')"
      - "size(body.logs.lines) == 5"
  - id: rider
    call: rider-service.getRider
    input: {riderId: "${steps.login.out.rid}"}
    assert: ["status == 200"]
  - id: card
    ui:
      app: rider
      actions:
        - wait_for: {id: "card_${steps.rider.body.riderId}"}
`)
	opts := uiOpts(t, fd, srv.URL)
	run, err := New(buildTestOperations(t)).Run(context.Background(), f, nil, opts)
	require.NoError(t, err)
	require.Equal(t, domain.RunPassed, run.Status, "%+v", run.Steps)

	assert.Equal(t, []string{
		"launch",
		"type hint=Enter mobile number <8445544024>",
		"read id=rider_id",
		"screenshot",
		"assert_visible text=Home",
		"wait_for id=card_r-42",
	}, fd.actions)
	assert.Equal(t, "/v1/riders/r-42", gotPath)
	assert.Equal(t, 1, fd.opened, "one device for the whole run")
	assert.Equal(t, 1, fd.closed, "closed when the run ends")

	login := run.Steps[0]
	assert.Equal(t, "ui:rider", login.Operation)
	assert.Equal(t, "r-42", login.Out["rid"])
	require.Len(t, login.Assertions, 5, "assert_visible is recorded with the step's own assertions")

	// Persisted logs are redacted; the expression value saw the raw ones.
	api := login.Response.Body.(map[string]any)["logs"].(map[string]any)["api"].([]any)
	hdr := api[0].(map[string]any)["request"].(map[string]any)["headers"].(map[string]any)
	assert.Equal(t, "[REDACTED]", hdr["Authorization"])

	kinds := map[string]bool{}
	for _, a := range login.Artifacts {
		kinds[a.Name] = true
		_, err := os.Stat(a.Path)
		assert.NoError(t, err, a.Path)
		assert.Contains(t, a.Path, run.ID)
	}
	assert.True(t, kinds["after-login.png"] && kinds["final.png"] && kinds["logcat.txt"], "%+v", login.Artifacts)
}

func TestUIStep_MissingElementFailsWithArtifacts(t *testing.T) {
	fd := &fakeDevice{missing: map[string]bool{"id=nope": true}}
	f := parseFlow(t, `
version: 1
id: fail
steps:
  - id: s
    ui:
      app: rider
      actions:
        - tap: {id: nope}
        - back
  - id: never
    ui: {app: rider, actions: [back]}
teardown:
  - id: cleanup
    ui: {app: rider, actions: [stop]}
`)
	run, err := New(buildTestOperations(t)).Run(context.Background(), f, nil, uiOpts(t, fd, "http://unused"))
	require.NoError(t, err)
	assert.Equal(t, domain.RunFailed, run.Status)

	s := run.Steps[0]
	assert.Equal(t, domain.StepFailed, s.Status)
	require.NotNil(t, s.Error)
	assert.Equal(t, string(device.ErrUIAction), s.Error.Code)
	assert.Equal(t, []string{"tap id=nope", "stop"}, fd.actions, "later actions and steps are skipped; teardown still runs")
	names := map[string]bool{}
	for _, a := range s.Artifacts {
		names[a.Name] = true
	}
	assert.True(t, names["failure.png"] && names["source.xml"], "%+v", s.Artifacts)
	assert.Equal(t, domain.StepSkipped, run.Steps[1].Status)
}

func TestUIStep_FailedAssertActionFailsStep(t *testing.T) {
	fd := &fakeDevice{missing: map[string]bool{"text=Home": true}}
	f := parseFlow(t, `
version: 1
id: a
steps:
  - id: s
    ui:
      app: rider
      actions:
        - assert_visible: {text: Home, message: home should show}
        - back
`)
	run, err := New(buildTestOperations(t)).Run(context.Background(), f, nil, uiOpts(t, fd, "http://unused"))
	require.NoError(t, err)
	s := run.Steps[0]
	assert.Equal(t, domain.StepFailed, s.Status)
	assert.Nil(t, s.Error, "the failure is the assertion, not an error")
	require.Len(t, s.Assertions, 1)
	assert.False(t, s.Assertions[0].Passed)
	assert.Equal(t, []string{"assert_visible text=Home"}, fd.actions)
}

func TestUIStep_UnknownAppAndNoDeviceError(t *testing.T) {
	f := parseFlow(t, "version: 1\nid: x\nsteps:\n  - id: s\n    ui: {app: overwatch, actions: [back]}\n")
	run, err := New(buildTestOperations(t)).Run(context.Background(), f, nil, uiOpts(t, &fakeDevice{}, "http://unused"))
	require.NoError(t, err)
	assert.Equal(t, domain.StepErrored, run.Steps[0].Status)
	assert.Contains(t, run.Steps[0].Error.Message, `app "overwatch"`)

	opts := uiOpts(t, &fakeDevice{}, "http://unused")
	opts.UIDevice = nil
	run, err = New(buildTestOperations(t)).Run(context.Background(), f, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, domain.StepErrored, run.Steps[0].Status)
	assert.Contains(t, run.Steps[0].Error.Message, "no device")
}

func TestUIStep_WhenFalseSkipsWithoutOpeningDevice(t *testing.T) {
	fd := &fakeDevice{}
	f := parseFlow(t, "version: 1\nid: x\nsteps:\n  - id: s\n    when: \"false\"\n    ui: {app: rider, actions: [back]}\n")
	run, err := New(buildTestOperations(t)).Run(context.Background(), f, nil, uiOpts(t, fd, "http://unused"))
	require.NoError(t, err)
	assert.Equal(t, domain.StepSkipped, run.Steps[0].Status)
	assert.Equal(t, 0, fd.opened)
}
