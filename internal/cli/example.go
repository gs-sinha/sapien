package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

func init() { Register(newExampleCmd) }

func newExampleCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "example",
		Short: "Save and replay known-good request examples",
	}
	cmd.AddCommand(
		newExampleListCmd(app),
		newExampleShowCmd(app),
		newExampleAddCmd(app),
		newExampleRmCmd(app),
		newExampleRescopeCmd(app),
		newExampleMoveCmd(app),
		newExampleMvCmd(app),
		newExampleCommitCmd(app),
	)
	return cmd
}

func newExampleListCmd(app *App) *cobra.Command {
	var operation, service, tag, text, folderFilter string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List saved examples",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			q := domain.ExampleQuery{Operation: operation, Service: service, Tag: tag, Text: text, Folder: folderFilter}
			examples, err := eng.Examples().List(cmd.Context(), q)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(examples)
			}
			showFolder := anyExampleHasFolder(examples)
			header := []string{"ID", "OPERATION", "SCOPE", "TIER", "SHIPPED", "VERIFIED", "UPDATED", "DESCRIPTION"}
			if showFolder {
				header = []string{"ID", "OPERATION", "SCOPE", "FOLDER", "TIER", "SHIPPED", "VERIFIED", "UPDATED", "DESCRIPTION"}
			}
			rows := make([][]string, 0, len(examples))
			for _, ex := range examples {
				row := []string{
					ex.ID, ex.Operation, string(ex.Scope), tierColumn(ex.Tier), shipStateColumn(ex.Shipped),
					verifiedEnvString(ex.Verified), formatExampleTime(ex.Updated), truncate(ex.Description, 60),
				}
				if showFolder {
					row = []string{
						ex.ID, ex.Operation, string(ex.Scope), ex.Folder, tierColumn(ex.Tier), shipStateColumn(ex.Shipped),
						verifiedEnvString(ex.Verified), formatExampleTime(ex.Updated), truncate(ex.Description, 60),
					}
				}
				rows = append(rows, row)
			}
			app.Printer.Table(header, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&operation, "operation", "", "filter by operation id")
	cmd.Flags().StringVar(&service, "service", "", "filter by service")
	cmd.Flags().StringVar(&tag, "tag", "", "filter by tag")
	cmd.Flags().StringVar(&text, "text", "", "filter: substring over id, description, tags, folder")
	cmd.Flags().StringVar(&folderFilter, "folder", "", "restrict to this folder and everything below it")
	return cmd
}

// anyExampleHasFolder reports whether any example in exs carries a
// non-root folder, so `example list`'s table only grows a FOLDER column
// when it would actually show something (PLAN §34f item 5).
func anyExampleHasFolder(exs []domain.SavedExample) bool {
	for _, ex := range exs {
		if ex.Folder != "" {
			return true
		}
	}
	return false
}

// verifiedEnvString renders the VERIFIED column of `example list`: the
// environment an example was verified against, or "-" for a hand-written
// example that has never been run.
func verifiedEnvString(v *domain.ExampleVerified) string {
	if v == nil || v.Env == "" {
		return "-"
	}
	return v.Env
}

// formatExampleTime renders a timestamp the way `run list`'s STARTED
// column does: RFC3339 in UTC, or "" for the zero value.
func formatExampleTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func newExampleShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a saved example",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			ex, err := eng.Examples().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(ex)
			}
			app.Printer.Line("%s", renderExampleYAML(ex))
			return nil
		},
	}
}

// renderExampleYAML renders ex the way it is stored on disk: YAML via
// yaml.v3 over domain.SavedExample's own yaml tags, so the field order
// (version, id, operation, description, input, body, headers, expect,
// verified, tags, created, updated) matches the file exactly, preceded by
// its path as a comment line when known.
func renderExampleYAML(ex *domain.SavedExample) string {
	var b strings.Builder
	if ex.Tier != "" {
		fmt.Fprintf(&b, "# tier: %s\n", tierWithShip(ex.Tier, ex.Shipped))
	}
	if ex.Path != "" {
		fmt.Fprintf(&b, "# %s\n", ex.Path)
	}
	if data, err := yaml.Marshal(ex); err == nil {
		b.Write(data)
	}
	return strings.TrimRight(b.String(), "\n")
}

func newExampleRmCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a saved example",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.Examples().Delete(cmd.Context(), args[0]); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"removed": args[0]})
			}
			app.Printer.Line("removed %s", args[0])
			return nil
		},
	}
}

func newExampleRescopeCmd(app *App) *cobra.Command {
	var scope string

	cmd := &cobra.Command{
		Use:   "rescope <id> --scope <scope>",
		Short: "Move a saved example to another scope without losing it",
		Long: `Move a saved example between scopes: the same fix as "rm" then "add",
minus the chance of losing the payload in between.

Scope decides storage and sharing: service scope commits the example
under the service's own api/examples, reviewable by anyone who clones
that repo; workspace scope keeps it local under <workspace>/examples,
with no git history unless the workspace itself happens to be a git
repo.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if scope == "" {
				return errs.New(errs.Invalid, "example: rescope requires --scope").
					WithHint("pass --scope workspace|service")
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			ex, err := eng.Examples().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			updated := *ex
			oldPath := ex.Path
			updated.Scope = domain.ExampleScope(scope)

			moved, err := eng.Examples().Update(cmd.Context(), updated)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{
					"id":       moved.ID,
					"old_path": oldPath,
					"new_path": moved.Path,
					"example":  moved,
				})
			}
			app.Printer.Line("%s -> %s", oldPath, moved.Path)
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "workspace|service (required)")
	return cmd
}

func newExampleAddCmd(app *App) *cobra.Command {
	var id, description, scope, folderArg string
	var tags []string
	var params []string
	var headers []string
	var body string

	cmd := &cobra.Command{
		Use:   "add <operation>",
		Short: "Save a hand-written example for one operation",
		Long: `Save a hand-written example: a request you already know is shaped
right for one operation, without having run it through Sapien first. It
carries no "verified" record: only "sapien call --save-example" and
"sapien run save-example" set that, from a real run, so a tested example
can always be told from a drafted one.

Scope decides storage and sharing, not subject: service scope commits
the example under the service's own api/examples, reviewable by anyone
who clones that repo; workspace scope keeps it local under
<workspace>/examples.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			paramMap, err := parseParams(params)
			if err != nil {
				return err
			}
			headerMap, err := parseHeaders(headers)
			if err != nil {
				return err
			}
			bodyVal, err := parseBody(body)
			if err != nil {
				return err
			}

			exID := id
			if exID == "" {
				exID = defaultExampleID(args[0])
			}

			ex := domain.SavedExample{
				ID:          exID,
				Operation:   args[0],
				Description: description,
				Scope:       domain.ExampleScope(scope),
				Input:       paramMap,
				Body:        bodyVal,
				Headers:     headerMap,
				Tags:        tags,
				Folder:      folderArg,
			}

			created, err := eng.Examples().Create(cmd.Context(), ex)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(created)
			}
			app.Printer.Line("created %s", created.ID)
			printExampleUseHint(app, created)
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "example id (default: derived from the operation)")
	cmd.Flags().StringVar(&description, "description", "", "example description")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	cmd.Flags().StringVar(&scope, "scope", "workspace", "workspace|service")
	cmd.Flags().StringArrayVarP(&params, "param", "p", nil, "operation parameter key=value; JSON-decoded when it parses (repeatable)")
	cmd.Flags().StringVar(&body, "body", "", "request body: inline JSON, or @file to read it from a file")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "extra request header key:value (repeatable)")
	cmd.Flags().StringVar(&folderArg, "folder", "", "subfolder of the scope's examples directory to save into; default the root")
	return cmd
}

// newExampleMoveCmd is `sapien example move <id> --to local|team`: places a
// workspace-scope example's file in another tier, keeping its id and
// scope. Mirrors `sapien memory move`.
func newExampleMoveCmd(app *App) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "move <id> --to local|team",
		Short: "Move a workspace-scope example's file to another tier",
		Long: `Move a workspace-scope example's file between tiers, keeping its id and
scope: local (` + "`<workspace>/local/examples`" + `, this machine only, never
committed) or team (` + "`<workspace>/examples`" + `, the team's repo). Refused for
service scope, whose home is the service's own repo, not a tier.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tier, err := tierFlag("example move", to)
			if err != nil {
				return err
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			ex, err := eng.Examples().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			oldPath := ex.Path

			moved, err := eng.Examples().Move(cmd.Context(), args[0], tier)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{
					"id":       moved.ID,
					"tier":     moved.Tier,
					"old_path": oldPath,
					"new_path": moved.Path,
					"example":  moved,
				})
			}
			app.Printer.Line("%s -> %s", oldPath, moved.Path)
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "local|team (required)")
	return cmd
}

// newExampleMvCmd is `sapien example mv <id> <folder>`: move an example's
// file to another folder within its current directory, keeping its scope
// and tier (PLAN §34f item 6). <folder> "" or "/" moves it to the root.
// Distinct from `example move`, which moves between tiers and keeps folder.
func newExampleMvCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "mv <id> <folder>",
		Short: "Move a saved example to another folder, keeping its tier",
		Long: `Move a saved example's file to another folder within its current
directory, keeping its scope and tier. <folder> "" or "/" moves it to the
root. Refused when a file already exists at the destination, or for a
service-scope example read from a managed git clone. Use
"sapien example move" to move between tiers instead; it keeps the
example's current folder.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			ex, err := eng.Examples().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			oldPath := ex.Path

			moved, err := eng.Examples().MoveFolder(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{
					"id":       moved.ID,
					"folder":   moved.Folder,
					"old_path": oldPath,
					"new_path": moved.Path,
					"example":  moved,
				})
			}
			app.Printer.Line("%s -> %s", oldPath, moved.Path)
			return nil
		},
	}
}

// newExampleCommitCmd is `sapien example commit <id> [-m <message>]` or
// `sapien example commit --all`: records a workspace-tier example's file
// in the workspace repository with one commit, never a push. Mirrors
// `sapien memory commit`.
func newExampleCommitCmd(app *App) *cobra.Command {
	var message string
	var all bool
	cmd := &cobra.Command{
		Use:   "commit <id> [-m <message>]",
		Short: "Commit a workspace-tier example's file in the workspace repository",
		Long: `Record a workspace-tier example's file with one commit; never pushes.
Refused when the example is not at the team tier, the workspace is not a
git repository, or the file already has nothing to commit.

--all commits every team-tier example that is not committed or modified
(untracked, or tracked with an uncommitted edit), one commit per example,
using the same message for each (or each example's own default when -m is
omitted); an example already committed is left alone.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case all && len(args) == 1:
				return errs.New(errs.Invalid, "example commit: pass an example id or --all, not both")
			case !all && len(args) != 1:
				return errs.New(errs.Invalid, "example commit: requires an example id, or --all")
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if !all {
				return commitOneExample(app, cmd.Context(), eng, args[0], message)
			}
			return commitAllExamples(app, cmd.Context(), eng, message)
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message; default depends on whether the file was ever added to git")
	cmd.Flags().BoolVar(&all, "all", false, "commit every team-tier example that is not committed or modified")
	return cmd
}

func commitOneExample(app *App, ctx context.Context, eng engine.Engine, id, message string) error {
	ex, err := eng.Examples().Commit(ctx, id, message)
	if err != nil {
		return err
	}
	if app.Printer.IsJSON() {
		return app.Printer.JSON(ex)
	}
	app.Printer.Line("committed %s (%s)", ex.Path, shipStateColumn(ex.Shipped))
	return nil
}

// commitAllExamples is `example commit --all`: every team-tier example
// whose Shipped state says it has something to commit (untracked or
// modified), committed one at a time with the same message (id order, so
// output is stable).
func commitAllExamples(app *App, ctx context.Context, eng engine.Engine, message string) error {
	examples, err := eng.Examples().List(ctx, domain.ExampleQuery{})
	if err != nil {
		return err
	}
	var ids []string
	for _, ex := range examples {
		if ex.Tier != domain.TierWorkspace {
			continue
		}
		if ex.Shipped == domain.ShipUntracked || ex.Shipped == domain.ShipModified {
			ids = append(ids, ex.ID)
		}
	}
	sort.Strings(ids)

	if len(ids) == 0 {
		if app.Printer.IsJSON() {
			return app.Printer.JSON([]domain.SavedExample{})
		}
		app.Printer.Line("nothing to commit")
		return nil
	}

	results := make([]*domain.SavedExample, 0, len(ids))
	for _, id := range ids {
		ex, err := eng.Examples().Commit(ctx, id, message)
		if err != nil {
			return err
		}
		results = append(results, ex)
	}
	if app.Printer.IsJSON() {
		return app.Printer.JSON(results)
	}
	for _, ex := range results {
		app.Printer.Line("committed %s (%s)", ex.Path, shipStateColumn(ex.Shipped))
	}
	return nil
}

// defaultExampleID derives a default example id from the operation
// reference for `example add` when --id is omitted: the operation's raw
// name (the part after its last '.'), slugified with the same slugify
// docs.go uses for doc anchors. It is not guaranteed unique; a collision
// surfaces as the engine's own E_CONFLICT, and the caller can retry with
// an explicit --id.
func defaultExampleID(operation string) string {
	base := operation
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[i+1:]
	}
	return slugify(base)
}

// exampleSavedLine renders the confirmation line shared by `sapien call
// --save-example` and `sapien run save-example`: "saved example <id>
// (verified against <env>, status <n>) -> <path>". status is the HTTP
// status recorded on the example (from the run step's response); env is
// the environment the run executed against.
func exampleSavedLine(ex *domain.SavedExample) string {
	env := ""
	if ex.Verified != nil {
		env = ex.Verified.Env
	}
	status := 0
	if ex.Expect != nil {
		status = ex.Expect.Status
	}
	return fmt.Sprintf("saved example %s (verified against %s, status %d) -> %s", ex.ID, env, status, ex.Path)
}

// printExampleUseHint prints the one hint `example add` and `sapien call
// --save-example` both end with (PLAN §34b build item 4): how to replay
// the example, plus the same "local to this machine" note the memory
// commands print when a workspace-scoped item has nowhere to travel.
func printExampleUseHint(app *App, ex *domain.SavedExample) {
	if ex.Scope == domain.ExampleScopeWorkspace {
		if ws, err := app.Workspace(); err == nil && ws != nil && !isGitRepo(ws.Dir) {
			app.Printer.Line("note: %s is not a git repo, so workspace examples live only on this machine", ws.Dir)
		}
	}
	app.Printer.Line("use it: `sapien call --example %s`, or in a flow step: `example: %s`", ex.ID, ex.ID)
}
