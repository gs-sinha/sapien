package cli

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

func init() { Register(newUICmd) }

// newUICmd is `sapien ui [--no-open] [--json]` (PLAN §34c): like `sapien
// mcp`, the UI is a host of the workspace's daemon, so finding or
// starting one is the right thing to do. It prints the daemon's
// /ui/session URL, which exchanges the daemon's bearer token for an
// HttpOnly session cookie and redirects to /ui/ (internal/server's
// handleUISession), and opens it in the platform's default browser
// unless --no-open is given.
//
// --install-app writes a macOS launcher that runs this same command, so
// the UI can be opened from Spotlight, the Dock or a Raycast hotkey
// rather than only from a terminal (ui_installapp.go).
func newUICmd(app *App) *cobra.Command {
	var noOpen bool
	var installApp bool

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the live inspector UI for this workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}

			// --install-app only writes the launcher; it deliberately
			// neither starts a daemon nor opens anything, so installing
			// from a script leaves no process behind.
			if installApp {
				bundle, err := installAppBundle(ws.Dir)
				if err != nil {
					return err
				}
				if app.Printer.IsJSON() {
					return app.Printer.JSON(map[string]any{"bundle": bundle, "workspace": ws.Dir})
				}
				app.Printer.Line("installed %s", bundle)
				app.Printer.Line("  workspace: %s", ws.Dir)
				app.Printer.Line("  launch it from Spotlight, the Dock, or Raycast; re-run this command to repoint it")
				return nil
			}

			info, err := findOrStartDaemon(cmd.Context(), ws, Version)
			if err != nil {
				return err
			}

			url := fmt.Sprintf("http://127.0.0.1:%d/ui/session?token=%s", info.Port, info.Token)

			if app.Printer.IsJSON() {
				if err := app.Printer.JSON(map[string]any{"url": url, "port": info.Port}); err != nil {
					return err
				}
			} else {
				app.Printer.Line("%s", url)
			}

			if noOpen {
				return nil
			}
			if err := openURL(url); err != nil {
				app.Printer.Errorf("could not open a browser automatically (%v); open the URL above yourself", err)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print the URL instead of opening it in a browser")
	cmd.Flags().BoolVar(&installApp, "install-app", false, "install ~/Applications/Sapien.app, a launcher for this workspace's UI (macOS), and exit")
	return cmd
}

// openerBinary names the platform's default URL-handler binary: `open`
// on darwin, `xdg-open` on linux, rundll32 (via its URL protocol
// handler) on windows. A GOOS with no entry has no known opener.
var openerBinary = map[string]string{
	"darwin":  "open",
	"linux":   "xdg-open",
	"windows": "rundll32",
}

// openURL launches the platform's default handler for url, resolved via
// exec.LookPath first; when it isn't on PATH (or the platform has no
// entry in openerBinary), openURL returns an error and the caller falls
// back to having already printed the URL.
func openURL(url string) error {
	name, ok := openerBinary[runtime.GOOS]
	if !ok {
		return fmt.Errorf("no known URL opener for platform %q", runtime.GOOS)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	args := []string{url}
	if runtime.GOOS == "windows" {
		// rundll32 takes the handler DLL and entry point as one argument,
		// with the target URL as a second.
		args = []string{"url.dll,FileProtocolHandler", url}
	}
	return exec.Command(path, args...).Start()
}
