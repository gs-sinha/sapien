package cli

import (
	"github.com/spf13/cobra"

	"github.com/growsimplee/sapien/internal/diagnose"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
)

// newRunResumeCmd is `sapien run resume <run-id> [--from <step>] [--until
// <step>] [-i k=v]...`: run the same flow again, reusing that run's setup
// and pre-failure step results instead of executing them (PLAN §34d). The
// environment and inputs default to the earlier run's.
func newRunResumeCmd(app *App) *cobra.Command {
	var fromStep, untilStep string
	var inputs []string
	var allowProduction bool

	cmd := &cobra.Command{
		Use:   "resume <run-id>",
		Short: "Run a flow again from where an earlier run stopped, reusing its results",
		Long: `Run the flow of an earlier run again, copying that run's setup steps and
every main step before the resume point (request, response, extracted
values) instead of executing them, so later steps see exactly what they saw
before. The resume point defaults to the first step that did not pass; --from
overrides it and --until stops early. Teardown always runs. Fix the flow
first (sapien flow patch, or edit the file) when the failure was in the flow.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			earlier, err := eng.Runs().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			inputMap, err := parseParams(inputs)
			if err != nil {
				return err
			}
			envName := app.EnvName
			if envName == "" {
				envName = earlier.Environment
			}
			opts := engine.RunOptions{
				Environment:     envName,
				Inputs:          inputMap,
				ResumeFrom:      earlier.ID,
				FromStep:        fromStep,
				UntilStep:       untilStep,
				AllowProduction: allowProduction,
				Trigger:         "cli",
			}

			var run *domain.Run
			switch {
			case earlier.FlowID != "":
				run, err = eng.Runner().RunFlow(cmd.Context(), earlier.FlowID, opts)
			case earlier.FlowSnapshot != "":
				run, err = eng.Runner().RunFlowSource(cmd.Context(), earlier.FlowSnapshot, opts)
			default:
				return errs.New(errs.Invalid, "run %s has no flow to resume", earlier.ID).
					WithHint("only flow runs can be resumed; a single call is rerun with `sapien call`")
			}
			if err != nil {
				return err
			}

			hints := diagnose.Run(cmd.Context(), eng, run)
			if app.Printer.IsJSON() {
				return app.Printer.JSON(runWithHints{Run: *run, Hints: hints})
			}
			printFlowRunHuman(app.Printer, run)
			printSoftLines(app.Printer, run, diagnose.SoftChanges(cmd.Context(), eng, run))
			printHints(app.Printer, hints)
			return runExitError(run)
		},
	}
	cmd.Flags().StringVar(&fromStep, "from", "", "first main step to execute (inclusive); default: the first step that did not pass in the earlier run")
	cmd.Flags().StringVar(&untilStep, "until", "", "last main step to execute (inclusive); teardown still runs")
	cmd.Flags().StringArrayVarP(&inputs, "input", "i", nil, "flow input key=value overriding the earlier run's inputs (repeatable)")
	cmd.Flags().BoolVar(&allowProduction, "allow-production", false, "allow running against a production environment")
	return cmd
}
