package cli

import (
	"context"
	"encoding/json"
	"fmt"
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
		newFlowPromoteCmd(app),
		newFlowRescopeCmd(app),
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
				rows = append(rows, []string{f.ID, f.Name, flowTier(f.OwnerKind, f.OwnerID), shipStateColumn(f.Shipped), strconv.Itoa(f.StepCount), strings.Join(f.Operations, ",")})
			}
			app.Printer.Table([]string{"ID", "NAME", "TIER", "SHIPPED", "STEPS", "OPERATIONS"}, rows)
			return nil
		},
	}
}

// flowTier renders a flow's tier for the TIER column and the save
// summaries: local (this machine), team (the workspace repo), or
// service:<name>. "team" rather than "workspace" because the column answers
// "who else sees this?", and the workspace directory is where the team's
// copy lives.
func flowTier(kind, ownerID string) string {
	switch kind {
	case domain.FlowOwnerLocal:
		return "local"
	case domain.FlowOwnerService:
		if ownerID != "" {
			return "service:" + ownerID
		}
		return "service"
	default:
		return "team"
	}
}

// shipStateColumn renders a workspace-tier flow's domain.Ship* state (PLAN
// §7b) for `flow list`'s SHIPPED column: "not committed" (in flows/ but
// never added to git), "modified" (tracked, with uncommitted changes),
// "not pushed" (committed here, not yet on the upstream), or "shipped".
// Empty -- the local and service tiers, and a workspace not in git --
// renders an empty cell.
func shipStateColumn(state string) string {
	switch state {
	case domain.ShipUntracked:
		return "not committed"
	case domain.ShipModified:
		return "modified"
	case domain.ShipUnpushed:
		return "not pushed"
	case domain.ShipShipped:
		return "shipped"
	default:
		return ""
	}
}

// flowTierDescription is the longer form the tier commands print: where the
// flow lives and who can see it there.
func flowTierDescription(kind, ownerID string) string {
	switch kind {
	case domain.FlowOwnerLocal:
		return "local tier: this machine only"
	case domain.FlowOwnerService:
		return "service tier: " + ownerID + "/api/flows, rides your branch"
	default:
		return "team tier: the workspace repo"
	}
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
			// The tier rides as a YAML comment so `flow show x > x.flow.yaml`
			// still produces a file `flow create` accepts unchanged.
			app.Printer.Line("# tier: %s (%s)", flowTier(flow.OwnerKind, flow.OwnerID), flow.Path)
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
	var destPath, scope, service string
	cmd := &cobra.Command{
		Use:   "create <file> [--scope local|workspace|service] [--service <name>]",
		Short: "Save a flow file into a tier (default: local, this machine only)",
		Long: `Validate the flow YAML in <file> and save it as <id>.flow.yaml (or --path,
relative to the chosen tier's flows directory) into one of three tiers:

  local      <workspace>/local/flows: this machine only, never committed (default)
  workspace  <workspace>/flows: the team's repo, shared with everyone who clones it
  service    <service>/api/flows: the owning service's own repo; needs --service,
             and that service bound to a local checkout here (sapien service bind)

Start local. When the flow runs green and others would benefit from it, move
it up with "sapien flow promote <id>". Prints a summary of what was saved,
never the document.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, owner, err := flowScopeFlags(scope, service)
			if err != nil {
				return err
			}

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

			flow, err := eng.Flows().CreateIn(cmd.Context(), string(data), engine.CreateFlowOptions{
				Path: destPath, OwnerKind: kind, OwnerID: owner,
			})
			if err != nil {
				return err
			}
			if err := printFlowSaved(app, "saved", flow); err != nil {
				return err
			}
			if !app.Printer.IsJSON() && flow.OwnerKind == domain.FlowOwnerLocal {
				app.Printer.Line("%s", app.Printer.Dim("local tier: this machine only; `sapien flow promote "+flow.ID+"` shares it with the team when it works"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&destPath, "path", "", "destination relative to the chosen tier's flows directory (default: <id>.flow.yaml)")
	cmd.Flags().StringVar(&scope, "scope", domain.FlowOwnerLocal, "tier to save into: local (this machine), workspace (the team's repo), or service (needs --service)")
	cmd.Flags().StringVar(&service, "service", "", "owning service for --scope service")
	return cmd
}

// flowScopeFlags turns --scope/--service into the owner kind and id the
// engine takes, refusing the combinations that would file a flow somewhere
// the user did not mean: service scope without a service, or a service
// named for a tier that ignores it.
func flowScopeFlags(scope, service string) (kind, owner string, err error) {
	switch scope {
	case "", domain.FlowOwnerLocal:
		kind = domain.FlowOwnerLocal
	case domain.FlowOwnerWorkspace, "team":
		kind = domain.FlowOwnerWorkspace
	case domain.FlowOwnerService:
		kind = domain.FlowOwnerService
	default:
		return "", "", errs.New(errs.Invalid, "unknown scope %q", scope).
			WithHint("--scope is local (this machine), workspace (the team's repo), or service (the owning service; pass --service)")
	}
	if kind == domain.FlowOwnerService {
		if service == "" {
			return "", "", errs.New(errs.Invalid, "service scope requires --service").
				WithHint("pass --service <name>: the service whose api/flows should own this flow; it must be bound to a local checkout here (see `sapien service bind`)")
		}
		return kind, service, nil
	}
	if service != "" {
		return "", "", errs.New(errs.Invalid, "--service applies to --scope service only, not %s", kind).
			WithHint("drop --service, or pass --scope service")
	}
	return kind, "", nil
}

// tierRank orders the ladder promote climbs: local < workspace < service.
func tierRank(kind string) int {
	switch kind {
	case domain.FlowOwnerLocal:
		return 0
	case domain.FlowOwnerService:
		return 2
	default:
		return 1
	}
}

// newFlowPromoteCmd is `sapien flow promote <id> [--to workspace|service]
// [--service <name>]`: move a flow one rung up the ladder (local ->
// workspace -> service), or straight to --to. It refuses to move sideways
// or down, so "promote" always means "more people can see this now";
// `flow rescope` is the form with no such opinion.
func newFlowPromoteCmd(app *App) *cobra.Command {
	var to, service, message string
	var commit bool
	cmd := &cobra.Command{
		Use:   "promote <id> [--to workspace|service] [--service <name>] [--commit] [-m <message>]",
		Short: "Move a flow up a tier: local -> workspace -> service",
		Long: `Move a flow up the tier ladder, keeping its file name:

  local      this machine only (<workspace>/local/flows)
  workspace  the team's repo (<workspace>/flows), shared with everyone who clones it
  service    the owning service's repo (<service>/api/flows); needs --service, and
             that service bound to a local checkout here, so the flow rides your branch

Without --to the flow moves one rung up. Promote when the flow has run green
and others would benefit; use "sapien flow rescope" to move a flow down.

--commit additionally records the moved file in the workspace repository
with one commit, only for a promotion to the workspace tier; it never
pushes. Without --commit the file just sits in flows/ until you (or a
rerun with --commit) commit it by hand.`,
		Args: cobra.ExactArgs(1),
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
			current := flow.OwnerKind
			if current == "" {
				current = domain.FlowOwnerWorkspace
			}

			target := to
			if target == "" {
				switch current {
				case domain.FlowOwnerLocal:
					target = domain.FlowOwnerWorkspace
				case domain.FlowOwnerWorkspace:
					target = domain.FlowOwnerService
				default:
					return errs.New(errs.Invalid, "flow %s is already in the service tier (%s); there is nothing above it", flow.ID, flow.OwnerID)
				}
			}
			kind, owner, err := flowScopeFlags(target, service)
			if err != nil {
				return err
			}
			if kind == domain.FlowOwnerLocal {
				return errs.New(errs.Invalid, "promote moves a flow up; local is the bottom of the ladder").
					WithHint("use `sapien flow rescope " + flow.ID + " --scope local` to move it down")
			}
			if tierRank(kind) <= tierRank(current) {
				return errs.New(errs.Invalid, "flow %s is already in the %s tier; promote only moves up", flow.ID, flowTier(current, flow.OwnerID)).
					WithHint("use `sapien flow rescope " + flow.ID + " --scope <tier>` to move it elsewhere")
			}
			return rescopeFlow(app, cmd.Context(), eng, flow, kind, owner, engine.RescopeOptions{Commit: commit, Message: message})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "target tier: workspace or service (default: the next tier up)")
	cmd.Flags().StringVar(&service, "service", "", "owning service, required when the target is the service tier")
	cmd.Flags().BoolVar(&commit, "commit", false, "also commit the moved file in the workspace repository (workspace tier only); never pushes")
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message (with --commit); default: \"Promote flow <id> to the team workspace\"")
	return cmd
}

// newFlowRescopeCmd is `sapien flow rescope <id> --scope <tier> [--service
// <name>]`: the explicit form of promote, allowed to move a flow in any
// direction.
func newFlowRescopeCmd(app *App) *cobra.Command {
	var scope, service, message string
	var commit bool
	cmd := &cobra.Command{
		Use:   "rescope <id> --scope local|workspace|service [--service <name>] [--commit] [-m <message>]",
		Short: "Move a flow to another tier without losing it",
		Long: `Move a flow to another tier, keeping its file name: local is this machine
only, workspace is the team's repo, service is the owning service's repo
(needs --service and a bound local checkout). Unlike "flow promote" this
moves in any direction, so a shared flow can come back to local for private
experimentation.

--commit additionally records the moved file in the workspace repository
with one commit, only for a move to the workspace tier; it never pushes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if scope == "" {
				return errs.New(errs.Invalid, "flow: rescope requires --scope").
					WithHint("pass --scope local|workspace|service (with --service <name> for service)")
			}
			kind, owner, err := flowScopeFlags(scope, service)
			if err != nil {
				return err
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			flow, err := eng.Flows().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			current := flow.OwnerKind
			if current == "" {
				current = domain.FlowOwnerWorkspace
			}
			if current == kind && (kind != domain.FlowOwnerService || flow.OwnerID == owner) {
				return errs.New(errs.Invalid, "flow %s is already in the %s tier", flow.ID, flowTier(current, flow.OwnerID))
			}
			return rescopeFlow(app, cmd.Context(), eng, flow, kind, owner, engine.RescopeOptions{Commit: commit, Message: message})
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "local|workspace|service (required)")
	cmd.Flags().StringVar(&service, "service", "", "owning service for --scope service")
	cmd.Flags().BoolVar(&commit, "commit", false, "also commit the moved file in the workspace repository (workspace tier only); never pushes")
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message (with --commit); default: \"Promote flow <id> to the team workspace\"")
	return cmd
}

// rescopeFlow is what promote and rescope share once the target is
// settled: the engine move (through RescopeWith, so opts.Commit reaches
// it), then where the file went, and -- only for a move to the workspace
// tier -- whether it was committed.
func rescopeFlow(app *App, ctx context.Context, eng engine.Engine, flow *domain.Flow, kind, owner string, opts engine.RescopeOptions) error {
	oldPath := flow.Path
	moved, err := eng.Flows().RescopeWith(ctx, flow.ID, kind, owner, opts)
	if err != nil {
		return err
	}
	if app.Printer.IsJSON() {
		return app.Printer.JSON(map[string]any{
			"id":        moved.ID,
			"tier":      moved.OwnerKind,
			"service":   moved.OwnerID,
			"old_path":  oldPath,
			"new_path":  moved.Path,
			"committed": opts.Commit,
			"flow":      summarizeFlow(moved),
		})
	}
	desc := flowTierDescription(moved.OwnerKind, moved.OwnerID)
	if oldPath == "" && moved.Path == "" {
		app.Printer.Line("moved flow %s to the %s", moved.ID, desc)
	} else {
		app.Printer.Line("%s -> %s (%s)", oldPath, moved.Path, desc)
	}
	if moved.OwnerKind == domain.FlowOwnerWorkspace && !opts.Commit {
		app.Printer.Line("%s", app.Printer.Dim(fmt.Sprintf(
			"not committed yet: git add %s && git commit, or rerun with --commit", moved.Path)))
	}
	return nil
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
