package cli

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/errs"
)

// icnsData is the bundle's icon, built from the 🗿 the UI uses as its
// favicon so the Dock tile, Spotlight hit and browser tab are recognisably
// the same thing.
//
//go:embed Sapien.icns
var icnsData []byte

// appBundleName is the .app written into ~/Applications. It is also the
// name Spotlight, Raycast and Alfred will match on, and the label under a
// Dock tile, so it is the product name and nothing else.
const appBundleName = "Sapien.app"

// bundleIdentifier follows the repository rather than a domain nobody
// owns; nothing here is signed or submitted anywhere, so the only job it
// has is to be unique on the machine.
const bundleIdentifier = "com.github.gs-sinha.sapien.launcher"

// iconFileName is CFBundleIconFile's value and the Resources basename;
// the ".icns" extension is written on disk and omitted in the plist, which
// is the convention LaunchServices expects.
const iconFileName = "Sapien"

// installAppBundle writes ~/Applications/Sapien.app: a launcher that runs
// `sapien ui` for workspaceDir.
//
// It is a shim around the CLI rather than anything that holds a URL,
// because a URL cannot survive. The daemon's port moves whenever it is
// replaced, and serve.go mints a fresh bearer token on every start, so a
// bookmark, a Dock URL or a Chrome PWA install is stale as soon as the
// daemon idle-exits -- which it does after thirty minutes with no tab
// open, i.e. exactly when you would next reach for the launcher. Going
// through `sapien ui` re-resolves the port and mints a fresh session
// every time, so the shim never goes stale and never needs updating: a
// `brew upgrade sapien` replaces the binary at the same path, and the UI
// it serves is embedded in that binary (internal/ui/embed.go).
func installAppBundle(workspaceDir string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errs.New(errs.Invalid,
			"--install-app builds a macOS .app bundle and only works on darwin (this is %s); on linux, create a .desktop entry running `sapien ui`", runtime.GOOS)
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "resolving the running sapien binary's path")
	}
	// EvalSymlinks deliberately NOT called: on Homebrew the binary on
	// PATH is a symlink into the Cellar, and the symlink is the stable
	// name -- resolving it would pin the bundle to one version's Cellar
	// directory, which the next `brew upgrade` deletes.
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "making %s absolute", exePath)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "resolving the home directory")
	}
	appsDir := filepath.Join(home, "Applications")
	bundle := filepath.Join(appsDir, appBundleName)

	if err := removeExistingBundle(bundle); err != nil {
		return "", err
	}
	macOSDir := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macOSDir, 0o755); err != nil {
		return "", errs.Wrap(errs.Internal, err, "creating %s", macOSDir)
	}

	plist := filepath.Join(bundle, "Contents", "Info.plist")
	if err := os.WriteFile(plist, []byte(infoPlist(Version)), 0o644); err != nil {
		return "", errs.Wrap(errs.Internal, err, "writing %s", plist)
	}
	resourcesDir := filepath.Join(bundle, "Contents", "Resources")
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		return "", errs.Wrap(errs.Internal, err, "creating %s", resourcesDir)
	}
	icon := filepath.Join(resourcesDir, iconFileName+".icns")
	if err := os.WriteFile(icon, icnsData, 0o644); err != nil {
		return "", errs.Wrap(errs.Internal, err, "writing %s", icon)
	}

	launcher := filepath.Join(macOSDir, "Sapien")
	if err := os.WriteFile(launcher, []byte(launcherScript(exePath, workspaceDir)), 0o755); err != nil {
		return "", errs.Wrap(errs.Internal, err, "writing %s", launcher)
	}

	// LaunchServices indexes a bundle by its modification time; rewriting
	// files inside it does not always bump the directory's own, which
	// leaves Spotlight serving the previous entry.
	now := time.Now()
	_ = os.Chtimes(bundle, now, now)

	return bundle, nil
}

// removeExistingBundle clears a previous install, refusing anything at
// that path that is not recognisably one of our bundles -- the path is
// fixed and inside ~/Applications, but it is still a recursive delete, so
// it happens only to a directory carrying the launcher we wrote.
func removeExistingBundle(bundle string) error {
	info, err := os.Lstat(bundle)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errs.Wrap(errs.Internal, err, "inspecting %s", bundle)
	}
	if !info.IsDir() {
		return errs.New(errs.Conflict, "%s exists and is not a directory; move it aside and re-run", bundle)
	}
	if _, err := os.Stat(filepath.Join(bundle, "Contents", "MacOS", "Sapien")); err != nil {
		return errs.New(errs.Conflict,
			"%s exists but does not look like a Sapien launcher (no Contents/MacOS/Sapien); move it aside and re-run", bundle)
	}
	if err := os.RemoveAll(bundle); err != nil {
		return errs.Wrap(errs.Internal, err, "replacing %s", bundle)
	}
	return nil
}

// launcherScript is the bundle's executable. It runs `sapien ui`, which
// finds or starts the workspace's daemon and opens the session URL in the
// *default* browser (ui.go's openURL uses `open`, so this follows
// whatever the user has set, not a particular browser).
//
// Two things it has to get right that a hand-written wrapper usually does
// not. A Finder-launched process inherits none of the login shell's
// environment, so PATH does not contain Homebrew and the binary must be
// named absolutely; and it has no terminal, so a failure would otherwise
// be completely silent -- hence the log file and the one alert.
func launcherScript(exePath, workspaceDir string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Generated by `sapien ui --install-app`. Re-run that command to repoint it.\n")
	b.WriteString("SAPIEN=" + shellQuote(exePath) + "\n")
	b.WriteString("WORKSPACE=" + shellQuote(workspaceDir) + "\n")
	b.WriteString(`LOG="$HOME/Library/Logs/Sapien/launch.log"
mkdir -p "$(dirname "$LOG")" 2>/dev/null

# The recorded path is the one ` + "`sapien ui --install-app`" + ` ran from. If the
# binary moved (a different install method, a deleted Cellar), fall back to
# PATH -- thin, since Finder gives us only the system default PATH, but it
# costs nothing and covers /usr/local/bin.
if [ ! -x "$SAPIEN" ]; then
  SAPIEN="$(command -v sapien 2>/dev/null)"
fi
if [ ! -x "$SAPIEN" ]; then
  echo "$(date): sapien binary not found; re-run 'sapien ui --install-app'" >>"$LOG"
  osascript -e 'display alert "Sapien not found" message "The sapien binary has moved. Re-run: sapien ui --install-app"' >/dev/null 2>&1
  exit 1
fi

echo "$(date): launching $SAPIEN ui --workspace $WORKSPACE" >>"$LOG"
if ! "$SAPIEN" ui --workspace "$WORKSPACE" >>"$LOG" 2>&1; then
  osascript -e 'display alert "Sapien could not start" message "See ~/Library/Logs/Sapien/launch.log"' >/dev/null 2>&1
  exit 1
fi
`)
	return b.String()
}

// shellQuote renders s as a single-quoted POSIX shell word, so a path
// with a space (or a quote) in it survives.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// plistVersionRE matches the leading dotted-numeric part of a version
// string. CFBundleShortVersionString is specified as one to three
// integers, and a dev build's version ("dev", or a v-prefixed tag with a
// commit suffix) is not one.
var plistVersionRE = regexp.MustCompile(`^v?(\d+(?:\.\d+){0,2})`)

func plistVersion(version string) string {
	if m := plistVersionRE.FindStringSubmatch(version); m != nil {
		return m[1]
	}
	return "0.0.0"
}

func infoPlist(version string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>Sapien</string>
	<key>CFBundleDisplayName</key>
	<string>Sapien</string>
	<key>CFBundleIdentifier</key>
	<string>%s</string>
	<key>CFBundleExecutable</key>
	<string>Sapien</string>
	<key>CFBundleIconFile</key>
	<string>%s</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleVersion</key>
	<string>%s</string>
	<key>CFBundleShortVersionString</key>
	<string>%s</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
`, bundleIdentifier, iconFileName, plistVersion(version), plistVersion(version))
}
