package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/growsimplee/sapien/internal/domain"
)

func init() { Register(newContextCmd) }

func newContextCmd(app *App) *cobra.Command {
	var ops []string
	var flow string
	var budget int

	cmd := &cobra.Command{
		Use:   "context <intent>",
		Short: "Build an agent-ready context bundle for an intent",
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

			req := domain.ContextRequest{
				Intent:       args[0],
				Operations:   ops,
				Flow:         flow,
				Environment:  envName,
				BudgetTokens: budget,
			}

			bundle, err := eng.Context().Build(cmd.Context(), req)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(bundle)
			}
			printContextHuman(app.Printer, bundle)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&ops, "op", nil, "operation id to include (repeatable; default: relevant to the intent)")
	cmd.Flags().StringVar(&flow, "flow", "", "flow id to include")
	cmd.Flags().IntVar(&budget, "budget", 0, "token budget (default: engine-defined, ~8000)")
	return cmd
}

// printContextHuman renders a ContextBundle as labeled sections (PLAN §14):
// Operations, Docs, Memories, Flows, Runs, each tagged with its knowledge
// tier, followed by an "omitted:" footer when the budget cut anything.
func printContextHuman(p *Printer, b *domain.ContextBundle) {
	p.Line("intent: %s", b.Intent)
	p.Line("")

	if len(b.Operations) > 0 {
		p.Line("Operations")
		for _, op := range b.Operations {
			p.Line("  [%s] %s %s  %s — %s", op.Tier, op.Method, op.Path, op.ID, op.Summary)
		}
		p.Line("")
	}
	if len(b.Docs) > 0 {
		p.Line("Docs")
		for _, d := range b.Docs {
			p.Line("  [%s] %s/%s#%s", d.Tier, d.Service, d.Path, d.Heading)
		}
		p.Line("")
	}
	if len(b.Memories) > 0 {
		p.Line("Memories")
		for _, m := range b.Memories {
			p.Line("  [%s] %s (%s) — %s", m.Tier, m.ID, m.Type, truncate(m.Text, 80))
		}
		p.Line("")
	}
	if len(b.Flows) > 0 {
		p.Line("Flows")
		for _, f := range b.Flows {
			p.Line("  %s: %s", f.ID, strings.Join(f.Steps, ", "))
		}
		p.Line("")
	}
	if len(b.Runs) > 0 {
		p.Line("Runs")
		for _, r := range b.Runs {
			p.Line("  [%s] %s %s — %s", r.Tier, r.ID, r.Status, r.Summary)
		}
		p.Line("")
	}

	if len(b.Omitted) > 0 {
		keys := make([]string, 0, len(b.Omitted))
		for k := range b.Omitted {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s: %d", k, b.Omitted[k]))
		}
		p.Line("omitted: %s", strings.Join(parts, ", "))
	}
	p.Line("estimated tokens: %d", b.EstimatedTokens)
}
