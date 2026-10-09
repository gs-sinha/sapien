package device

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gs-sinha/sapien/internal/device/appium"
	"github.com/gs-sinha/sapien/internal/device/logs"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// Device is what the runner drives: one per run, created on the first ui
// step and closed when the run ends.
type Device interface {
	// App returns the session for the named app (an entry of the
	// environment's apps:), preparing the device, Appium, and the app on
	// first use.
	App(ctx context.Context, name string) (App, error)
	Close(ctx context.Context) error
}

// App drives one app on the device.
type App interface {
	Do(ctx context.Context, a domain.UIAction) (Outcome, error)
	// LogMark and Logs bracket a step: the lines (and the API calls parsed
	// from them) logged since the mark.
	LogMark() int
	Logs(since int) (lines []string, api []map[string]any)
	Screenshot(ctx context.Context) ([]byte, error)
	Source(ctx context.Context) (string, error)
	Activity(ctx context.Context) string
}

// Outcome is what one action produced beyond success.
type Outcome struct {
	Text    string     // read
	PNG     []byte     // screenshot
	Assert  *Assertion // assert_* actions
	Warning string
}

// Assertion is an assert_* action's verdict.
type Assertion struct {
	Passed  bool
	Expr    string
	Actual  string
	Message string
}

// Options configures a Manager.
type Options struct {
	WorkspaceDir string
	StateDir     string // <workspace>/.sapien
	EnvName      string
	Apps         map[string]domain.AppConfig
	Local        LocalConfig
	// Attach adds apps to Local.Attach for this run (the CLI's --attach).
	Attach []string
	// Rebuild forces each app's build command before installing.
	Rebuild bool
	Exec    Exec
	// Progress receives one-line status messages (booting, building, ...).
	Progress func(string)
	// EnvsWithApp, when set, lists the workspace environments whose apps:
	// define the named app, so an unknown-app error can point at them.
	EnvsWithApp func(app string) []string
	// Poll is the element-lookup retry interval (default 300ms).
	Poll time.Duration
}

// Manager implements Device.
type Manager struct {
	opts Options
	ex   Exec

	mu        sync.Mutex
	adb       *ADB
	client    *appium.Client
	session   *appium.Session
	stopFns   []func()
	logcats   map[string]*logs.Buffer // by tag
	prepared  map[string]bool
	apps      map[string]*appSession
	logCancel context.CancelFunc
}

// NewManager returns a Manager; nothing touches the device until App.
func NewManager(opts Options) *Manager {
	if opts.Exec == nil {
		opts.Exec = OSExec{}
	}
	if opts.Poll <= 0 {
		opts.Poll = 300 * time.Millisecond
	}
	return &Manager{opts: opts, ex: opts.Exec, logcats: map[string]*logs.Buffer{}, prepared: map[string]bool{}, apps: map[string]*appSession{}}
}

func (m *Manager) progress(format string, args ...any) {
	if m.opts.Progress != nil {
		m.opts.Progress(fmt.Sprintf(format, args...))
	}
}

func (m *Manager) attached(name string) bool {
	for _, a := range append(append([]string{}, m.opts.Local.Attach...), m.opts.Attach...) {
		if a == name {
			return true
		}
	}
	return false
}

// App implements Device.
func (m *Manager) App(ctx context.Context, name string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.apps[name]; ok {
		return s, nil
	}
	cfg, ok := m.opts.Apps[name]
	if !ok {
		have := make([]string, 0, len(m.opts.Apps))
		for k := range m.opts.Apps {
			have = append(have, k)
		}
		sort.Strings(have)
		e := errs.New(errs.Invalid, "app %q is not in environment %q's apps: (have: %s)", name, m.opts.EnvName, strings.Join(have, ", "))
		var elsewhere []string
		if m.opts.EnvsWithApp != nil {
			for _, env := range m.opts.EnvsWithApp(name) {
				if env != m.opts.EnvName {
					elsewhere = append(elsewhere, env)
				}
			}
		}
		if len(elsewhere) > 0 {
			// Said in the message itself, not only the hint: the inspector
			// and run summaries show the message alone.
			e.Message += fmt.Sprintf("; it is defined in %s, run with that environment", strings.Join(elsewhere, ", "))
			return nil, e.WithDetail("environments", elsewhere).
				WithHint(fmt.Sprintf("app %q is defined in %s: run against that environment (--env %s), or add it under apps: in environments/%s.yaml", name, strings.Join(elsewhere, ", "), elsewhere[0], m.opts.EnvName))
		}
		return nil, e.WithHint("add it under apps: in environments/" + m.opts.EnvName + ".yaml")
	}
	if cfg.Package == "" {
		return nil, errs.New(errs.Invalid, "app %q has no package", name)
	}
	if cfg.Platform != "" && cfg.Platform != "android" {
		return nil, errs.New(errs.Invalid, "app %q: platform %q is not supported yet; only android", name, cfg.Platform)
	}
	if err := m.ensureDevice(ctx); err != nil {
		return nil, err
	}
	if err := m.ensureAppium(ctx); err != nil {
		return nil, err
	}
	attach := m.attached(name)
	if !attach {
		if err := m.prepareApp(ctx, name, cfg); err != nil {
			return nil, err
		}
	}
	if err := m.ensureSession(ctx); err != nil {
		return nil, err
	}
	tag := cfg.LogTag
	if tag == "" {
		tag = "flutter"
	}
	s := &appSession{m: m, name: name, cfg: cfg, attach: attach, logBuf: m.ensureLogcat(tag)}
	m.apps[name] = s
	return s, nil
}

// ensureDevice picks the attached device (or boots the configured AVD).
func (m *Manager) ensureDevice(ctx context.Context) error {
	if m.adb != nil {
		return nil
	}
	local := m.opts.Local
	bin := local.adbBin()
	serials, err := Devices(ctx, m.ex, bin)
	if err != nil {
		return errs.Wrap(errs.Invalid, err, "running adb").WithHint("run `sapien device doctor`")
	}
	serial := local.Serial
	switch {
	case serial != "":
		found := false
		for _, s := range serials {
			found = found || s == serial
		}
		if !found {
			return errs.New(errs.Invalid, "device %s (ui.yaml serial) is not attached; attached: %v", serial, serials)
		}
	case len(serials) > 0:
		serial = serials[0]
		if len(serials) > 1 {
			m.progress("several devices attached (%v); using %s (set serial: in .sapien/ui.yaml to pick)", serials, serial)
		}
	case local.AVD != "":
		m.progress("booting emulator %s ...", local.AVD)
		stop, err := m.ex.Start(local.emulatorBin(), []string{"-avd", local.AVD, "-no-snapshot-save", "-no-boot-anim"}, filepath.Join(m.opts.StateDir, "emulator.log"))
		if err != nil {
			return errs.Wrap(errs.Invalid, err, "starting emulator %s", local.AVD)
		}
		_ = stop // the emulator outlives the run on purpose: booting takes a minute
		serial, err = m.waitForBoot(ctx, bin)
		if err != nil {
			return err
		}
	default:
		return errs.New(errs.Invalid, "no Android device attached and no avd: set in .sapien/ui.yaml").
			WithHint("start an emulator (emulator -avd <name>) or set avd: in .sapien/ui.yaml; `sapien device doctor` lists your AVDs")
	}
	m.adb = &ADB{Exec: m.ex, Bin: bin, Serial: serial}
	m.adb.DisableAnimations(ctx)
	return nil
}

func (m *Manager) waitForBoot(ctx context.Context, bin string) (string, error) {
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		if serials, _ := Devices(ctx, m.ex, bin); len(serials) > 0 {
			a := &ADB{Exec: m.ex, Bin: bin, Serial: serials[0]}
			if a.BootCompleted(ctx) {
				m.progress("emulator %s booted", serials[0])
				return serials[0], nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return "", errs.New(errs.Invalid, "emulator did not finish booting within 4 minutes (see .sapien/emulator.log)")
}

// ensureAppium connects to Appium, starting it when allowed.
func (m *Manager) ensureAppium(ctx context.Context) error {
	if m.client != nil {
		return nil
	}
	local := m.opts.Local
	c := appium.New(local.appiumURL())
	if err := c.Ready(ctx); err == nil {
		m.client = c
		return nil
	}
	if !local.autoStart() {
		return errs.New(errs.Invalid, "Appium is not answering at %s and auto_start_appium is off", local.appiumURL()).
			WithHint("start it with `appium`, or run `sapien device doctor`")
	}
	u, err := url.Parse(local.appiumURL())
	if err != nil {
		return errs.Wrap(errs.Invalid, err, "invalid appium_url")
	}
	port := u.Port()
	if port == "" {
		port = "4723"
	}
	logPath := filepath.Join(m.opts.StateDir, "appium.log")
	m.progress("starting Appium on port %s (log: %s) ...", port, logPath)
	stop, err := m.ex.Start(local.appiumBin(), []string{"--port", port, "--log-no-colors"}, logPath)
	if err != nil {
		return errs.Wrap(errs.Invalid, err, "starting appium").
			WithHint("install it: npm install -g appium && appium driver install uiautomator2 (or run `sapien device doctor`)")
	}
	m.stopFns = append(m.stopFns, stop)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.Ready(ctx); err == nil {
			m.client = c
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return errs.New(errs.Invalid, "Appium did not come up within 60s (see %s)", logPath)
}

// ensureSession opens the run's one WebDriver session: a device-wide
// UiAutomator2 session (no appPackage), so it can drive several apps and
// leaves launching to the flow's own `launch` action.
func (m *Manager) ensureSession(ctx context.Context) error {
	if m.session != nil {
		return nil
	}
	m.progress("opening Appium session on %s ...", m.adb.Serial)
	caps := map[string]any{
		"platformName":                           "Android",
		"appium:automationName":                  "UiAutomator2",
		"appium:udid":                            m.adb.Serial,
		"appium:noReset":                         true,
		"appium:newCommandTimeout":               600,
		"appium:disableWindowAnimation":          true,
		"appium:uiautomator2ServerLaunchTimeout": 120000,
	}
	s, err := m.client.NewSession(ctx, caps)
	if err != nil {
		return errs.Wrap(errs.Invalid, err, "creating Appium session").
			WithHint("is the uiautomator2 driver installed? `appium driver install uiautomator2`; see .sapien/appium.log")
	}
	m.session = s
	return nil
}

// ensureLogcat starts one logcat stream per tag for the run.
func (m *Manager) ensureLogcat(tag string) *logs.Buffer {
	if b, ok := m.logcats[tag]; ok {
		return b
	}
	b := logs.NewBuffer(0)
	m.logcats[tag] = b
	ctx, cancel := context.WithCancel(context.Background())
	prev := m.logCancel
	m.logCancel = func() {
		cancel()
		if prev != nil {
			prev()
		}
	}
	adb := m.adb
	go func() { _ = adb.Logcat(ctx, tag, b.Append) }()
	return b
}

// prepareApp builds (when asked or when the APK is missing) and installs
// (when the APK changed since the last install on this device) the app.
func (m *Manager) prepareApp(ctx context.Context, name string, cfg domain.AppConfig) error {
	if m.prepared[name] {
		return nil
	}
	repo, apk := resolveAppPaths(m.opts.WorkspaceDir, name, cfg, m.opts.Local)
	if apk == "" {
		if !m.adb.Installed(ctx, cfg.Package) {
			return errs.New(errs.Invalid, "app %q (%s) is not installed and has no apk: to install", name, cfg.Package)
		}
		m.prepared[name] = true
		return nil
	}
	_, statErr := os.Stat(apk)
	if m.opts.Rebuild || os.IsNotExist(statErr) {
		if cfg.Build == "" {
			return errs.New(errs.Invalid, "app %q: %s does not exist and there is no build: command", name, apk)
		}
		logPath := filepath.Join(m.opts.StateDir, "build-"+name+".log")
		m.progress("building %s: %s (in %s; log: %s) ...", name, cfg.Build, repo, logPath)
		// Bounded: a build tool that stops to ask something (fvm offering to
		// install a pinned SDK) would otherwise hang the run with no output.
		bctx, cancel := context.WithTimeout(ctx, buildTimeout)
		out, err := m.ex.Run(bctx, repo, "sh", "-c", cfg.Build)
		cancel()
		_ = os.WriteFile(logPath, []byte(out), 0o644)
		if bctx.Err() == context.DeadlineExceeded {
			return errs.New(errs.Invalid, "building app %q took longer than %s; is the build command waiting for input?", name, buildTimeout).
				WithHint("run it by hand in " + repo + " to see; output so far is in " + logPath)
		}
		if err != nil {
			return errs.Wrap(errs.Invalid, err, "building app %q", name).WithHint("see " + logPath)
		}
		if _, err := os.Stat(apk); err != nil {
			return errs.New(errs.Invalid, "app %q: build succeeded but %s does not exist; check apk:", name, apk)
		}
	}
	sum, err := fileHash(apk)
	if err != nil {
		return errs.Wrap(errs.Invalid, err, "reading %s", apk)
	}
	state := loadInstallState(m.opts.StateDir)
	key := m.adb.Serial + "/" + cfg.Package
	if state[key] != sum || !m.adb.Installed(ctx, cfg.Package) {
		m.progress("installing %s on %s ...", filepath.Base(apk), m.adb.Serial)
		if err := m.adb.Install(ctx, apk); err != nil {
			return errs.Wrap(errs.Invalid, err, "installing %s", apk)
		}
		state[key] = sum
		saveInstallState(m.opts.StateDir, state)
	}
	m.prepared[name] = true
	return nil
}

// Close ends the session, the logcat streams, and a self-started Appium.
// A booted emulator is left running for the next run.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session != nil {
		_ = m.session.Delete(ctx)
		m.session = nil
	}
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	for _, stop := range m.stopFns {
		stop()
	}
	m.stopFns = nil
	return nil
}

const installStateFile = "device-state.json"

// buildTimeout bounds an app's build command; a cold Flutter debug build
// takes a few minutes.
var buildTimeout = 15 * time.Minute

func loadInstallState(stateDir string) map[string]string {
	st := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(stateDir, installStateFile)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveInstallState(stateDir string, st map[string]string) {
	if b, err := json.MarshalIndent(st, "", "  "); err == nil {
		_ = os.MkdirAll(stateDir, 0o755)
		_ = os.WriteFile(filepath.Join(stateDir, installStateFile), b, 0o644)
	}
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
