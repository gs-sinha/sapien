package cli

import (
	"bufio"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/friction"
)

func init() { Register(newFrictionCmd) }

func newFrictionCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "friction",
		Short: "Review and send friction reports agents filed with report_friction",
		Long: `Review and send friction reports agents filed with report_friction.

An agent that hits friction over MCP -- a tool that returned the wrong
shape, a missing capability, a doc that misled it -- can queue a report
with the report_friction tool. It is never sent anywhere by the agent:
it sits on disk as a plain Markdown file until a human reviews it here
and, on "send", posts it as a GitHub Discussion through the gh CLI.
Sapien stores no credentials for this, the same as it does for git:
gh already holds the user's own auth.`,
	}
	cmd.AddCommand(
		newFrictionListCmd(app),
		newFrictionShowCmd(app),
		newFrictionSendCmd(app),
		newFrictionDropCmd(app),
		newFrictionAddCmd(app),
	)
	return cmd
}

// frictionStore opens the friction store. There is deliberately no
// workspace threading here: friction reports are per-machine, not
// per-workspace (an agent may hit friction before any workspace resolves),
// so the only override is friction.New's own SAPIEN_FRICTION_DIR
// environment variable, which the report_friction MCP tool honours too --
// see internal/friction/store.go's frictionDirEnv doc comment.
func frictionStore() *friction.Store {
	return friction.New("")
}

// resolveFrictionConfig loads engine-level config for `friction send`'s
// defaults. A workspace is used when one resolves from the current
// directory or --workspace, but friction must work with no workspace at
// all (an agent files a report from wherever it happens to be), so a
// resolution failure is swallowed here and config.Load(nil) -- defaults
// plus the user-level file -- is used instead. config.Load already treats
// a nil workspace exactly that way, so this is the same call either way.
func resolveFrictionConfig(app *App) (config.Config, error) {
	var ws *domain.Workspace
	if w, err := app.Workspace(); err == nil {
		ws = w
	}
	return config.Load(ws)
}

func newFrictionListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List queued friction reports",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			reports, err := frictionStore().List(cmd.Context())
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(reports)
			}
			if len(reports) == 0 {
				app.Printer.Line("no friction reports; agents file them with the report_friction MCP tool")
				return nil
			}
			rows := make([][]string, 0, len(reports))
			for _, r := range reports {
				rows = append(rows, []string{
					r.ID, r.Status, string(r.Category), truncate(r.Title, 60),
					r.Workspace, r.Created.UTC().Format(time.RFC3339),
				})
			}
			app.Printer.Table([]string{"ID", "STATUS", "CATEGORY", "TITLE", "WORKSPACE", "CREATED"}, rows)
			return nil
		},
	}
}

func newFrictionShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a friction report's fields, then its body exactly as it would be posted",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := frictionStore().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(r)
			}
			printFrictionFields(app.Printer, r)
			title, body := friction.Render(*r, friction.Host{OS: runtime.GOOS})
			app.Printer.Line("")
			app.Printer.Line("title: %s", title)
			app.Printer.Line("")
			app.Printer.Line("%s", body)
			return nil
		},
	}
}

// printFrictionFields prints a report's front-matter-shaped fields, human
// mode only, for `friction show`.
func printFrictionFields(p *Printer, r *friction.Report) {
	p.Line("id: %s", r.ID)
	p.Line("status: %s", r.Status)
	p.Line("category: %s", r.Category)
	if r.Tool != "" {
		p.Line("tool: %s", r.Tool)
	}
	if r.Workspace != "" {
		p.Line("workspace: %s", r.Workspace)
	}
	if r.Client != "" {
		p.Line("client: %s", r.Client)
	}
	if r.Version != "" {
		p.Line("version: %s", r.Version)
	}
	p.Line("created: %s", r.Created.UTC().Format(time.RFC3339))
	if r.SentURL != "" {
		p.Line("sent_url: %s", r.SentURL)
	}
	if !r.SentAt.IsZero() {
		p.Line("sent_at: %s", r.SentAt.UTC().Format(time.RFC3339))
	}
}

func newFrictionSendCmd(app *App) *cobra.Command {
	var all, yes bool
	var repoFlag, categoryFlag string

	cmd := &cobra.Command{
		Use:   "send <id>",
		Short: "Post one (or, with --all, every pending) friction report as a GitHub Discussion",
		Long: `Post a queued friction report as a GitHub Discussion via the gh CLI.

For each report this prints the exact title and body that would be
posted, then asks "Post to <repo> as a GitHub Discussion? [y/N]" before
doing anything -- nothing is posted without that confirmation (or
--yes). --repo and --category override the friction.repo/friction.category
config for this run only.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return errs.New(errs.Invalid, "friction send: pass exactly one of a report id or --all").
					WithHint("sapien friction send <id>, or sapien friction send --all")
			}

			cfg, err := resolveFrictionConfig(app)
			if err != nil {
				return err
			}
			repo := cfg.Friction.Repo
			if repoFlag != "" {
				repo = repoFlag
			}
			category := cfg.Friction.Category
			if categoryFlag != "" {
				category = categoryFlag
			}

			s := frictionStore()
			var targets []friction.Report
			if all {
				reports, err := s.List(cmd.Context())
				if err != nil {
					return err
				}
				for _, r := range reports {
					if r.Status == friction.StatusPending {
						targets = append(targets, r)
					}
				}
			} else {
				r, err := s.Get(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				targets = append(targets, *r)
			}

			if len(targets) == 0 {
				app.Printer.Line("no pending friction reports")
				return nil
			}

			if !yes && !isTerminal(os.Stdin) {
				return errs.New(errs.Invalid, "friction send: stdin is not a terminal").
					WithHint("pass --yes to send without an interactive confirmation")
			}

			pub := friction.GitHubDiscussions{Repo: repo, Category: category}
			reader := bufio.NewReader(os.Stdin)

			type sendResult struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				URL    string `json:"url,omitempty"`
			}
			var results []sendResult

			for _, r := range targets {
				title, body := friction.Render(r, friction.Host{OS: runtime.GOOS})
				app.Printer.Line("--- %s ---", r.ID)
				app.Printer.Line("%s", title)
				app.Printer.Line("")
				app.Printer.Line("%s", body)

				confirmed := yes
				if !confirmed {
					app.Printer.Line("Post to %s as a GitHub Discussion? [y/N]", repo)
					line, _ := reader.ReadString('\n')
					line = strings.ToLower(strings.TrimSpace(line))
					confirmed = line == "y" || line == "yes"
				}
				if !confirmed {
					app.Printer.Line("skipped %s", r.ID)
					results = append(results, sendResult{ID: r.ID, Status: "skipped"})
					continue
				}

				url, err := pub.Publish(cmd.Context(), title, body)
				if err != nil {
					return err
				}
				sent, err := s.MarkSent(cmd.Context(), r.ID, url)
				if err != nil {
					return err
				}
				app.Printer.Line("sent %s -> %s", sent.ID, sent.SentURL)
				results = append(results, sendResult{ID: sent.ID, Status: sent.Status, URL: sent.SentURL})
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(results)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "send every pending report")
	cmd.Flags().BoolVar(&yes, "yes", false, "post without an interactive confirmation")
	cmd.Flags().StringVar(&repoFlag, "repo", "", "owner/name to post to (default: friction.repo config, "+
		"itself defaulting to gs-sinha/sapien)")
	cmd.Flags().StringVar(&categoryFlag, "category", "", "discussion category (default: friction.category config, "+
		"itself defaulting to General)")
	return cmd
}

func newFrictionDropCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "drop <id>",
		Short: "Delete a friction report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := frictionStore().Drop(cmd.Context(), args[0]); err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"dropped": args[0]})
			}
			app.Printer.Line("dropped %s", args[0])
			return nil
		},
	}
}

func newFrictionAddCmd(app *App) *cobra.Command {
	var happened, tried, wouldHelp, tool, category string

	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "File a friction report from the terminal (the human's own way in; agents use report_friction)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceName := ""
			if ws, err := app.Workspace(); err == nil && ws != nil {
				workspaceName = ws.Name
			}

			created, err := frictionStore().Create(cmd.Context(), friction.Report{
				Title: args[0], Happened: happened, Tried: tried, WouldHelp: wouldHelp,
				Tool: tool, Category: friction.Category(category),
				Workspace: workspaceName, Client: "cli", Version: Version,
			})
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(created)
			}
			app.Printer.Line("queued %s", created.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&happened, "happened", "", "what happened instead of what you expected (required)")
	cmd.Flags().StringVar(&tried, "tried", "", "what you were trying to do")
	cmd.Flags().StringVar(&wouldHelp, "would-help", "", "the change that would have removed the friction")
	cmd.Flags().StringVar(&tool, "tool", "", "the Sapien tool or command involved")
	cmd.Flags().StringVar(&category, "category", "", "bug|idea|docs|missing (default bug)")
	_ = cmd.MarkFlagRequired("happened")
	return cmd
}
