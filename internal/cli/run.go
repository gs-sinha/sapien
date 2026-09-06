package cli

import (
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/growsimplee/sapien/internal/diagnose"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
)

func init() { Register(newRunCmd) }

// newRunCmd registers the top-level `sapien run` command group, which
// inspects and manages run *history* (PLAN §21) — distinct from `sapien flow
// run`, which executes a flow.
func newRunCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Inspect and manage past runs",
	}
	cmd.AddCommand(newRunListCmd(app), newRunShowCmd(app), newRunResumeCmd(app), newRunPinCmd(app), newRunPurgeCmd(app), newRunSaveExampleCmd(app))
	return cmd
}

func newRunListCmd(app *App) *cobra.Command {
	var flowID, status string
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List past runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			filter := domain.RunFilter{FlowID: flowID, Status: domain.RunStatus(status), Limit: limit}
			runs, err := eng.Runs().List(cmd.Context(), filter)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(runs)
			}
			rows := make([][]string, 0, len(runs))
			for _, r := range runs {
				started := ""
				if !r.Started.IsZero() {
					started = r.Started.UTC().Format(time.RFC3339)
				}
				rows = append(rows, []string{
					r.ID, r.FlowID, r.Environment, string(r.Status),
					strconv.Itoa(r.Summary.StepsTotal), formatMs(float64(r.DurationMs)), started,
				})
			}
			app.Printer.Table([]string{"RUN", "FLOW", "ENV", "STATUS", "STEPS", "DURATION", "STARTED"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&flowID, "flow", "", "filter by flow id")
	cmd.Flags().StringVar(&status, "status", "", "filter by run status: queued|running|passed|failed|errored|cancelled")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum results")
	return cmd
}

func newRunShowCmd(app *App) *cobra.Command {
	var step string
	var bodies bool

	cmd := &cobra.Command{
		Use:   "show <run-id>",
		Short: "Show one run (or, with --step, one of its steps)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			run, err := eng.Runs().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if step != "" {
				st := findStep(run, step)
				if st == nil {
					return errs.New(errs.Invalid, "step %q not found in run %s", step, run.ID).
						WithHint("run `sapien run show` without --step to see every step id")
				}
				if app.Printer.IsJSON() {
					return app.Printer.JSON(st)
				}
				printStepDetail(app.Printer, *st)
				return nil
			}

			hints := diagnose.Run(cmd.Context(), eng, run)
			if app.Printer.IsJSON() {
				return app.Printer.JSON(runWithHints{Run: *run, Hints: hints})
			}
			printRunShowHuman(app.Printer, run, bodies)
			printHints(app.Printer, hints)
			return nil
		},
	}
	cmd.Flags().StringVar(&step, "step", "", "show one step's request/response records")
	cmd.Flags().BoolVar(&bodies, "bodies", false, "include request/response bodies in the step table")
	return cmd
}

func findStep(run *domain.Run, stepID string) *domain.StepResult {
	for i := range run.Steps {
		if run.Steps[i].StepID == stepID {
			return &run.Steps[i]
		}
	}
	return nil
}

func printRunShowHuman(p *Printer, run *domain.Run, bodies bool) {
	p.Line("run %s", run.ID)
	p.Line("flow: %s", run.FlowID)
	p.Line("environment: %s", run.Environment)
	p.Line("status: %s", run.Status)
	if !run.Started.IsZero() {
		p.Line("started: %s", run.Started.UTC().Format(time.RFC3339))
	}
	p.Line("")

	headers := []string{"STEP", "OPERATION", "STATUS", "HTTP", "LATENCY", "ASSERTIONS"}
	if bodies {
		headers = append(headers, "REQUEST BODY", "RESPONSE BODY")
	}
	rows := make([][]string, 0, len(run.Steps))
	for _, st := range run.Steps {
		httpStatus := ""
		if st.Response != nil {
			httpStatus = strconv.Itoa(st.Response.Status)
		}
		row := []string{stepIDLabel(st), st.Operation, stepStatusLabel(st), httpStatus, formatMs(stepDurationMs(st)), assertionSummary(st.Assertions)}
		if bodies {
			reqBody, respBody := "", ""
			if st.Request != nil {
				reqBody = compactJSON(st.Request.Body)
			}
			if st.Response != nil {
				respBody = compactJSON(st.Response.Body)
			}
			row = append(row, reqBody, respBody)
		}
		rows = append(rows, row)
	}
	p.Table(headers, rows)
}

func printStepDetail(p *Printer, st domain.StepResult) {
	p.Line("step %s (%s)  %s", st.StepID, st.Operation, st.Status)

	if st.Request != nil {
		p.Line("")
		p.Line("request: %s %s", st.Request.Method, st.Request.URL)
		for _, k := range sortedKeys(st.Request.Headers) {
			p.Line("  %s: %s", k, st.Request.Headers[k])
		}
		if st.Request.Body != nil {
			if body, err := prettyJSON(st.Request.Body); err == nil {
				p.Line("%s", body)
			}
		}
	}

	if st.Response != nil {
		p.Line("")
		p.Line("response: %d", st.Response.Status)
		for _, k := range sortedKeys(st.Response.Headers) {
			p.Line("  %s: %s", k, st.Response.Headers[k])
		}
		if st.Response.Body != nil {
			if body, err := prettyJSON(st.Response.Body); err == nil {
				p.Line("%s", body)
			}
		}
	}

	if len(st.Assertions) > 0 {
		p.Line("")
		for _, a := range st.Assertions {
			mark := "✓"
			if !a.Passed {
				mark = "✗"
			}
			p.Line("%s %s", mark, a.Expr)
		}
	}

	if st.Error != nil {
		p.Line("")
		p.Line("error: %s", st.Error.Message)
	}
}

func newRunPinCmd(app *App) *cobra.Command {
	var unpin bool

	cmd := &cobra.Command{
		Use:   "pin <id>",
		Short: "Pin a run so purge never removes it (or, with --unpin, unpin it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			pinned := !unpin
			if err := eng.Runs().Pin(cmd.Context(), args[0], pinned); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{"id": args[0], "pinned": pinned})
			}
			if pinned {
				app.Printer.Line("pinned %s", args[0])
			} else {
				app.Printer.Line("unpinned %s", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&unpin, "unpin", false, "unpin instead of pin")
	return cmd
}

func newRunPurgeCmd(app *App) *cobra.Command {
	var keep int

	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete old, unpinned runs, keeping the N most recent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			removed, err := eng.Runs().Purge(cmd.Context(), keep)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]int{"removed": removed})
			}
			app.Printer.Line("removed %d run(s)", removed)
			return nil
		},
	}
	cmd.Flags().IntVar(&keep, "keep", 0, "number of most-recent unpinned runs to keep")
	return cmd
}

func newRunSaveExampleCmd(app *App) *cobra.Command {
	var step, name, scope, description string
	var tags []string

	cmd := &cobra.Command{
		Use:   "save-example <run-id> --name <id>",
		Short: "Save one step of a past run as a verified example",
		Long: `Save one step of a past run as a verified example (PLAN §34b): the
request as it was sent and the response it produced, so the next call
or agent can replay it instead of rediscovering it.

Scope decides storage and sharing: service scope commits the example
under the service's own api/examples, reviewable by anyone who clones
that repo; workspace scope keeps it local under <workspace>/examples.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errs.New(errs.Invalid, "run save-example requires --name").
					WithHint("pass --name <id>")
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			created, err := eng.Examples().FromRun(cmd.Context(), engine.ExampleFromRun{
				RunID:       args[0],
				StepID:      step,
				ID:          name,
				Description: description,
				Scope:       domain.ExampleScope(scope),
				Tags:        tags,
				Source:      &domain.MemorySource{Kind: "user"},
			})
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(created)
			}
			app.Printer.Line("%s", exampleSavedLine(created))
			return nil
		},
	}
	cmd.Flags().StringVar(&step, "step", "", "step id to save (default: the run's only, or first, step)")
	cmd.Flags().StringVar(&name, "name", "", "example id to save as (required)")
	cmd.Flags().StringVar(&scope, "scope", "", "workspace|service (default workspace)")
	cmd.Flags().StringVar(&description, "description", "", "example description")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	return cmd
}
