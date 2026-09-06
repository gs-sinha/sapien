package cli

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/domain"
)

func init() { Register(newSearchCmd) }

func newSearchCmd(app *App) *cobra.Command {
	var method, service string
	var limit int
	var docs bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search operations (or, with --docs, documentation)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			query := args[0]
			opts := domain.SearchOptions{Service: service, Method: method, Limit: limit}

			if docs {
				results, err := eng.Search().Docs(cmd.Context(), query, opts)
				if err != nil {
					return err
				}
				if app.Printer.IsJSON() {
					return app.Printer.JSON(results)
				}
				rows := make([][]string, 0, len(results))
				for _, r := range results {
					rows = append(rows, []string{
						formatScore(r.Score), r.Service, r.Path, r.Heading, r.Snippet,
					})
				}
				app.Printer.Table([]string{"SCORE", "SERVICE", "PATH", "HEADING", "SNIPPET"}, rows)
				return nil
			}

			results, err := eng.Search().Operations(cmd.Context(), query, opts)
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(results)
			}
			rows := make([][]string, 0, len(results))
			for _, r := range results {
				op := r.Operation
				path, methodStr := "", ""
				if op.HTTP != nil {
					path, methodStr = op.HTTP.Path, op.HTTP.Method
				}
				rows = append(rows, []string{
					formatScore(r.Score), op.ID, methodStr, path, op.Summary, strings.Join(r.MatchedOn, ","),
				})
			}
			app.Printer.Table([]string{"SCORE", "ID", "METHOD", "PATH", "SUMMARY", "MATCHED"}, rows)
			return nil
		},
	}

	cmd.Flags().StringVar(&method, "method", "", "filter by HTTP method")
	cmd.Flags().StringVar(&service, "service", "", "filter by service name")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum results (default: search-defined)")
	cmd.Flags().BoolVar(&docs, "docs", false, "search documentation instead of operations")
	return cmd
}

func formatScore(score float64) string {
	return strconv.FormatFloat(score, 'f', 2, 64)
}
