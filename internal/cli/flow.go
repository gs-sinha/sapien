package cli

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/diagnose"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

func init() { Register(newFlowCmd) }

func newFlowCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flow",
		Short: "Manage and run flows",
	}
	cmd.AddCommand(
		newFlowListCmd(app),
		newFlowShowCmd(app),
		newFlowValidateCmd(app),
		newFlowCreateCmd(app),
		newFlowUpdateCmd(app),
		newFlowPatchCmd(app),
		newFlowDeleteCmd(app),
		newFlowReferenceCmd(app),
		newFlowRunCmd(app),
	)
	return cmd
}

func newFlowListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list [query]",
		Short: "List flows",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			query := ""
			if len(args) > 0 {
				query = args[0]
			}
			flows, err := eng.Flows().List(cmd.Context(), query)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(flows)
			}
			rows := make([][]string, 0, len(flows))
			for _, f := range flows {
				rows = append(rows, []string{f.ID, f.Name, ownerString(f), strconv.Itoa(f.StepCount), strings.Join(f.Operations, ",")})
			}
			app.Printer.Table([]string{"ID", "NAME", "OWNER", "STEPS", "OPERATIONS"}, rows)
			return nil
		},
	}
}

// ownerString renders a flow's owner column: the specific owner id when the
// flow belongs to a service, else its owner kind ("workspace").
func ownerString(f domain.FlowSummary) string {
	if f.OwnerID != "" {
		return f.OwnerID
	}
	return f.OwnerKind
}

func newFlowShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a flow's YAML source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			flow, err := eng.Flows().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(flow)
			}
			app.Printer.Line("%s", flow.Source)
			return nil
		},
	}
}

func newFlowValidateCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "validate <id|path>",
		Short: "Validate a flow's references, bindings, and expressions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			src, err := resolveFlowSource(cmd.Context(), eng, args[0])
			if err != nil {
				return err
			}

			result, err := eng.Flows().Validate(cmd.Context(), src)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				if jerr := app.Printer.JSON(result); jerr != nil {
					return jerr
				}
			} else {
				printDiagnostics(app.Printer, result)
			}

			if !result.Valid {
				return errs.New(errs.FlowInvalid, "flow is invalid: %d diagnostic(s)", len(result.Diagnostics))
			}
			return nil
		},
	}
}

// resolveFlowSource implements the id-or-path argument shared by `flow
// validate`: a path (the arg ends in .yaml/.yml, or exists as a file) is
// read directly; anything else is looked up as a saved flow id.
func resolveFlowSource(ctx context.Context, eng engine.Engine, ref string) (string, error) {
	if looksLikeFlowPath(ref) {
		data, err := os.ReadFile(ref)
		if err != nil {
			return "", errs.Wrap(errs.Invalid, err, "reading %s", ref)
		}
		return string(data), nil
	}
	flow, err := eng.Flows().Get(ctx, ref)
	if err != nil {
		return "", err
	}
	return flow.Source, nil
}

// looksLikeFlowPath reports whether ref names a file on disk rather than a
// saved flow id (PLAN §21): it ends in .yaml/.yml, or a file exists there.
func looksLikeFlowPath(ref string) bool {
	if strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml") {
		return true
	}
	info, err := os.Stat(ref)
	return err == nil && !info.IsDir()
}

func printDiagnostics(p *Printer, result *domain.ValidationResult) {
	rows := make([][]string, 0, len(result.Diagnostics))
	for _, d := range result.Diagnostics {
		rows = append(rows, []string{string(d.Severity), strconv.Itoa(d.Line), d.Code, d.Message, strings.Join(d.Suggestions, "; ")})
	}
	p.Table([]string{"SEVERITY", "LINE", "CODE", "MESSAGE", "SUGGESTIONS"}, rows)
	if result.Valid {
		p.Line("valid")
	}
}

func newFlowCreateCmd(app *App) *cobra.Command {
	var destPath string
	cmd := &cobra.Command{
		Use:   "create <file>",
		Short: "Save a flow file into the workspace",
		Long: `Validate the flow YAML in <file> and save it under the workspace's flows/
directory as <id>.flow.yaml (or --path, relative to flows/). Prints a summary
of what was saved, never the document.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			path := args[0]
			data, err := os.ReadFile(path)
			if err != nil {
				return errs.Wrap(errs.Invalid, err, "reading %s", path)
			}

			flow, err := eng.Flows().Create(cmd.Context(), string(data), destPath)
			if err != nil {
				return err
			}
			return printFlowSaved(app, "saved", flow)
		},
	}
	cmd.Flags().StringVar(&destPath, "path", "", "destination inside the workspace flows directory (default: <id>.flow.yaml)")
	return cmd
}

func newFlowDeleteCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a saved flow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.Flows().Delete(cmd.Context(), args[0]); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"deleted": args[0]})
			}
			app.Printer.Line("deleted %s", args[0])
			return nil
		},
	}
}

func newFlowReferenceCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "reference [sapien|flow|memory|expressions|service]",
		Short: "Print the DSL reference text for agents",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := "flow"
			if len(args) > 0 {
				topic = args[0]
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			text, err := eng.Flows().Reference(cmd.Context(), topic)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"topic": topic, "text": text})
			}
			app.Printer.Line("%s", text)
			return nil
		},
	}
}

func newFlowRunCmd(app *App) *cobra.Command {
	var inputs []string
	var resumeFrom, fromStep, untilStep string
	var continueOnFailure, allowProduction, watch bool
	var report, out string

	cmd := &cobra.Command{
		Use:   "run <id|path>",
		Short: "Run a flow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			envName, err := app.EnvironmentName()
			if err != nil {
				return err
			}
			if err := checkProduction(cmd.Context(), eng, envName, allowProduction); err != nil {
				return err
			}

			inputMap, err := parseParams(inputs)
			if err != nil {
				return err
			}

			opts := engine.RunOptions{
				Environment:       envName,
				Inputs:            inputMap,
				ContinueOnFailure: continueOnFailure,
				ResumeFrom:        resumeFrom,
				FromStep:          fromStep,
				UntilStep:         untilStep,
				AllowProduction:   allowProduction,
				Trigger:           "cli",
			}
			// --watch prints step transitions live via the Observer callback
			// (engine.RunOptions), the seam the Engine interface documents for
			// observing a run in progress; it is skipped under --json since
			// that mode must emit exactly one JSON document.
			if watch && !app.Printer.IsJSON() {
				opts.Observer = func(ev domain.Event) { printWatchEvent(app.Printer, ev) }
			}

			ref := args[0]
			var run *domain.Run
			if looksLikeFlowPath(ref) {
				data, rerr := os.ReadFile(ref)
				if rerr != nil {
					return errs.Wrap(errs.Invalid, rerr, "reading %s", ref)
				}
				run, err = eng.Runner().RunFlowSource(cmd.Context(), string(data), opts)
			} else {
				run, err = eng.Runner().RunFlow(cmd.Context(), ref, opts)
			}
			if err != nil {
				return err
			}

			if report != "" {
				return writeReport(app, report, out, run)
			}

			hints := diagnose.Run(cmd.Context(), eng, run)
			if app.Printer.IsJSON() {
				if jerr := app.Printer.JSON(runWithHints{Run: *run, Hints: hints}); jerr != nil {
					return jerr
				}
			} else {
				printFlowRunHuman(app.Printer, run)
				printSoftLines(app.Printer, run, diagnose.SoftChanges(cmd.Context(), eng, run))
				printHints(app.Printer, hints)
			}
			return runExitError(run)
		},
	}

	cmd.Flags().StringArrayVarP(&inputs, "input", "i", nil, "flow input key=value; JSON-decoded when it parses (repeatable)")
	cmd.Flags().BoolVar(&continueOnFailure, "continue-on-failure", false, "continue past a failed or errored step instead of stopping")
	cmd.Flags().StringVar(&resumeFrom, "resume", "", "run id of an earlier run of this flow whose setup and pre-failure step results are reused instead of executed")
	cmd.Flags().StringVar(&fromStep, "from", "", "first main step to execute (inclusive); with --resume, overrides its default resume point")
	cmd.Flags().StringVar(&untilStep, "until", "", "last main step to execute (inclusive); teardown still runs")
	cmd.Flags().BoolVar(&allowProduction, "allow-production", false, "allow running against a production environment")
	cmd.Flags().StringVar(&report, "report", "", "write a report instead of the default output: junit|json")
	cmd.Flags().StringVar(&out, "out", "", "report output file (default: stdout)")
	cmd.Flags().BoolVar(&watch, "watch", false, "print step transitions live as the run executes")
	return cmd
}

// writeReport implements `flow run --report junit|json [--out file]`.
func writeReport(app *App, kind, out string, run *domain.Run) error {
	w := app.Stdout
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			return errs.Wrap(errs.Internal, err, "creating %s", out)
		}
		defer f.Close()
		w = f
	}

	switch kind {
	case "junit":
		if err := writeJUnit(w, run); err != nil {
			return err
		}
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(run); err != nil {
			return err
		}
	default:
		return errs.New(errs.Invalid, "unknown --report %q (want junit or json)", kind)
	}

	if out != "" {
		app.Printer.Line("wrote %s", out)
	}
	return runExitError(run)
}
