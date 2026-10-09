package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/device"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

func init() { Register(newDeviceCmd) }

func newDeviceCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "Set up and inspect the local Android device that flow ui steps drive",
		Long: "ui steps (`sapien flow reference ui`) drive a mobile app on a local Android emulator\n" +
			"through Appium. These commands check the machine, show what is on screen, and build and\n" +
			"install an app. Per-machine settings live in .sapien/ui.yaml.",
	}
	cmd.AddCommand(newDeviceDoctorCmd(app), newDeviceSnapshotCmd(app), newDeviceBuildCmd(app))
	return cmd
}

// deviceManager builds a device.Manager for the workspace and the selected
// environment's apps:.
func deviceManager(app *App, cmd *cobra.Command, attach []string, rebuild bool) (*device.Manager, error) {
	ws, err := app.Workspace()
	if err != nil {
		return nil, err
	}
	stateDir := filepath.Join(ws.Dir, domain.WorkspaceStateDir)
	local, err := device.LoadLocalConfig(stateDir)
	if err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "reading .sapien/%s", device.LocalConfigFile)
	}
	envName, err := app.EnvironmentName()
	if err != nil {
		return nil, err
	}
	var apps map[string]domain.AppConfig
	if envName != "" {
		e, err := workspace.LoadEnvironment(ws, envName)
		if err != nil {
			return nil, err
		}
		apps = e.Apps
	}
	return device.NewManager(device.Options{
		WorkspaceDir: ws.Dir,
		StateDir:     stateDir,
		EnvName:      envName,
		Apps:         apps,
		Local:        local,
		Attach:       attach,
		Rebuild:      rebuild,
		Progress:     func(msg string) { fmt.Fprintln(cmd.ErrOrStderr(), app.Printer.Dim("device: "+msg)) },
	}), nil
}

func newDeviceDoctorCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check this machine can run ui steps (Node, Appium, Android SDK, JDK, emulator)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			local := device.LocalConfig{}
			if ws, err := app.Workspace(); err == nil {
				if l, lerr := device.LoadLocalConfig(filepath.Join(ws.Dir, domain.WorkspaceStateDir)); lerr == nil {
					local = l
				}
			}
			checks := device.Doctor(cmd.Context(), local, nil)
			failed := 0
			for _, c := range checks {
				if !c.OK && !c.Optional {
					failed++
				}
			}
			if app.Printer.IsJSON() {
				if err := app.Printer.JSON(map[string]any{"ok": failed == 0, "checks": checks}); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					mark := app.Printer.Green("ok  ")
					if !c.OK {
						mark = app.Printer.Red("FAIL")
					}
					app.Printer.Line("%s %-20s %s", mark, c.Name, c.Detail)
					if !c.OK && c.Fix != "" {
						app.Printer.Line("     %-20s %s", "", app.Printer.Dim("fix: "+c.Fix))
					}
				}
				if failed == 0 {
					app.Printer.Line("\nready for ui steps")
				}
			}
			if failed > 0 {
				return errs.New(errs.Invalid, "%d ui check(s) failed", failed)
			}
			return nil
		},
	}
}

func newDeviceSnapshotCmd(app *App) *cobra.Command {
	var shot string
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "List the selectors on the device's current screen",
		Long: "Lists every element on the current screen that a ui step selector can match (id,\n" +
			"text/label, hint) with the selector to write for it. Nothing is built, installed, or\n" +
			"launched: open the screen first (by hand, or with a flow), then snapshot it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := deviceManager(app, cmd, nil, false)
			if err != nil {
				return err
			}
			defer m.Close(cmd.Context())
			els, png, err := m.Snapshot(cmd.Context())
			if err != nil {
				return err
			}
			if shot != "" && len(png) > 0 {
				if err := os.WriteFile(shot, png, 0o644); err != nil {
					return errs.Wrap(errs.Internal, err, "writing %s", shot)
				}
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(els)
			}
			rows := make([][]string, 0, len(els))
			for _, e := range els {
				label := e.Text
				if label == "" {
					label = e.Desc
				}
				tap := ""
				if e.Clickable {
					tap = "yes"
				}
				rows = append(rows, []string{e.Selector(), truncate(label, 40), e.Hint, shortClassName(e.Class), tap})
			}
			app.Printer.Table([]string{"SELECTOR", "TEXT/LABEL", "HINT", "CLASS", "TAPPABLE"}, rows)
			if shot != "" {
				app.Printer.Line("\nscreenshot: %s", shot)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&shot, "screenshot", "", "also save the screen as a PNG here")
	return cmd
}

func newDeviceBuildCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "build <app>",
		Short: "Build an app with its build: command and install it on the device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := deviceManager(app, cmd, nil, true)
			if err != nil {
				return err
			}
			defer m.Close(cmd.Context())
			if _, err := m.App(cmd.Context(), args[0]); err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"built": args[0]})
			}
			app.Printer.Line("built and installed %s", args[0])
			return nil
		},
	}
}

func shortClassName(c string) string {
	if i := strings.LastIndexByte(c, '.'); i >= 0 {
		return c[i+1:]
	}
	return c
}
