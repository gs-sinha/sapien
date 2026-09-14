package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

func init() { Register(newMemoryCmd) }

func newMemoryCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Capture and retrieve memories",
	}
	cmd.AddCommand(
		newMemoryAddCmd(app),
		newMemoryListCmd(app),
		newMemorySearchCmd(app),
		newMemoryShowCmd(app),
		newMemoryRmCmd(app),
		newMemoryPromoteCmd(app),
		newMemoryRescopeCmd(app),
		newMemoryMoveCmd(app),
		newMemoryCommitCmd(app),
		newMemoryReindexCmd(app),
	)
	return cmd
}

func newMemoryAddCmd(app *App) *cobra.Command {
	var op, field, service, schema, flow, concept, scope, typ string
	var tags []string

	cmd := &cobra.Command{
		Use:   "add <text>",
		Short: "Capture one memory",
		Long: `Capture one memory.

Scope decides storage and sharing, not subject. Service scope is
committed under <service>/api/memories: reviewable, and it reaches
everyone who clones that service's repo. Workspace scope is local
under <workspace>/memories: no git history unless the workspace
itself happens to be a git repo, and invisible to teammates who don't
share your machine. That asymmetry, not what the memory is about, is
the decision. A workspace-scoped memory may still concern a service.

Ask: would this still be true in a fresh environment with empty
databases? Yes means service. No means workspace. A memory that mixes
a lasting contract fact with local environment data belongs in
neither scope as written; split it into two memories instead.

Memory is a staging area, not the destination: once a note stabilises,
"sapien memory promote <id>" finds where it belongs in api/docs or the
contract, and "sapien memory rescope <id> --scope <scope>" moves it
without losing it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			mem := domain.Memory{
				Type:  domain.MemoryType(typ),
				Scope: domain.MemoryScope(scope),
				Subject: domain.Subject{
					Service: service, Operation: op, Field: field, Schema: schema,
					Flow: flow, Environment: app.EnvName, Concept: concept,
				},
				Tags:   tags,
				Source: domain.MemorySource{Kind: "user"},
				Status: domain.MemoryActive,
				Text:   args[0],
			}
			if mem.Scope == "" {
				mem.Scope = domain.ScopeWorkspace
			}
			if mem.Type == "" {
				mem.Type = domain.MemoryNote
			}

			created, err := eng.Memories().Create(cmd.Context(), mem)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(created)
			}
			app.Printer.Line("created %s", created.ID)
			printMemoryStorageHints(cmd.Context(), app, eng, created)
			return nil
		},
	}
	cmd.Flags().StringVar(&op, "op", "", "operation id this memory concerns")
	cmd.Flags().StringVar(&field, "field", "", "field path this memory concerns (needs --op or --schema)")
	cmd.Flags().StringVar(&service, "service", "", "service this memory concerns")
	cmd.Flags().StringVar(&schema, "schema", "", "schema (\"<service>.<Name>\") this memory concerns")
	cmd.Flags().StringVar(&flow, "flow", "", "flow id this memory concerns")
	cmd.Flags().StringVar(&concept, "concept", "", "concept this memory concerns")
	cmd.Flags().StringVar(&scope, "scope", "workspace", "personal|workspace|service|flow; see --help for the rule")
	cmd.Flags().StringVar(&typ, "type", "note", "semantic|behavioral|testing|invariant|environment|gotcha|note")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	// --env is deliberately not a local flag here: it would shadow the
	// persistent --env flag already declared on the root command (PLAN §21
	// lists "--env" for `memory add`'s subject.environment), so this reads
	// that same global flag (app.EnvName) instead of redeclaring it.
	return cmd
}

// printMemoryStorageHints prints, in human mode only, the situational
// nudges from the memory-scope feedback: where the memory actually landed,
// a warning when workspace scope has no git history to carry it, a rescope
// suggestion when a workspace-scoped memory names a service, up to three
// similar existing memories, and a standing reminder that `memory promote`
// is how a memory graduates out of the staging area. It never errors: a
// failed similar-memory lookup just means that hint is skipped.
func printMemoryStorageHints(ctx context.Context, app *App, eng engine.Engine, created *domain.Memory) {
	if created.Scope == domain.ScopePersonal {
		app.Printer.Line("stored: SQLite only")
	} else if created.FilePath != "" {
		app.Printer.Line("stored: %s", created.FilePath)
	}

	if created.Scope == domain.ScopeWorkspace {
		if ws, err := app.Workspace(); err == nil && ws != nil && !isGitRepo(ws.Dir) {
			app.Printer.Line("note: %s is not a git repo, so workspace memories live only on this machine", ws.Dir)
		}
		if created.Subject.Service != "" {
			app.Printer.Line("concerns %s; if it would hold in a fresh environment, run `sapien memory rescope %s --scope service`",
				created.Subject.Service, created.ID)
		}
	}

	q := domain.MemoryQuery{Text: created.Text, Limit: 3}
	if created.Subject.Service != "" {
		q.Service = created.Subject.Service
	}
	if similar, err := eng.Memories().Search(ctx, q); err == nil {
		shown := 0
		for _, r := range similar {
			if r.Memory.ID == created.ID {
				continue
			}
			app.Printer.Line("similar: %s (%s) %s", r.Memory.ID, formatScore(r.Score), firstLine(r.Memory.Text))
			app.Printer.Line("consider updating that one instead, or promoting it: `sapien memory promote %s`", r.Memory.ID)
			shown++
			if shown == 3 {
				break
			}
		}
	}

	app.Printer.Line("when this stabilises, `sapien memory promote %s` locates where it belongs", created.ID)
}

// isGitRepo reports whether dir looks like a git working tree: it has a
// .git entry, directory (a normal clone) or file (a worktree/submodule).
func isGitRepo(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// firstLine returns s's first line, trimmed and cut to 60 bytes, for a
// compact "similar:" preview.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.TrimSpace(s), 60)
}

// displayMemoryPath renders a memory's FilePath for human output, per PLAN
// §12: personal-scope memories have no file at all.
func displayMemoryPath(path string) string {
	if path == "" {
		return "SQLite only"
	}
	return path
}

func newMemoryListCmd(app *App) *cobra.Command {
	var op, service, scope, typ string
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			q := domain.MemoryQuery{
				Operation: op, Service: service,
				Scope: domain.MemoryScope(scope), Type: domain.MemoryType(typ),
				Limit: limit,
			}
			mems, err := eng.Memories().List(cmd.Context(), q)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(mems)
			}
			rows := make([][]string, 0, len(mems))
			for _, m := range mems {
				rows = append(rows, []string{
					m.ID, string(m.Type), string(m.Scope), tierColumn(m.Tier), shipStateColumn(m.Shipped),
					subjectString(m.Subject), truncate(m.Text, 60),
				})
			}
			app.Printer.Table([]string{"ID", "TYPE", "SCOPE", "TIER", "SHIPPED", "SUBJECT", "TEXT"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&op, "op", "", "filter by operation id")
	cmd.Flags().StringVar(&service, "service", "", "filter by service")
	cmd.Flags().StringVar(&scope, "scope", "", "filter by scope")
	cmd.Flags().StringVar(&typ, "type", "", "filter by type")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum results")
	return cmd
}

func newMemorySearchCmd(app *App) *cobra.Command {
	var op, service string

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search memories",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			q := domain.MemoryQuery{Text: args[0], Operation: op, Service: service}
			results, err := eng.Memories().Search(cmd.Context(), q)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(results)
			}
			rows := make([][]string, 0, len(results))
			for _, r := range results {
				rows = append(rows, []string{
					formatScore(r.Score), r.Memory.ID, string(r.Memory.Type),
					subjectString(r.Memory.Subject), truncate(r.Memory.Text, 60), strings.Join(r.Reasons, ","),
				})
			}
			app.Printer.Table([]string{"SCORE", "ID", "TYPE", "SUBJECT", "TEXT", "REASONS"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&op, "op", "", "filter by operation id")
	cmd.Flags().StringVar(&service, "service", "", "filter by service")
	return cmd
}

func newMemoryShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a memory as its stored Markdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			mem, err := eng.Memories().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(mem)
			}
			if mem.Tier != "" {
				app.Printer.Line("# tier: %s (%s)", tierWithShip(mem.Tier, mem.Shipped), mem.FilePath)
			}
			app.Printer.Line("%s", renderMemoryMarkdown(mem))
			return nil
		},
	}
}

func newMemoryRmCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.Memories().Delete(cmd.Context(), args[0]); err != nil {
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

func newMemoryPromoteCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "promote <id>",
		Short: "Locate where a memory's knowledge should be promoted (PLAN §26)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			target, err := eng.Memories().PromotionTarget(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(target)
			}
			printPromotionTarget(app.Printer, target)
			return nil
		},
	}
}

func newMemoryRescopeCmd(app *App) *cobra.Command {
	var scope, service string

	cmd := &cobra.Command{
		Use:   "rescope <id> --scope <scope>",
		Short: "Move a memory to another scope without losing it",
		Long: `Move a memory to another scope: it is the same fix as "rm" then "add",
minus the chance of losing the text in between.

Scope decides storage and sharing, not subject: service scope commits
the memory under <service>/api/memories, reviewable by anyone who
clones that repo; workspace scope keeps it local under
<workspace>/memories; personal scope keeps it in SQLite only, on this
machine. Rescoping never changes what the memory is about unless
--service is also given.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if scope == "" {
				return errs.New(errs.Invalid, "memory: rescope requires --scope").
					WithHint("pass --scope personal|workspace|service|flow")
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			mem, err := eng.Memories().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			updated := *mem
			oldPath := mem.FilePath
			updated.Scope = domain.MemoryScope(scope)
			if service != "" {
				updated.Subject.Service = service
			}
			if updated.Scope == domain.ScopeService && updated.Subject.Service == "" {
				return errs.New(errs.Invalid, "memory: service scope requires a service subject").
					WithHint("pass --service <name>, or give the memory a service subject first")
			}

			moved, err := eng.Memories().Update(cmd.Context(), updated)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{
					"id":       moved.ID,
					"old_path": oldPath,
					"new_path": moved.FilePath,
					"memory":   moved,
				})
			}
			app.Printer.Line("%s -> %s", displayMemoryPath(oldPath), displayMemoryPath(moved.FilePath))
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "personal|workspace|service|flow (required)")
	cmd.Flags().StringVar(&service, "service", "", "service subject to set; required for service scope if the memory has none")
	return cmd
}

// tierColumn renders a memory or example's Tier (PLAN §7b) for a TIER
// column: "local" (this machine), "team" (the workspace repo), "service"
// (the owning service's own repo), or "" for a personal-scope memory,
// which has no tier at all -- it lives in SQLite only.
func tierColumn(tier string) string {
	switch tier {
	case domain.TierLocal:
		return "local"
	case domain.TierWorkspace:
		return "team"
	case domain.TierService:
		return "service"
	default:
		return ""
	}
}

// tierWithShip renders tier the way `memory show` and `example show`'s
// leading "# tier:" comment does: the tier alone, or with the workspace
// tier's ship state appended ("team, not committed").
func tierWithShip(tier, shipped string) string {
	t := tierColumn(tier)
	if s := shipStateColumn(shipped); s != "" {
		t += ", " + s
	}
	return t
}

// newMemoryMoveCmd is `sapien memory move <id> --to local|team`: places a
// workspace-scope memory's file in another tier, keeping its id and scope.
// "team" (not "workspace") to match the TIER column and flow's own
// promote/rescope vocabulary; it maps to domain.TierWorkspace underneath.
func newMemoryMoveCmd(app *App) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "move <id> --to local|team",
		Short: "Move a workspace-scope memory's file to another tier",
		Long: `Move a workspace-scope memory's file between tiers, keeping its id and
scope: local (` + "`<workspace>/local/memories`" + `, this machine only, never
committed) or team (` + "`<workspace>/memories`" + `, the team's repo). Refused for
personal and service scope, whose home is their scope, not a tier.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tier, err := tierFlag("memory move", to)
			if err != nil {
				return err
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			mem, err := eng.Memories().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			oldPath := mem.FilePath

			moved, err := eng.Memories().Move(cmd.Context(), args[0], tier)
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]any{
					"id":       moved.ID,
					"tier":     moved.Tier,
					"old_path": oldPath,
					"new_path": moved.FilePath,
					"memory":   moved,
				})
			}
			app.Printer.Line("%s -> %s", displayMemoryPath(oldPath), displayMemoryPath(moved.FilePath))
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "local|team (required)")
	return cmd
}

// tierFlag turns `--to local|team` into the domain.Tier* constant
// Memories().Move and Examples().Move take, for `memory move` and
// `example move`; cmdName names the caller for the error message.
func tierFlag(cmdName, to string) (string, error) {
	switch to {
	case domain.TierLocal:
		return domain.TierLocal, nil
	case domain.TierWorkspace, "team":
		return domain.TierWorkspace, nil
	case "":
		return "", errs.New(errs.Invalid, "%s: requires --to", cmdName).
			WithHint("pass --to local|team")
	default:
		return "", errs.New(errs.Invalid, "unknown tier %q", to).
			WithHint("--to is local (this machine) or team (the workspace repo)")
	}
}

// newMemoryCommitCmd is `sapien memory commit <id> [-m <message>]` or
// `sapien memory commit --all`: records a workspace-tier memory's file in
// the workspace repository with one commit, never a push.
func newMemoryCommitCmd(app *App) *cobra.Command {
	var message string
	var all bool
	cmd := &cobra.Command{
		Use:   "commit <id> [-m <message>]",
		Short: "Commit a workspace-tier memory's file in the workspace repository",
		Long: `Record a workspace-tier memory's file with one commit; never pushes.
Refused when the memory is not at the team tier, the workspace is not a git
repository, or the file already has nothing to commit.

--all commits every team-tier memory that is not committed or modified
(untracked, or tracked with an uncommitted edit), one commit per memory,
using the same message for each (or each memory's own default when -m is
omitted); a memory already committed is left alone.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case all && len(args) == 1:
				return errs.New(errs.Invalid, "memory commit: pass a memory id or --all, not both")
			case !all && len(args) != 1:
				return errs.New(errs.Invalid, "memory commit: requires a memory id, or --all")
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if !all {
				return commitOneMemory(app, cmd.Context(), eng, args[0], message)
			}
			return commitAllMemories(app, cmd.Context(), eng, message)
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message; default depends on whether the file was ever added to git")
	cmd.Flags().BoolVar(&all, "all", false, "commit every team-tier memory that is not committed or modified")
	return cmd
}

func commitOneMemory(app *App, ctx context.Context, eng engine.Engine, id, message string) error {
	mem, err := eng.Memories().Commit(ctx, id, message)
	if err != nil {
		return err
	}
	if app.Printer.IsJSON() {
		return app.Printer.JSON(mem)
	}
	app.Printer.Line("committed %s (%s)", displayMemoryPath(mem.FilePath), shipStateColumn(mem.Shipped))
	return nil
}

// commitAllMemories is `memory commit --all`: every team-tier memory whose
// Shipped state says it has something to commit (untracked or modified),
// committed one at a time with the same message (id order, so output is
// stable).
func commitAllMemories(app *App, ctx context.Context, eng engine.Engine, message string) error {
	mems, err := eng.Memories().List(ctx, domain.MemoryQuery{Scope: domain.ScopeWorkspace})
	if err != nil {
		return err
	}
	var ids []string
	for _, m := range mems {
		if m.Tier != domain.TierWorkspace {
			continue
		}
		if m.Shipped == domain.ShipUntracked || m.Shipped == domain.ShipModified {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)

	if len(ids) == 0 {
		if app.Printer.IsJSON() {
			return app.Printer.JSON([]domain.Memory{})
		}
		app.Printer.Line("nothing to commit")
		return nil
	}

	results := make([]*domain.Memory, 0, len(ids))
	for _, id := range ids {
		mem, err := eng.Memories().Commit(ctx, id, message)
		if err != nil {
			return err
		}
		results = append(results, mem)
	}
	if app.Printer.IsJSON() {
		return app.Printer.JSON(results)
	}
	for _, mem := range results {
		app.Printer.Line("committed %s (%s)", displayMemoryPath(mem.FilePath), shipStateColumn(mem.Shipped))
	}
	return nil
}

func printPromotionTarget(p *Printer, t *engine.PromotionTarget) {
	loc := t.File
	if t.Line > 0 {
		loc += fmt.Sprintf(":%d", t.Line)
	}
	p.Line("kind: %s", t.Kind)
	p.Line("location: %s", loc)
	if t.Pointer != "" {
		p.Line("pointer: %s", t.Pointer)
	}
	if t.Section != "" {
		p.Line("section: %s", t.Section)
	}
	if t.Current != "" {
		p.Line("")
		p.Line("current:")
		p.Line("%s", t.Current)
	}
	if t.Suggested != "" {
		p.Line("")
		p.Line("suggested:")
		p.Line("%s", t.Suggested)
	}
}

func newMemoryReindexCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the memory index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.Memories().Reindex(cmd.Context()); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]bool{"reindexed": true})
			}
			app.Printer.Line("reindexed memories")
			return nil
		},
	}
}

// subjectString renders a memory Subject as a single compact string for
// table rows, in priority order (most-specific first).
func subjectString(s domain.Subject) string {
	switch {
	case s.Operation != "":
		return s.Operation
	case s.Field != "":
		return s.Field
	case s.Schema != "":
		return s.Schema
	case s.Flow != "":
		return s.Flow
	case s.Service != "":
		return s.Service
	case s.Concept != "":
		return s.Concept
	case s.Run != "":
		return s.Run
	default:
		return ""
	}
}

// truncate returns s cut to at most n bytes, for TEXT table columns.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// renderMemoryMarkdown reconstructs a memory's Markdown-with-front-matter
// form (PLAN §10) from its struct fields, for `memory show`'s human output.
// The engine's MemoryAPI doesn't hand back raw file bytes (a personal-scope
// memory has none — it lives only in SQLite), so this renders the same
// front matter a workspace/service-scope memory would have on disk.
func renderMemoryMarkdown(m *domain.Memory) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", m.ID)
	if m.Type != "" {
		fmt.Fprintf(&b, "type: %s\n", m.Type)
	}
	fmt.Fprintf(&b, "scope: %s\n", m.Scope)
	if !m.Subject.IsZero() {
		b.WriteString("subject:\n")
		writeSubjectYAML(&b, m.Subject)
	}
	if len(m.Tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(m.Tags, ", "))
	}
	fmt.Fprintf(&b, "source: { kind: %s }\n", m.Source.Kind)
	if !m.Created.IsZero() {
		fmt.Fprintf(&b, "created: %s\n", m.Created.UTC().Format(time.RFC3339))
	}
	if !m.Updated.IsZero() {
		fmt.Fprintf(&b, "updated: %s\n", m.Updated.UTC().Format(time.RFC3339))
	}
	if m.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", m.Status)
	}
	b.WriteString("---\n\n")
	b.WriteString(m.Text)
	return b.String()
}

func writeSubjectYAML(b *strings.Builder, s domain.Subject) {
	if s.Service != "" {
		fmt.Fprintf(b, "  service: %s\n", s.Service)
	}
	if s.Operation != "" {
		fmt.Fprintf(b, "  operation: %s\n", s.Operation)
	}
	if s.Field != "" {
		fmt.Fprintf(b, "  field: %s\n", s.Field)
	}
	if s.Schema != "" {
		fmt.Fprintf(b, "  schema: %s\n", s.Schema)
	}
	if s.Flow != "" {
		fmt.Fprintf(b, "  flow: %s\n", s.Flow)
	}
	if s.Step != "" {
		fmt.Fprintf(b, "  step: %s\n", s.Step)
	}
	if s.Run != "" {
		fmt.Fprintf(b, "  run: %s\n", s.Run)
	}
	if s.Environment != "" {
		fmt.Fprintf(b, "  environment: %s\n", s.Environment)
	}
	if s.Concept != "" {
		fmt.Fprintf(b, "  concept: %s\n", s.Concept)
	}
}
