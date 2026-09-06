package cli

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/domain"
)

func init() { Register(newDescribeCmd) }

func newDescribeCmd(app *App) *cobra.Command {
	var showFields, showExamples bool

	cmd := &cobra.Command{
		Use:   "describe <operation-ref>",
		Short: "Show one operation's contract",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			ctx := cmd.Context()
			op, err := eng.Catalog().ResolveOperation(ctx, args[0])
			if err != nil {
				return err
			}
			fields, err := eng.Catalog().Fields(ctx, op.ID)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{"operation": op, "fields": fields})
			}

			printDescribeHuman(app.Printer, op, fields, showFields, showExamples)
			return nil
		},
	}

	cmd.Flags().BoolVar(&showFields, "fields", false, "print the full flattened field list")
	cmd.Flags().BoolVar(&showExamples, "examples", false, "print examples as JSON")
	return cmd
}

func printDescribeHuman(p *Printer, op *domain.Operation, fields []domain.Field, showFields, showExamples bool) {
	method, path := "", ""
	if op.HTTP != nil {
		method, path = op.HTTP.Method, op.HTTP.Path
	}
	p.Line("%s %s  %s", method, path, op.ID)

	if op.Summary != "" {
		p.Line("%s", op.Summary)
	}
	if op.Description != "" {
		p.Line("%s", op.Description)
	}

	if len(op.Params) > 0 {
		p.Line("")
		rows := make([][]string, 0, len(op.Params))
		for _, param := range op.Params {
			rows = append(rows, []string{param.Name, string(param.In), schemaTypeString(param.Schema), strconv.FormatBool(param.Required), param.Description})
		}
		p.Table([]string{"NAME", "IN", "TYPE", "REQUIRED", "DESCRIPTION"}, rows)
	}

	if op.RequestBody != nil {
		p.Line("")
		p.Line("request body (%s):", op.RequestBody.ContentType)
		for _, f := range fieldsWithPrefix(fields, "request.body.") {
			p.Line("  %s: %s — %s", f.relPath, f.Type, f.Description)
		}
	}

	if len(op.Responses) > 0 {
		p.Line("")
		p.Line("responses:")
		for _, r := range op.Responses {
			p.Line("  %s (%s)", r.Status, r.ContentType)
			prefix := "response." + r.Status + ".body."
			for _, f := range topLevelFields(fields, prefix) {
				p.Line("    %s: %s — %s", f.relPath, f.Type, f.Description)
			}
		}
	}

	if len(op.Security) > 0 {
		names := make([]string, len(op.Security))
		for i, s := range op.Security {
			names[i] = s.Scheme
		}
		p.Line("")
		p.Line("security: %s", strings.Join(names, ", "))
	}

	if op.Deprecated {
		p.Line("")
		p.Line("DEPRECATED")
	}

	if op.Source.File != "" {
		loc := op.Source.File
		if op.Source.Line > 0 {
			loc += ":" + strconv.Itoa(op.Source.Line)
		}
		p.Line("")
		p.Line("source: %s", loc)
	}

	if showFields {
		p.Line("")
		p.Line("fields:")
		rows := make([][]string, 0, len(fields))
		for _, f := range fields {
			rows = append(rows, []string{f.Path, f.Type, strconv.FormatBool(f.Required), f.Description})
		}
		p.Table([]string{"PATH", "TYPE", "REQUIRED", "DESCRIPTION"}, rows)
	}

	if showExamples {
		examples := map[string]any{}
		if op.RequestBody != nil && len(op.RequestBody.Examples) > 0 {
			examples["request"] = op.RequestBody.Examples
		}
		for _, r := range op.Responses {
			if len(r.Examples) > 0 {
				examples["response_"+r.Status] = r.Examples
			}
		}
		p.Line("")
		_ = p.JSON(examples)
	}
}

func schemaTypeString(s *domain.Schema) string {
	if s == nil {
		return ""
	}
	return string(s.Kind)
}

// fieldView is a Field with its path relativized against a stripped prefix,
// for compact "path: type — description" rendering (PLAN §14).
type fieldView struct {
	relPath string
	domain.Field
}

// fieldsWithPrefix returns every field whose Path starts with prefix, with
// the prefix stripped for display.
func fieldsWithPrefix(fields []domain.Field, prefix string) []fieldView {
	var out []fieldView
	for _, f := range fields {
		if rel, ok := strings.CutPrefix(f.Path, prefix); ok {
			out = append(out, fieldView{relPath: rel, Field: f})
		}
	}
	return out
}

// topLevelFields returns only the immediate children of prefix (no further
// "." in the relativized path), for a response's compact top-level summary.
func topLevelFields(fields []domain.Field, prefix string) []fieldView {
	var out []fieldView
	for _, f := range fieldsWithPrefix(fields, prefix) {
		if !strings.Contains(f.relPath, ".") {
			out = append(out, f)
		}
	}
	return out
}
