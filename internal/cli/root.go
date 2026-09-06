// Package cli implements Sapien's command line interface (PLAN §21) on top
// of cobra. It is the thin adapter over internal/engine that every command
// talks through; no business logic lives here.
//
// # Adding a command
//
// Each command lives in its own file in this package and registers itself
// with an init() that calls Register, so new commands never require editing
// this file (or any other shared file). Register takes a CommandFactory: a
// function of the shared *App that returns a fresh *cobra.Command. A fresh
// tree is built by newRootCmd on every call to Execute, so factories must
// not memoize anything across calls.
//
//	func init() { Register(newFooCmd) }
//
//	func newFooCmd(app *App) *cobra.Command {
//		return &cobra.Command{
//			Use:   "foo",
//			Short: "...",
//			RunE: func(cmd *cobra.Command, args []string) error {
//				ws, err := app.Workspace() // discovers, or uses --workspace
//				if err != nil {
//					return err // Execute formats it (human or --json) and sets the exit code
//				}
//				if app.Printer.IsJSON() {
//					return app.Printer.JSON(result)
//				}
//				app.Printer.Line("...")
//				return nil
//			},
//		}
//	}
//
// A command with subcommands (see env.go) builds them the same way and
// wires them with AddCommand inside its own factory.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// CommandFactory builds one subcommand of the root command, given the App
// for this invocation. See the package doc comment for the registration
// pattern.
type CommandFactory func(app *App) *cobra.Command

var registry []CommandFactory

// Register adds a command factory to the root command. Call it from an
// init() in the file that defines the command.
func Register(f CommandFactory) {
	registry = append(registry, f)
}

// newRootCmd builds a fresh root command tree bound to app. Global
// persistent flags are declared here and nowhere else.
func newRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "sapien",
		Short:         "Sapien — local-first API workspace engine",
		SilenceErrors: true, // Execute formats and prints errors itself
		SilenceUsage:  true,
	}

	root.PersistentFlags().BoolVar(&app.JSON, "json", false, "print machine-readable JSON output")
	root.PersistentFlags().StringVar(&app.WorkspaceDir, "workspace", "", "workspace directory (default: discovered by walking up from the current directory)")
	root.PersistentFlags().BoolVarP(&app.Verbose, "verbose", "v", false, "verbose logging")
	root.PersistentFlags().StringVar(&app.EnvName, "env", "", "environment name (default: the workspace's default environment)")
	root.PersistentFlags().BoolVar(&app.NoColor, "no-color", false, "disable colored output")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		app.Printer = NewPrinter(app.Stdout, app.Stderr, app.JSON, ColorEnabled(app.Stdout, app.NoColor))
		return nil
	}

	for _, factory := range registry {
		root.AddCommand(factory(app))
	}

	return root
}

// Execute runs the CLI for args (as in os.Args[1:]) and returns the process
// exit code (errs.ExitCode: 0 ok, 1 assertion failure, 2 error, 3 blocked).
func Execute(args []string, stdout, stderr io.Writer) int {
	app := &App{Stdout: stdout, Stderr: stderr}
	// A Printer exists even if PersistentPreRunE never runs (e.g. a flag
	// parse error aborts before it), so error formatting always has one.
	app.Printer = NewPrinter(stdout, stderr, false, ColorEnabled(stdout, false))

	root := newRootCmd(app)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.Execute(); err != nil {
		writeError(app, err)
		return errs.ExitCode(err)
	}
	return 0
}

// writeError formats a command's returned error to stderr: the structured
// errs.Error JSON object under --json, a plain message (plus hint) otherwise.
func writeError(app *App, err error) {
	e := errs.As(err)
	if app.JSON {
		enc := json.NewEncoder(app.Stderr)
		enc.SetIndent("", "  ")
		_ = enc.Encode(e)
		return
	}
	fmt.Fprintf(app.Stderr, "error: %s\n", e.Error())
	for _, line := range errorDiagnosticLines(e) {
		fmt.Fprintf(app.Stderr, "  %s\n", line)
	}
	if e.Hint != "" {
		fmt.Fprintf(app.Stderr, "hint: %s\n", e.Hint)
	}
}

// errorDiagnosticLines renders the "diagnostics" detail an E_FLOW_INVALID
// (or example validation) error carries, one line each, so the human-mode
// message says what is wrong rather than only how many problems there are.
// The detail is []domain.Diagnostic in-process and []any of maps after a
// daemon round trip; both are normalised through JSON.
func errorDiagnosticLines(e *errs.Error) []string {
	raw, ok := e.Details["diagnostics"]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var diags []domain.Diagnostic
	if err := json.Unmarshal(data, &diags); err != nil {
		return nil
	}
	lines := make([]string, 0, len(diags))
	for _, d := range diags {
		line := string(d.Severity)
		if d.Line > 0 {
			line += fmt.Sprintf(" line %d", d.Line)
		}
		if d.StepID != "" {
			line += " step " + d.StepID
		}
		line += ": " + d.Code + ": " + d.Message
		if len(d.Suggestions) > 0 {
			line += " (did you mean: " + strings.Join(d.Suggestions, ", ") + ")"
		}
		lines = append(lines, line)
	}
	return lines
}
