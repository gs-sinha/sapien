package cli

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/flowpatch"
)

// flowSaveSummary is what the authoring commands print after a write: enough
// to confirm what landed where, never the document (PLAN §34d).
type flowSaveSummary struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// Tier is the owner kind the file lives in (local, workspace, service);
	// Service names the owner for the service tier.
	Tier          string   `json:"tier"`
	Service       string   `json:"service,omitempty"`
	Steps         int      `json:"steps"`
	SetupSteps    int      `json:"setup_steps"`
	TeardownSteps int      `json:"teardown_steps"`
	Operations    []string `json:"operations"`
	// Notes carries `flow patch`'s comment-handling notes (see
	// flowpatch.Result.Notes); always empty for `flow update`.
	Notes []string `json:"notes,omitempty"`
}

func summarizeFlow(f *domain.Flow) flowSaveSummary {
	seen := map[string]bool{}
	var ops []string
	for _, list := range [][]domain.Step{f.Setup, f.Steps, f.Teardown} {
		for _, st := range list {
			if st.Call != "" && !seen[st.Call] {
				seen[st.Call] = true
				ops = append(ops, st.Call)
			}
		}
	}
	sum := flowSaveSummary{ID: f.ID, Path: f.Path, Tier: f.OwnerKind, Steps: len(f.Steps), SetupSteps: len(f.Setup), TeardownSteps: len(f.Teardown), Operations: ops}
	if f.OwnerKind == domain.FlowOwnerService {
		sum.Service = f.OwnerID
	}
	return sum
}

// printFlowSaved prints the lean summary every authoring command ends
// with. notes (only ever non-empty from `flow patch`, see flowpatch.Result)
// are attached to the summary in JSON mode and printed one per line
// otherwise.
func printFlowSaved(app *App, verb string, f *domain.Flow, notes ...string) error {
	sum := summarizeFlow(f)
	sum.Notes = notes
	if app.Printer.IsJSON() {
		return app.Printer.JSON(sum)
	}
	extra := ""
	if sum.SetupSteps > 0 || sum.TeardownSteps > 0 {
		extra = " (+" + itoa(sum.SetupSteps) + " setup, " + itoa(sum.TeardownSteps) + " teardown)"
	}
	where := ""
	if sum.Path != "" {
		where = " at " + sum.Path
	}
	app.Printer.Line("%s flow %s%s [%s], %d steps%s", verb, sum.ID, where, flowTier(f.OwnerKind, f.OwnerID), sum.Steps, extra)
	for _, n := range notes {
		app.Printer.Line("note: %s", n)
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// newFlowUpdateCmd is `sapien flow update <id> --file <path>`: re-read a
// flow file already edited on disk (or any YAML file), validate it, and save
// it as that flow. The cheap path for "I fixed one line in the editor".
func newFlowUpdateCmd(app *App) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <id> --file <path>",
		Short: "Replace a flow's YAML from a file edited on disk",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return errs.New(errs.Invalid, "--file is required").WithHint("pass the edited flow file; use `sapien flow patch` for step-level edits")
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return errs.Wrap(errs.Invalid, err, "reading %s", file)
			}
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()
			f, err := eng.Flows().Update(cmd.Context(), args[0], string(data))
			if err != nil {
				return err
			}
			return printFlowSaved(app, "updated", f)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "path of the edited flow YAML")
	return cmd
}

// newFlowPatchCmd is `sapien flow patch <id> [--ops @ops.json] [--merge-step
// <step> <yaml>]... [--remove-step <step>]...`: step-level edits applied to
// the flow's YAML with comments and order preserved (internal/flowpatch),
// then validated and saved.
func newFlowPatchCmd(app *App) *cobra.Command {
	var opsFile string
	var merges, removes []string
	cmd := &cobra.Command{
		Use:   "patch <id>",
		Short: "Edit a flow step by step without resending the whole file",
		Long: `Apply step-level operations to a flow's YAML, preserving comments and order:

  --ops @ops.json           a JSON array of operations (kinds: set_step, merge_step,
                            add_step, remove_step, set_inputs, set_meta; see docs/flows.md)
  --merge-step <id>=<yaml>  set some keys of one step, e.g. 'rider={assert: [status == 200]}'
  --remove-step <id>        drop a step

The result is validated before it is saved; an invalid patch changes nothing.

This is layout-preserving, not lossless: a step the patch doesn't touch keeps
its own comments, key order, and indent width (2 or 4 spaces, matched from the
file); a step the patch writes (set_step, add_step, a merge_step's newly-added
field) uses the flow DSL's own conventional key order, never alphabetical.
add_step's --ops before/after infers which phase (and, nested inside a loop
block, which block) to insert into from the named step, wherever it actually
is; an explicit phase/into that disagrees with it is rejected, naming where
that step really is. A step's own leading comment survives merge_step (noted,
since what's under it just changed) but not set_step or remove_step (also
noted); blank lines between steps are never preserved.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var ops []flowpatch.Op
			if opsFile != "" {
				data, err := readArg(opsFile)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(data, &ops); err != nil {
					return errs.Wrap(errs.Invalid, err, "parsing ops JSON")
				}
			}
			for _, m := range merges {
				id, body, ok := strings.Cut(m, "=")
				if !ok || strings.TrimSpace(id) == "" {
					return errs.New(errs.Invalid, "--merge-step wants <step-id>=<yaml or json>, got %q", m)
				}
				var fields map[string]any
				if err := yaml.Unmarshal([]byte(body), &fields); err != nil || fields == nil {
					return errs.New(errs.Invalid, "--merge-step %s: value must be a YAML/JSON mapping of step keys", id)
				}
				ops = append(ops, flowpatch.Op{Kind: "merge_step", ID: strings.TrimSpace(id), Fields: fields})
			}
			for _, id := range removes {
				ops = append(ops, flowpatch.Op{Kind: "remove_step", ID: strings.TrimSpace(id)})
			}
			if len(ops) == 0 {
				return errs.New(errs.Invalid, "nothing to patch").WithHint("pass --ops, --merge-step, or --remove-step")
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()
			f, err := eng.Flows().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if f.Source == "" {
				return errs.New(errs.Internal, "flow %s has no YAML source to patch", f.ID)
			}
			patchResult, err := flowpatch.Apply(f.Source, ops)
			if err != nil {
				return err
			}
			updated, err := eng.Flows().Update(cmd.Context(), f.ID, patchResult.YAML)
			if err != nil {
				return err
			}
			return printFlowSaved(app, "patched", updated, patchResult.Notes...)
		},
	}
	cmd.Flags().StringVar(&opsFile, "ops", "", "operations as JSON: inline, or @file")
	cmd.Flags().StringArrayVar(&merges, "merge-step", nil, "<step-id>=<yaml mapping of keys to set> (repeatable)")
	cmd.Flags().StringArrayVar(&removes, "remove-step", nil, "step id to remove (repeatable)")
	return cmd
}

// readArg returns the bytes of an argument that is either inline text or
// "@path" naming a file.
func readArg(v string) ([]byte, error) {
	if strings.HasPrefix(v, "@") {
		data, err := os.ReadFile(v[1:])
		if err != nil {
			return nil, errs.Wrap(errs.Invalid, err, "reading %s", v[1:])
		}
		return data, nil
	}
	return []byte(v), nil
}
