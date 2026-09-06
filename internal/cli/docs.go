package cli

import (
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func init() { Register(newDocsCmd) }

func newDocsCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Browse and search service documentation",
	}
	cmd.AddCommand(newDocsListCmd(app), newDocsSearchCmd(app), newDocsShowCmd(app))
	return cmd
}

func newDocsListCmd(app *App) *cobra.Command {
	var service string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List documentation files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			docs, err := eng.Catalog().ListDocs(cmd.Context(), service)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(docs)
			}

			rows := make([][]string, 0, len(docs))
			for _, d := range docs {
				rows = append(rows, []string{d.ServiceID, d.Path, d.Title, string(d.Source)})
			}
			app.Printer.Table([]string{"SERVICE", "PATH", "TITLE", "SOURCE"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "filter by service name")
	return cmd
}

func newDocsSearchCmd(app *App) *cobra.Command {
	var service string

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search documentation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			results, err := eng.Search().Docs(cmd.Context(), args[0], domain.SearchOptions{Service: service})
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(results)
			}

			rows := make([][]string, 0, len(results))
			for _, r := range results {
				rows = append(rows, []string{formatScore(r.Score), r.Service, r.Path, r.Heading, r.Snippet})
			}
			app.Printer.Table([]string{"SCORE", "SERVICE", "PATH", "HEADING", "SNIPPET"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "filter by service name")
	return cmd
}

func newDocsShowCmd(app *App) *cobra.Command {
	var section string

	cmd := &cobra.Command{
		Use:   "show <service> <path>",
		Short: "Show a documentation file (or one section of it)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			service, path := args[0], args[1]
			doc, err := eng.Catalog().GetDoc(cmd.Context(), service, path)
			if err != nil {
				return err
			}

			if section != "" {
				sec := findSection(doc, section)
				if sec == nil {
					return errs.New(errs.Invalid, "section %q not found in %s/%s", section, service, path).
						WithHint("run `sapien docs show` without --section to see every heading")
				}
				if app.Printer.IsJSON() {
					out := *doc
					out.Sections = []domain.DocSection{*sec}
					return app.Printer.JSON(out)
				}
				app.Printer.Line("%s", renderSection(*sec))
				return nil
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(doc)
			}
			app.Printer.Line("%s", renderDoc(doc))
			return nil
		},
	}
	cmd.Flags().StringVar(&section, "section", "", "show only the section matching this heading (or its slug)")
	return cmd
}

// findSection locates the section within doc matching want, by exact
// (case-insensitive) heading text, by slug, or by a literal DocSection.ID
// suffix ("#"+want).
func findSection(doc *domain.Doc, want string) *domain.DocSection {
	wantSlug := slugify(want)
	for i := range doc.Sections {
		sec := &doc.Sections[i]
		if strings.EqualFold(sec.Heading, want) {
			return sec
		}
		if slugify(sec.Heading) == wantSlug {
			return sec
		}
		if strings.HasSuffix(sec.ID, "#"+want) || strings.HasSuffix(sec.ID, "#"+wantSlug) {
			return sec
		}
	}
	return nil
}

var slugTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

// slugify mirrors internal/ingest/docs.Slug's heading -> slug rule (lower-case,
// runs of [a-z0-9] joined by '-') without importing that ingest package.
func slugify(s string) string {
	parts := slugTokenPattern.FindAllString(strings.ToLower(s), -1)
	return strings.Join(parts, "-")
}

// renderSection reconstructs one section as Markdown: a heading line (level
// hashes) followed by its body.
func renderSection(sec domain.DocSection) string {
	var b strings.Builder
	level := sec.Level
	if level < 1 {
		level = 2
	}
	b.WriteString(strings.Repeat("#", level))
	b.WriteString(" ")
	b.WriteString(sec.Heading)
	b.WriteString("\n\n")
	b.WriteString(sec.Body)
	return strings.TrimRight(b.String(), "\n")
}

// renderDoc reconstructs the full doc as Markdown from its sections.
func renderDoc(doc *domain.Doc) string {
	var b strings.Builder
	for i, sec := range doc.Sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(renderSection(sec))
	}
	return b.String()
}
