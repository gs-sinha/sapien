package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/catalog"
)

func init() { Register(newReindexCmd) }

// statsProvider is implemented by engine.Engine values that can report
// catalog size (PLAN §15's Stats), e.g. *local.Local. engine.CatalogAPI has
// no facade for this yet (it wasn't needed before `sapien reindex`), so
// reindex asks for it structurally rather than requiring every engine
// implementation to carry it on the interface from day one.
type statsProvider interface {
	Stats(ctx context.Context) (catalog.Stats, error)
}

func newReindexCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the catalog for every registered service from canonical files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			ctx := cmd.Context()
			reindexErr := eng.Services().Reindex(ctx)

			svcs, listErr := eng.Services().List(ctx)
			if listErr != nil {
				if reindexErr != nil {
					return reindexErr
				}
				return listErr
			}

			var stats catalog.Stats
			if sp, ok := eng.(statsProvider); ok {
				stats, _ = sp.Stats(ctx)
			}

			if app.Printer.IsJSON() {
				if jerr := app.Printer.JSON(map[string]any{"services": svcs, "stats": stats}); jerr != nil {
					return jerr
				}
				return reindexErr
			}

			for _, svc := range svcs {
				printServiceHuman(app.Printer, &svc)
			}
			app.Printer.Line("")
			app.Printer.Line("services: %d, operations: %d, fields: %d, docs: %d, sections: %d, flows: %d",
				stats.Services, stats.Operations, stats.Fields, stats.Docs, stats.Sections, stats.Flows)

			return reindexErr
		},
	}
}
