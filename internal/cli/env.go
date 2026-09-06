package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

func init() { Register(newEnvCmd) }

func newEnvCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage workspace environments",
	}
	cmd.AddCommand(
		newEnvListCmd(app),
		newEnvShowCmd(app),
		newEnvUseCmd(app),
		newEnvScaffoldCmd(app),
		newEnvProbeCmd(app),
	)
	return cmd
}

func newEnvListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List environments",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}
			envs, err := workspace.ListEnvironments(ws)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(envs)
			}

			def := workspace.DefaultEnvironment(ws)
			rows := make([][]string, 0, len(envs))
			for _, e := range envs {
				defMarker := ""
				if e.Name == def {
					defMarker = "*"
				}
				rows = append(rows, []string{e.Name, fmt.Sprintf("%v", e.Production), defMarker})
			}
			app.Printer.Table([]string{"NAME", "PRODUCTION", "DEFAULT"}, rows)
			return nil
		},
	}
}

func newEnvShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show one environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}
			env, err := workspace.LoadEnvironment(ws, args[0])
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(env)
			}
			app.Printer.Line("name: %s", env.Name)
			app.Printer.Line("production: %v", env.Production)
			app.Printer.Line("path: %s", env.Path)
			return nil
		},
	}
}

func newEnvUseCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Set the workspace's default environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}
			if _, err := workspace.LoadEnvironment(ws, args[0]); err != nil {
				return err
			}

			ws.DefaultEnvironment = args[0]
			if err := workspace.Save(ws); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"default_environment": ws.DefaultEnvironment})
			}
			app.Printer.Line("default environment set to %s", ws.DefaultEnvironment)
			return nil
		},
	}
}

// newEnvScaffoldCmd creates or updates environments/<name>.yaml for every
// environment name any registered service declares in its service.yaml
// hints (PLAN §20 feedback: those hints alone don't make a workspace
// runnable against that environment; only a real environment file does,
// and previously nothing created one).
func newEnvScaffoldCmd(app *App) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "scaffold",
		Short: "Create or update environment files from registered services' service.yaml hints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			svcs, err := eng.Services().List(cmd.Context())
			if err != nil {
				return err
			}

			report, err := env.Scaffold(ws, svcs, force)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(report)
			}

			if !report.Changed() {
				app.Printer.Line("nothing to scaffold: every environment declared by a registered service already has a file with a base_url on record")
				return nil
			}
			for _, f := range report.Files {
				if f.Created {
					app.Printer.Line("created %s", f.Path)
				} else {
					app.Printer.Line("updated %s", f.Path)
				}
				for _, e := range f.Entries {
					if e.Overwritten {
						app.Printer.Line("overwrote %s.base_url in %s (%s)", e.Service, f.Path, e.BaseURL)
					} else {
						app.Printer.Line("added %s.base_url to %s (%s)", e.Service, f.Path, e.BaseURL)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing base_url that differs from the service.yaml hint")
	return cmd
}

// newEnvProbeCmd sends a GET request to every service base_url declared in
// one environment file and reports whether each answered (PLAN §20
// feedback: "env stage was unrunnable... nothing said so; I found it by
// failing" -- probe lets a developer check reachability before running
// anything for real). It operates on the environment file directly (like
// `env list`/`show`/`use`), not the service catalog, so it needs only the
// workspace, not the engine.
func newEnvProbeCmd(app *App) *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "probe [name]",
		Short: "GET every service base_url in an environment and report whether it answered",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}

			ws, err := app.Workspace()
			if err != nil {
				return err
			}
			loaded, err := env.Load(ws, name)
			if err != nil {
				return err
			}

			// The --timeout flag wins when the caller actually passed it;
			// otherwise an environment that sets transport.timeout_ms is
			// honored instead of the flag's own default.
			reqTimeout := timeout
			if !cmd.Flags().Changed("timeout") && loaded.Transport != nil && loaded.Transport.TimeoutMs > 0 {
				reqTimeout = time.Duration(loaded.Transport.TimeoutMs) * time.Millisecond
			}

			client := env.NewProbeClient(loaded, reqTimeout)
			results := env.Probe(cmd.Context(), loaded, client)

			failed := 0
			for _, r := range results {
				if r.Error != "" {
					failed++
				}
			}

			if app.Printer.IsJSON() {
				if jerr := app.Printer.JSON(results); jerr != nil {
					return jerr
				}
			} else {
				rows := make([][]string, 0, len(results))
				for _, r := range results {
					result := r.Error
					if result == "" {
						result = strconv.Itoa(r.Status)
					}
					rows = append(rows, []string{r.Service, r.BaseURL, result, fmt.Sprintf("%dms", r.LatencyMs)})
				}
				app.Printer.Table([]string{"SERVICE", "BASE_URL", "RESULT", "LATENCY"}, rows)
			}

			if failed > 0 {
				return errs.New(errs.AssertionFailed, "%d of %d service probes in environment %q failed to connect", failed, len(results), loaded.Name)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "per-service probe timeout (an environment's transport.timeout_ms is honored instead, unless --timeout is passed explicitly)")
	return cmd
}
