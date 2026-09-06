package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/growsimplee/sapien/internal/workspace"
)

func init() { Register(newInitCmd) }

func newInitCmd(app *App) *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a new Sapien workspace",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			ws, err := workspace.Init(dir, name)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(ws)
			}
			app.Printer.Line("Initialized workspace %q in %s", ws.Name, ws.Dir)
			if _, err := os.Stat(filepath.Join(ws.Dir, ".git")); err != nil {
				app.Printer.Line("tip: run `git init` here so flows, memories, and environments are versioned; workspace-scoped memories are otherwise local to this machine")
			}
			app.Printer.Line("next: `sapien mcp config --client claude-code --write` makes this the default workspace for agents and for sapien commands run elsewhere")
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "workspace name (default: the directory name)")
	return cmd
}
