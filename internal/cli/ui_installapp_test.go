package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
)

func TestPlistVersion_SanitisesNonNumericVersions(t *testing.T) {
	// CFBundleShortVersionString is one to three integers; a dev build's
	// version is not, and macOS rejects the bundle rather than ignoring
	// the key.
	for in, want := range map[string]string{
		"1.1.0":          "1.1.0",
		"v1.1.0":         "1.1.0",
		"1.2":            "1.2",
		"1":              "1",
		"1.1.0-rc.1":     "1.1.0",
		"v1.1.0+abcdef":  "1.1.0",
		"dev":            "0.0.0",
		"":               "0.0.0",
		"not-a-version":  "0.0.0",
		"1.1.0.4.5.6.77": "1.1.0",
	} {
		assert.Equal(t, want, cli.PlistVersion(in), "version %q", in)
	}
}

func TestShellQuote_SurvivesSpacesAndQuotes(t *testing.T) {
	assert.Equal(t, `'/Users/a b/sapien'`, cli.ShellQuote("/Users/a b/sapien"))
	assert.Equal(t, `'/Users/o'\''brien/ws'`, cli.ShellQuote("/Users/o'brien/ws"))
}

// The launcher must name the binary absolutely: a Finder-launched process
// inherits none of the login shell's environment, so a bare `sapien` would
// never be found under Homebrew.
func TestLauncherScript_NamesBinaryAndWorkspaceAbsolutely(t *testing.T) {
	script := cli.LauncherScript("/opt/homebrew/bin/sapien", "/Users/me/Desktop/ws")
	assert.Contains(t, script, "#!/bin/sh")
	assert.Contains(t, script, "SAPIEN='/opt/homebrew/bin/sapien'")
	assert.Contains(t, script, "WORKSPACE='/Users/me/Desktop/ws'")
	assert.Contains(t, script, `"$SAPIEN" ui --workspace "$WORKSPACE"`)
	// A Finder launch has no terminal, so a failure has to land somewhere
	// the user can be pointed at.
	assert.Contains(t, script, "Library/Logs/Sapien/launch.log")
}

func TestLauncherScript_QuotesAWorkspaceWithASpace(t *testing.T) {
	script := cli.LauncherScript("/usr/local/bin/sapien", "/Users/me/My Work/ws")
	assert.Contains(t, script, `WORKSPACE='/Users/me/My Work/ws'`)
}

func TestRemoveExistingBundle_RefusesSomethingThatIsNotOurs(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "Sapien.app")
	require.NoError(t, os.MkdirAll(filepath.Join(foreign, "Contents", "MacOS"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(foreign, "Contents", "MacOS", "SomethingElse"), []byte("x"), 0o755))

	err := cli.RemoveExistingBundle(foreign)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not look like a Sapien launcher")
	assert.DirExists(t, foreign, "a recursive delete must not touch a bundle we did not write")
}

func TestRemoveExistingBundle_MissingPathIsNotAnError(t *testing.T) {
	require.NoError(t, cli.RemoveExistingBundle(filepath.Join(t.TempDir(), "Sapien.app")))
}

func TestInstallAppBundle_WritesARunnableBundle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the .app bundle is macOS-only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()

	bundle, err := cli.InstallAppBundle(ws)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "Applications", "Sapien.app"), bundle)

	plist, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	require.NoError(t, err)
	assert.Contains(t, string(plist), "<key>CFBundleExecutable</key>")
	assert.Contains(t, string(plist), "<string>Sapien</string>")
	assert.Contains(t, string(plist), "<key>CFBundleIconFile</key>")

	// The Dock tile, Spotlight hit and browser tab should be the same
	// icon; a plist naming an icon that is not in Resources shows the
	// generic one instead, silently.
	icns, err := os.ReadFile(filepath.Join(bundle, "Contents", "Resources", "Sapien.icns"))
	require.NoError(t, err)
	assert.Equal(t, []byte("icns"), icns[:4], "the embedded icon must be a real .icns")
	assert.Greater(t, len(icns), 1024)

	launcher := filepath.Join(bundle, "Contents", "MacOS", "Sapien")
	info, err := os.Stat(launcher)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "the bundle executable must be executable")

	script, err := os.ReadFile(launcher)
	require.NoError(t, err)
	assert.Contains(t, string(script), ws)
}

// Re-running is how you repoint the launcher at another workspace, so it
// must replace rather than refuse.
func TestInstallAppBundle_ReinstallRepointsTheWorkspace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the .app bundle is macOS-only")
	}
	t.Setenv("HOME", t.TempDir())
	first, second := t.TempDir(), t.TempDir()

	_, err := cli.InstallAppBundle(first)
	require.NoError(t, err)
	bundle, err := cli.InstallAppBundle(second)
	require.NoError(t, err)

	script, err := os.ReadFile(filepath.Join(bundle, "Contents", "MacOS", "Sapien"))
	require.NoError(t, err)
	assert.Contains(t, string(script), second)
	assert.NotContains(t, string(script), first)
}
