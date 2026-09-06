package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

func init() { Register(newWorkspaceCmd) }

// workspaceRow is one row of `sapien workspace list`, and its --json shape.
type workspaceRow struct {
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Current  bool   `json:"current"`
	Default  bool   `json:"default"`
	Services int    `json:"services,omitempty"`
	Error    string `json:"error,omitempty"`
}

// newWorkspaceCmd is `sapien workspace`: the registry of workspaces this
// machine knows about, and which one commands use by default.
//
// Selecting a workspace for a single command has always worked
// (--workspace, $SAPIEN_WORKSPACE, or just running inside one); these verbs
// exist because neither the UI's picker nor MCP's switch_workspace can offer
// a choice the machine has never written down.
func newWorkspaceCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "List, switch between, and register workspaces",
	}
	cmd.AddCommand(
		newWorkspaceListCmd(app),
		newWorkspaceCurrentCmd(app),
		newWorkspaceUseCmd(app),
		newWorkspaceAddCmd(app),
		newWorkspaceForgetCmd(app),
	)
	return cmd
}

func newWorkspaceListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every registered workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs, err := config.KnownWorkspaces()
			if err != nil {
				return err
			}
			def, err := config.DefaultWorkspace()
			if err != nil {
				return err
			}
			// The workspace this invocation would actually use, which is
			// not necessarily the default one: running inside a workspace
			// wins over it.
			var currentDir string
			if ws, err := app.Workspace(); err == nil {
				currentDir = ws.Dir
			}
			// The workspace you are standing in belongs in the list whether
			// or not it was ever registered -- otherwise `workspace list`
			// run inside a workspace fails to mention it, which reads as a
			// bug rather than as "you never added this one".
			// Matched by directory, not by path string: run from a cwd that
			// spells the path differently (lowercase `desktop` on macOS, a
			// symlinked parent) and a string compare adds the workspace you
			// are already standing in a second time.
			if currentDir != "" {
				known := false
				for _, dir := range dirs {
					if workspace.SameDir(dir, currentDir) {
						known = true
						break
					}
				}
				if !known {
					dirs = append([]string{currentDir}, dirs...)
				}
			}

			rows := make([]workspaceRow, 0, len(dirs))
			for _, dir := range dirs {
				row := workspaceRow{Dir: dir, Current: workspace.SameDir(dir, currentDir), Default: workspace.SameDir(dir, def)}
				ws, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
				if err != nil {
					row.Error = err.Error()
					row.Name = filepath.Base(dir)
				} else {
					row.Name = ws.Name
					row.Services = len(ws.Services)
				}
				rows = append(rows, row)
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(rows)
			}
			if len(rows) == 0 {
				app.Printer.Line("No workspaces registered. Run `sapien init` in a new directory, or `sapien workspace add <dir>`.")
				return nil
			}
			for _, row := range rows {
				marker := " "
				if row.Current {
					marker = "*"
				}
				suffix := ""
				switch {
				case row.Error != "":
					suffix = "  (" + row.Error + ")"
				case row.Default:
					suffix = "  (default)"
				}
				app.Printer.Line("%s %-24s %s%s", marker, row.Name, row.Dir, suffix)
			}
			return nil
		},
	}
}

func newWorkspaceCurrentCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the workspace this directory resolves to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(workspaceRow{Name: ws.Name, Dir: ws.Dir, Current: true, Services: len(ws.Services)})
			}
			app.Printer.Line("%s  %s", ws.Name, ws.Dir)
			return nil
		},
	}
}

func newWorkspaceUseCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "use <dir|name>",
		Short: "Make a workspace the default for commands outside any workspace",
		Long: "Sets default_workspace in the user config. It is the fallback, not an override: " +
			"a command run inside a workspace, or given --workspace/$SAPIEN_WORKSPACE, still uses that one.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceArg(args[0])
			if err != nil {
				return err
			}
			if err := config.AddWorkspace(ws.Dir); err != nil {
				return err
			}
			if err := config.SetDefaultWorkspace(ws.Dir); err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(workspaceRow{Name: ws.Name, Dir: ws.Dir, Default: true, Services: len(ws.Services)})
			}
			app.Printer.Line("default workspace is now %s (%s)", ws.Name, ws.Dir)
			return nil
		},
	}
}

func newWorkspaceAddCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "add <dir>",
		Short: "Register an existing workspace so it appears in pickers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceArg(args[0])
			if err != nil {
				return err
			}
			if err := config.AddWorkspace(ws.Dir); err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(workspaceRow{Name: ws.Name, Dir: ws.Dir, Services: len(ws.Services)})
			}
			app.Printer.Line("registered %s (%s)", ws.Name, ws.Dir)
			return nil
		},
	}
}

func newWorkspaceForgetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "forget <dir>",
		Short: "Unregister a workspace (the directory itself is untouched)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return errs.Wrap(errs.Internal, err, "resolving %q", args[0])
			}
			if err := config.RemoveWorkspace(abs); err != nil {
				return err
			}
			app.Printer.Line("forgot %s (the directory is untouched)", abs)
			return nil
		},
	}
}

// resolveWorkspaceArg turns a CLI argument into a loaded workspace. A path
// is used directly; anything else is matched against the registered
// workspaces by name, so `sapien workspace use platform` works without
// typing the path the picker already knows.
func resolveWorkspaceArg(arg string) (*domain.Workspace, error) {
	abs, err := filepath.Abs(arg)
	if err == nil {
		if ws, loadErr := workspace.Load(filepath.Join(abs, domain.WorkspaceFileName)); loadErr == nil {
			return ws, nil
		}
	}

	dirs, err := config.KnownWorkspaces()
	if err != nil {
		return nil, err
	}
	var matches []*domain.Workspace
	for _, dir := range dirs {
		ws, loadErr := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
		if loadErr == nil && ws.Name == arg {
			matches = append(matches, ws)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, errs.New(errs.WorkspaceNotFound, "no workspace at %q, and none registered under that name", arg).
			WithHint("`sapien workspace list` shows the registered ones")
	default:
		dirsList := make([]string, 0, len(matches))
		for _, m := range matches {
			dirsList = append(dirsList, m.Dir)
		}
		return nil, errs.New(errs.Invalid, "%d registered workspaces are named %q", len(matches), arg).
			WithHint(fmt.Sprintf("name one by path: %v", dirsList))
	}
}
