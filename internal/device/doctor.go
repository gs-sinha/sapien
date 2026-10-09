package device

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gs-sinha/sapien/internal/device/appium"
)

// Check is one line of `sapien device doctor`.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	// Fix is the command (or edit) that resolves a failed check.
	Fix string `json:"fix,omitempty"`
	// Optional checks warn instead of failing the doctor.
	Optional bool `json:"optional,omitempty"`
}

var nodeVersionRe = regexp.MustCompile(`v(\d+)\.(\d+)`)

// Doctor checks everything ui steps need on this machine, in dependency
// order, with the fix for each gap.
func Doctor(ctx context.Context, local LocalConfig, ex Exec) []Check {
	if ex == nil {
		ex = OSExec{}
	}
	var checks []Check
	add := func(c Check) { checks = append(checks, c) }

	// Node: Appium 3 needs ^20.19 || ^22.12 || >=24; Appium 2 runs on 18+.
	// The bar depends on which Appium is installed, so read that first.
	appiumBin := local.appiumBin()
	appiumOut, appiumErr := ex.Run(ctx, "", appiumBin, "--version")
	appiumVersion := strings.TrimSpace(lastLine(appiumOut))
	appium2 := appiumErr == nil && strings.HasPrefix(appiumVersion, "2.")
	if out, err := ex.Run(ctx, "", "node", "--version"); err != nil {
		add(Check{Name: "node", Detail: "not found", Fix: "install Node 22 (e.g. `nvm install 22`)"})
	} else {
		v := strings.TrimSpace(out)
		major, minor := 0, 0
		if m := nodeVersionRe.FindStringSubmatch(v); m != nil {
			major, _ = strconv.Atoi(m[1])
			minor, _ = strconv.Atoi(m[2])
		}
		if appium2 {
			add(Check{Name: "node", OK: major >= 18, Detail: v + " (Appium 2)", Fix: "Appium 2 needs Node 18+: `nvm install 22 && nvm use 22`"})
		} else {
			ok := (major == 20 && minor >= 19) || (major == 22 && minor >= 12) || major >= 24
			add(Check{Name: "node", OK: ok, Detail: v, Fix: "Appium 3 needs Node 20.19+ or 22.12+: `nvm install 22 && nvm use 22` (then reinstall global npm packages), or stay on this Node with `npm install -g appium@2`"})
		}
	}

	if appiumErr != nil {
		add(Check{Name: "appium", Detail: "not found", Fix: "npm install -g appium"})
	} else {
		add(Check{Name: "appium", OK: true, Detail: appiumVersion})
		out, err := ex.Run(ctx, "", appiumBin, "driver", "list", "--installed", "--json")
		installed := err == nil && strings.Contains(out, "uiautomator2")
		if err == nil {
			var drivers map[string]any
			if jerr := json.Unmarshal([]byte(jsonPart(out)), &drivers); jerr == nil {
				_, installed = drivers["uiautomator2"]
			}
		}
		driverFix := "appium driver install uiautomator2"
		if appium2 {
			driverFix = "appium driver install uiautomator2@3.10.0 (the last release for Appium 2)"
		}
		add(Check{Name: "uiautomator2 driver", OK: installed, Detail: map[bool]string{true: "installed", false: "not installed"}[installed], Fix: driverFix})
	}

	home := local.androidHome()
	switch {
	case home == "":
		add(Check{Name: "ANDROID_HOME", Detail: "not set and no SDK at ~/Library/Android/sdk", Fix: "install the Android SDK (Android Studio), then `export ANDROID_HOME=$HOME/Library/Android/sdk` in ~/.zshrc"})
	case os.Getenv("ANDROID_HOME") == "" && os.Getenv("ANDROID_SDK_ROOT") == "":
		add(Check{Name: "ANDROID_HOME", Detail: "not set; Sapien found the SDK at " + home + " but Appium also needs it", Fix: "echo 'export ANDROID_HOME=" + home + "' >> ~/.zshrc, then restart the terminal (and the Sapien daemon)"})
	default:
		add(Check{Name: "ANDROID_HOME", OK: true, Detail: home})
	}

	add(javaCheck(ctx, ex))

	adb := local.adbBin()
	serials, err := Devices(ctx, ex, adb)
	if err != nil {
		add(Check{Name: "adb", Detail: err.Error(), Fix: "install Android SDK platform-tools"})
	} else {
		add(Check{Name: "adb", OK: true, Detail: adb})
		avds := listAVDs(ctx, ex, local.emulatorBin())
		switch {
		case len(serials) > 0:
			add(Check{Name: "device", OK: true, Detail: "attached: " + strings.Join(serials, ", ")})
		case local.AVD != "" && contains(avds, local.AVD):
			add(Check{Name: "device", OK: true, Detail: "none attached; ui steps will boot avd " + local.AVD})
		case local.AVD != "":
			add(Check{Name: "device", Detail: fmt.Sprintf("avd %q (ui.yaml) does not exist; have: %s", local.AVD, strings.Join(avds, ", ")), Fix: "fix avd: in .sapien/ui.yaml"})
		case len(avds) > 0:
			add(Check{Name: "device", Detail: "none attached; AVDs available: " + strings.Join(avds, ", "), Fix: "start one (`emulator -avd " + avds[0] + "`) or set `avd: " + avds[0] + "` in .sapien/ui.yaml so ui steps boot it"})
		default:
			add(Check{Name: "device", Detail: "none attached and no AVDs", Fix: "create an emulator in Android Studio's Device Manager"})
		}
	}

	c := appium.New(local.appiumURL())
	if err := c.Ready(ctx); err == nil {
		add(Check{Name: "appium server", OK: true, Detail: "answering at " + local.appiumURL()})
	} else if local.autoStart() {
		add(Check{Name: "appium server", OK: true, Optional: true, Detail: "not running; ui steps will start it"})
	} else {
		add(Check{Name: "appium server", Detail: "not answering at " + local.appiumURL() + " and auto_start_appium is off", Fix: "run `appium` in another terminal"})
	}
	return checks
}

func javaCheck(ctx context.Context, ex Exec) Check {
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		if _, err := os.Stat(filepath.Join(jh, "bin", "java")); err == nil {
			return Check{Name: "JAVA_HOME", OK: true, Detail: jh}
		}
		return Check{Name: "JAVA_HOME", Detail: jh + " has no bin/java", Fix: "point JAVA_HOME at a JDK 17+ (`brew install openjdk@17`)"}
	}
	fix := "brew install openjdk@17, then `export JAVA_HOME=$(/usr/libexec/java_home -v 17)` in ~/.zshrc"
	if out, err := ex.Run(ctx, "", "/usr/libexec/java_home"); err == nil && strings.TrimSpace(out) != "" {
		return Check{Name: "JAVA_HOME", Detail: "not set; a JDK exists at " + strings.TrimSpace(out), Fix: "echo 'export JAVA_HOME=$(/usr/libexec/java_home)' >> ~/.zshrc"}
	}
	return Check{Name: "JAVA_HOME", Detail: "not set and no JDK found (Appium's UiAutomator2 driver needs one to sign its helper APKs)", Fix: fix}
}

func listAVDs(ctx context.Context, ex Exec, emulator string) []string {
	out, err := ex.Run(ctx, "", emulator, "-list-avds")
	if err != nil {
		return nil
	}
	var avds []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "INFO") && !strings.Contains(l, " ") {
			avds = append(avds, l)
		}
	}
	return avds
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// jsonPart drops any log noise before the first `{`.
func jsonPart(s string) string {
	if i := strings.Index(s, "{"); i >= 0 {
		return s[i:]
	}
	return s
}

// Element is one node of the current screen, as `sapien device snapshot`
// lists it: the values a selector can match.
type Element struct {
	ID        string `json:"id,omitempty"` // resource-id (Flutter Semantics identifier)
	Text      string `json:"text,omitempty"`
	Desc      string `json:"desc,omitempty"` // content-desc (Flutter label)
	Hint      string `json:"hint,omitempty"`
	Class     string `json:"class"`
	Clickable bool   `json:"clickable,omitempty"`
	Bounds    string `json:"bounds,omitempty"`
}

// Selector returns the selector to write for e in a flow, preferring a
// stable id, else its text or label, else its hint.
func (e Element) Selector() string {
	switch {
	case e.ID != "":
		return "{id: " + yamlQuote(e.ID) + "}"
	case e.Text != "":
		return "{text: " + yamlQuote(e.Text) + "}"
	case e.Desc != "":
		return "{text: " + yamlQuote(e.Desc) + "}"
	case e.Hint != "":
		return "{hint: " + yamlQuote(e.Hint) + "}"
	}
	return ""
}

func yamlQuote(s string) string {
	if strings.ContainsAny(s, ":{}[],#&*!|>'\"%@`\n") || strings.TrimSpace(s) != s {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return s
}

// ParseSource lists the elements of a UiAutomator2 page source that a
// selector could target: those with an id, text, label, or hint.
func ParseSource(src string) []Element {
	dec := xml.NewDecoder(strings.NewReader(src))
	var out []Element
	for {
		tok, err := dec.Token()
		if err == io.EOF || err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		e := Element{Class: se.Name.Local}
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "resource-id":
				e.ID = a.Value
			case "text":
				e.Text = a.Value
			case "content-desc":
				e.Desc = a.Value
			case "hint":
				e.Hint = a.Value
			case "class":
				e.Class = a.Value
			case "clickable":
				e.Clickable = a.Value == "true"
			case "bounds":
				e.Bounds = a.Value
			}
		}
		if e.Hint == e.Text {
			e.Hint = "" // an empty field reports its hint as text too
		}
		if e.ID != "" || e.Text != "" || e.Desc != "" || e.Hint != "" {
			out = append(out, e)
		}
	}
	return out
}

// Snapshot lists the current screen's elements and its screenshot,
// without building, installing, or launching anything.
func (m *Manager) Snapshot(ctx context.Context) ([]Element, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureDevice(ctx); err != nil {
		return nil, nil, err
	}
	if err := m.ensureAppium(ctx); err != nil {
		return nil, nil, err
	}
	if err := m.ensureSession(ctx); err != nil {
		return nil, nil, err
	}
	src, err := m.session.Source(ctx)
	if err != nil {
		return nil, nil, err
	}
	png, _ := m.session.Screenshot(ctx)
	return ParseSource(src), png, nil
}
