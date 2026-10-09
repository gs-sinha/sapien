package device

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Exec runs external commands. The real one shells out; tests swap in a
// fake so the device layer can be exercised without adb or an emulator.
type Exec interface {
	// Run runs name with args (in dir when non-empty) and returns its
	// combined output.
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
	// Stream starts name with args and calls onLine for each stdout line
	// until ctx is cancelled or the process exits.
	Stream(ctx context.Context, onLine func(string), name string, args ...string) error
	// Start launches a long-lived background process, writing its output to
	// logPath, and returns a func that stops it.
	Start(name string, args []string, logPath string) (stop func(), err error)
}

// OSExec is Exec over os/exec.
type OSExec struct{}

func (OSExec) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, lastLines(out.String(), 5))
	}
	return out.String(), nil
}

func (OSExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		onLine(sc.Text())
	}
	return cmd.Wait()
}

func (OSExec) Start(name string, args []string, logPath string) (func(), error) {
	var w io.Writer = io.Discard
	var f *os.File
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
			if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				f, w = lf, lf
			}
		}
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		if f != nil {
			f.Close()
		}
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if f != nil {
			f.Close()
		}
	}, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// ADB wraps the adb commands ui steps need, against one device serial.
type ADB struct {
	Exec   Exec
	Bin    string // adb binary
	Serial string
}

func (a *ADB) args(args ...string) []string {
	if a.Serial == "" {
		return args
	}
	return append([]string{"-s", a.Serial}, args...)
}

// Run runs `adb [-s serial] args...`.
func (a *ADB) Run(ctx context.Context, args ...string) (string, error) {
	return a.Exec.Run(ctx, "", a.Bin, a.args(args...)...)
}

// Shell runs `adb shell args...`.
func (a *ADB) Shell(ctx context.Context, args ...string) (string, error) {
	return a.Run(ctx, append([]string{"shell"}, args...)...)
}

// Devices lists attached device serials in the `device` state.
func Devices(ctx context.Context, ex Exec, bin string) ([]string, error) {
	out, err := ex.Run(ctx, "", bin, "devices")
	if err != nil {
		return nil, err
	}
	var serials []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && f[1] == "device" {
			serials = append(serials, f[0])
		}
	}
	return serials, nil
}

// Install installs (or replaces) an APK, allowing test-only and downgrade
// installs so a debug build always goes on.
func (a *ADB) Install(ctx context.Context, apk string) error {
	_, err := a.Run(ctx, "install", "-r", "-t", "-d", apk)
	return err
}

// Clear wipes an app's data (logs it out, resets prefs).
func (a *ADB) Clear(ctx context.Context, pkg string) error {
	_, err := a.Shell(ctx, "pm", "clear", pkg)
	return err
}

// Grant grants runtime permissions (best effort: a permission the app does
// not declare, or one the OS version lacks, is skipped) and allows drawing
// over other apps, which rider_app_fe's offer overlay needs.
func (a *ADB) Grant(ctx context.Context, pkg string, perms []string) {
	for _, p := range perms {
		if !strings.Contains(p, ".") {
			p = "android.permission." + p
		}
		_, _ = a.Shell(ctx, "pm", "grant", pkg, p)
	}
	_, _ = a.Shell(ctx, "appops", "set", pkg, "SYSTEM_ALERT_WINDOW", "allow")
}

// DisableAnimations turns off the three system animation scales, so taps
// never land mid-transition.
func (a *ADB) DisableAnimations(ctx context.Context) {
	for _, k := range []string{"window_animation_scale", "transition_animation_scale", "animator_duration_scale"} {
		_, _ = a.Shell(ctx, "settings", "put", "global", k, "0")
	}
}

// BootCompleted reports whether the device finished booting.
func (a *ADB) BootCompleted(ctx context.Context) bool {
	out, err := a.Shell(ctx, "getprop", "sys.boot_completed")
	return err == nil && strings.TrimSpace(out) == "1"
}

// Installed reports whether pkg is installed.
func (a *ADB) Installed(ctx context.Context, pkg string) bool {
	out, err := a.Shell(ctx, "pm", "path", pkg)
	return err == nil && strings.Contains(out, "package:")
}

// ForegroundActivity returns the resumed activity, e.g.
// com.blitznow.kaptaan/.MainActivity ("" when unknown).
func (a *ADB) ForegroundActivity(ctx context.Context) string {
	out, err := a.Shell(ctx, "dumpsys", "activity", "activities")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "mResumedActivity") || strings.HasPrefix(l, "topResumedActivity") || strings.HasPrefix(l, "ResumedActivity") {
			for _, f := range strings.Fields(l) {
				if strings.Contains(f, "/") {
					return strings.TrimSuffix(f, "}")
				}
			}
		}
	}
	return ""
}

// Logcat streams the device's log lines for tag (all levels), starting
// from now, to onLine until ctx ends.
func (a *ADB) Logcat(ctx context.Context, tag string, onLine func(string)) error {
	return a.Exec.Stream(ctx, onLine, a.Bin, a.args("logcat", "-v", "brief", "-T", "1", tag+":V", "*:S")...)
}
