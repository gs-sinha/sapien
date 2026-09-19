package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/selfupdate"
)

func init() { Register(newUpgradeCmd) }

// releasesBaseURL and upgradeExecutablePath are test seams for `sapien
// upgrade`, the same shape as spawnDaemonFunc above: empty means the real
// behavior (the actual GitHub Releases root; the actual running binary),
// and a test overrides them (SetReleasesBaseURL, SetUpgradeExecutablePath
// in export_test.go) so it can exercise a real download-verify-replace
// cycle against an httptest.Server and a throwaway file, never the real
// network or the go-test binary that is running the test.
var (
	releasesBaseURL       string
	upgradeExecutablePath string
)

// newUpgradeCmd is `sapien upgrade [--version vX.Y.Z] [--check]` (PLAN
// §34f item 4). For a script install -- the one method it can act on
// itself -- it downloads the release archive, verifies checksums.txt,
// replaces its own binary atomically (selfupdate.Apply), and restarts a
// daemon already running for whatever workspace is discoverable, if any:
// best-effort, since `sapien upgrade` is meant to work from anywhere, not
// only from inside a workspace. For every other install method (Homebrew,
// go install, a dev build) it prints the right command and exits 0
// without changing anything -- those tools already own the binary, and a
// self-replace here would only be undone by the next `brew upgrade`/`go
// install`/`git pull` anyway. This is the CLI counterpart of POST
// /v1/update/apply, which runs the same selfupdate.Apply in-process.
func newUpgradeCmd(app *App) *cobra.Command {
	var version string
	var checkOnly bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the sapien binary to the latest (or a given) release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := selfupdate.Executable()
			if err != nil {
				return err
			}
			method := selfupdate.DetectMethod(Version, exe)

			if checkOnly {
				return runUpgradeCheck(cmd.Context(), app, method)
			}

			if !selfupdate.CanSelfUpgrade(method) {
				cmdStr := selfupdate.Command(method)
				if app.Printer.IsJSON() {
					return app.Printer.JSON(map[string]any{
						"upgraded":       false,
						"install_method": string(method),
						"command":        cmdStr,
					})
				}
				app.Printer.Line("sapien was installed via %s; run this instead:", method)
				app.Printer.Line("  %s", cmdStr)
				return nil
			}

			result, err := selfupdate.Apply(cmd.Context(), selfupdate.ApplyOptions{
				Version:        version,
				BaseURL:        releasesBaseURL,
				ExecutablePath: upgradeExecutablePath,
			})
			if err != nil {
				return err
			}

			restarted := restartRunningDaemonBestEffort(cmd.Context(), app)

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{
					"upgraded":  true,
					"version":   result.Version,
					"restarted": restarted,
				})
			}
			app.Printer.Line("upgraded sapien to %s (%s)", result.Version, result.Executable)
			if restarted {
				app.Printer.Line("restarted the running daemon")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "install this version instead of the latest release")
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only check for a newer release; do not install anything")
	return cmd
}

// runUpgradeCheck implements `sapien upgrade --check`: refresh the cached
// "is there a newer release" answer (bypassing selfupdate.Due's 24h
// throttle -- an explicit check is exactly what that's for) and report it,
// without installing anything.
func runUpgradeCheck(ctx context.Context, app *App, method selfupdate.Method) error {
	cache, err := selfupdate.Refresh(ctx, selfupdate.RefreshOptions{BaseURL: releasesBaseURL})
	if err != nil {
		return err
	}
	available := cache.Latest != "" && selfupdate.IsNewer(Version, cache.Latest)

	if app.Printer.IsJSON() {
		return app.Printer.JSON(map[string]any{
			"current":        Version,
			"latest":         cache.Latest,
			"available":      available,
			"install_method": string(method),
			"command":        selfupdate.Command(method),
			"error":          cache.Error,
		})
	}
	if cache.Error != "" {
		app.Printer.Errorf("could not check for updates: %s", cache.Error)
		return nil
	}
	if !available {
		app.Printer.Line("sapien %s is up to date", Version)
		return nil
	}
	app.Printer.Line("a newer release is available: %s (you have %s)", cache.Latest, Version)
	app.Printer.Line("run: %s", selfupdate.Command(method))
	return nil
}

// restartRunningDaemonBestEffort restarts a daemon already running for
// whatever workspace app.Workspace() resolves to, if any. It is silent
// (and returns false) when no workspace is discoverable -- `sapien
// upgrade` works from anywhere, unlike most commands -- or none is running
// there; a restart failure is reported on stderr but never fails the
// upgrade itself, since the binary has already been replaced either way.
func restartRunningDaemonBestEffort(ctx context.Context, app *App) bool {
	ws, err := app.Workspace()
	if err != nil {
		return false
	}
	info, err := daemon.Read(ws)
	if err != nil || !daemon.Running(info) {
		return false
	}
	if _, _, err := restartDaemon(ctx, ws); err != nil {
		app.Printer.Errorf("upgraded, but restarting the daemon failed: %v (run `sapien daemon restart` yourself)", err)
		return false
	}
	return true
}
