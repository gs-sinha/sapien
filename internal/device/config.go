// Package device runs ui steps against a local Android device: it finds or
// boots an emulator, starts (or reuses) an Appium server, builds and
// installs the app under test, keeps one WebDriver session for the whole
// run, and streams the app's logcat so each step can see the network calls
// it caused.
package device

import (
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
)

// LocalConfigFile is the per-machine ui settings file, inside the
// workspace's gitignored .sapien/ directory.
const LocalConfigFile = "ui.yaml"

// DefaultAppiumURL is where a locally started Appium listens.
const DefaultAppiumURL = "http://127.0.0.1:4723"

// LocalConfig is .sapien/ui.yaml: what differs per machine.
type LocalConfig struct {
	AppiumURL string `yaml:"appium_url,omitempty"`
	// AutoStartAppium starts `appium` as a child process when nothing
	// answers at AppiumURL (default true).
	AutoStartAppium *bool  `yaml:"auto_start_appium,omitempty"`
	AppiumBin       string `yaml:"appium_bin,omitempty"`
	// AndroidHome overrides $ANDROID_HOME for finding adb and emulator.
	AndroidHome string `yaml:"android_home,omitempty"`
	// AVD is booted when no device is attached.
	AVD string `yaml:"avd,omitempty"`
	// Serial picks one of several attached devices (`adb devices`).
	Serial string `yaml:"serial,omitempty"`
	// Attach lists apps to drive as already running (e.g. under `flutter
	// run`): never built, installed, or cleared.
	Attach []string `yaml:"attach,omitempty"`
	// Repos overrides an app's `repo:` on this machine.
	Repos map[string]string `yaml:"repos,omitempty"`
}

// LoadLocalConfig reads <stateDir>/ui.yaml; a missing file is an empty
// config.
func LoadLocalConfig(stateDir string) (LocalConfig, error) {
	var c LocalConfig
	b, err := os.ReadFile(filepath.Join(stateDir, LocalConfigFile))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = yaml.Unmarshal(b, &c)
	return c, err
}

func (c LocalConfig) appiumURL() string {
	if c.AppiumURL != "" {
		return c.AppiumURL
	}
	return DefaultAppiumURL
}

func (c LocalConfig) autoStart() bool { return c.AutoStartAppium == nil || *c.AutoStartAppium }

func (c LocalConfig) androidHome() string {
	if c.AndroidHome != "" {
		return c.AndroidHome
	}
	if h := os.Getenv("ANDROID_HOME"); h != "" {
		return h
	}
	if h := os.Getenv("ANDROID_SDK_ROOT"); h != "" {
		return h
	}
	if home, err := os.UserHomeDir(); err == nil {
		def := filepath.Join(home, "Library", "Android", "sdk")
		if _, err := os.Stat(def); err == nil {
			return def
		}
	}
	return ""
}

// sdkTool finds an Android SDK binary: under the SDK when known, else PATH.
func (c LocalConfig) sdkTool(sub, name string) string {
	if h := c.androidHome(); h != "" {
		p := filepath.Join(h, sub, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

func (c LocalConfig) adbBin() string      { return c.sdkTool("platform-tools", "adb") }
func (c LocalConfig) emulatorBin() string { return c.sdkTool("emulator", "emulator") }

func (c LocalConfig) appiumBin() string {
	if c.AppiumBin != "" {
		return c.AppiumBin
	}
	return "appium"
}

// resolveAppPaths returns app's repo directory and APK path, absolute, with
// the machine's repo override applied.
func resolveAppPaths(workspaceDir, name string, app domain.AppConfig, local LocalConfig) (repo, apk string) {
	repo = app.Repo
	if r, ok := local.Repos[name]; ok && r != "" {
		repo = r
	}
	if repo != "" && !filepath.IsAbs(repo) {
		repo = filepath.Join(workspaceDir, repo)
	}
	apk = app.APK
	if apk != "" && !filepath.IsAbs(apk) && repo != "" {
		apk = filepath.Join(repo, apk)
	}
	return repo, apk
}
