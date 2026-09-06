package cli

import "github.com/spf13/cobra"

// Version, Commit, and Date are overridden at build time via -ldflags (see
// the Makefile's build target).
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func init() { Register(newVersionCmd) }

func newVersionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the sapien version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{
					"version": Version,
					"commit":  Commit,
					"date":    Date,
				})
			}
			app.Printer.Line("sapien %s (commit %s, built %s)", Version, Commit, Date)
			return nil
		},
	}
}
