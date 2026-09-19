package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

func init() { Register(newWorkspaceCmd) }

// workspaceRow is one row of `sapien workspace list`, and its --json shape.
type workspaceRow struct {
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Current  bool   `json:"current"`
	Default  bool   `json:"default"`
	Services int    `json:"services,omitempty"`
	Error    string `json:"error,omitempty"`
}

// newWorkspaceCmd is `sapien workspace`: the registry of workspaces this
// machine knows about, and which one commands use by default.
//
// Selecting a workspace for a single command has always worked
// (--workspace, $SAPIEN_WORKSPACE, or just running inside one); these verbs
// exist because neither the UI's picker nor MCP's switch_workspace can offer
// a choice the machine has never written down.
func newWorkspaceCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "List, switch between, and register workspaces",
	}
	cmd.AddCommand(
		newWorkspaceListCmd(app),
		newWorkspaceCurrentCmd(app),
		newWorkspaceUseCmd(app),
		newWorkspaceAddCmd(app),
		newWorkspaceForgetCmd(app),
		newWorkspaceCloseCmd(app),
		newWorkspaceStatusCmd(app),
		newWorkspacePullCmd(app),
		newWorkspaceSyncCmd(app),
		newWorkspacePushCmd(app),
		newWorkspaceChangesCmd(app),
		newWorkspaceCommitCmd(app),
	)
	return cmd
}

func newWorkspaceListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every registered workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs, err := config.KnownWorkspaces()
			if err != nil {
				return err
			}
			def, err := config.DefaultWorkspace()
			if err != nil {
				return err
			}
			// The workspace this invocation would actually use, which is
			// not necessarily the default one: running inside a workspace
			// wins over it.
			var currentDir string
			if ws, err := app.Workspace(); err == nil {
				currentDir = ws.Dir
			}
			// The workspace you are standing in belongs in the list whether
			// or not it was ever registered -- otherwise `workspace list`
			// run inside a workspace fails to mention it, which reads as a
			// bug rather than as "you never added this one".
			// Matched by directory, not by path string: run from a cwd that
			// spells the path differently (lowercase `desktop` on macOS, a
			// symlinked parent) and a string compare adds the workspace you
			// are already standing in a second time.
			if currentDir != "" {
				known := false
				for _, dir := range dirs {
					if workspace.SameDir(dir, currentDir) {
						known = true
						break
					}
				}
				if !known {
					dirs = append([]string{currentDir}, dirs...)
				}
			}

			rows := make([]workspaceRow, 0, len(dirs))
			for _, dir := range dirs {
				row := workspaceRow{Dir: dir, Current: workspace.SameDir(dir, currentDir), Default: workspace.SameDir(dir, def)}
				ws, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
				if err != nil {
					row.Error = err.Error()
					row.Name = filepath.Base(dir)
				} else {
					row.Name = ws.Name
					row.Services = len(ws.Services)
				}
				rows = append(rows, row)
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(rows)
			}
			if len(rows) == 0 {
				app.Printer.Line("No workspaces registered. Run `sapien init` in a new directory, or `sapien workspace add <dir>`.")
				return nil
			}
			for _, row := range rows {
				marker := " "
				if row.Current {
					marker = "*"
				}
				suffix := ""
				switch {
				case row.Error != "":
					suffix = "  (" + row.Error + ")"
				case row.Default:
					suffix = "  (default)"
				}
				app.Printer.Line("%s %-24s %s%s", marker, row.Name, row.Dir, suffix)
			}
			return nil
		},
	}
}

func newWorkspaceCurrentCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the workspace this directory resolves to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(workspaceRow{Name: ws.Name, Dir: ws.Dir, Current: true, Services: len(ws.Services)})
			}
			app.Printer.Line("%s  %s", ws.Name, ws.Dir)
			return nil
		},
	}
}

func newWorkspaceUseCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "use <dir|name>",
		Short: "Make a workspace the default for commands outside any workspace",
		Long: "Sets default_workspace in the user config. It is the fallback, not an override: " +
			"a command run inside a workspace, or given --workspace/$SAPIEN_WORKSPACE, still uses that one.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceArg(args[0])
			if err != nil {
				return err
			}
			if err := config.AddWorkspace(ws.Dir); err != nil {
				return err
			}
			if err := config.SetDefaultWorkspace(ws.Dir); err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(workspaceRow{Name: ws.Name, Dir: ws.Dir, Default: true, Services: len(ws.Services)})
			}
			app.Printer.Line("default workspace is now %s (%s)", ws.Name, ws.Dir)
			return nil
		},
	}
}

func newWorkspaceAddCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "add <dir>",
		Short: "Register an existing workspace so it appears in pickers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceArg(args[0])
			if err != nil {
				return err
			}
			if err := config.AddWorkspace(ws.Dir); err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(workspaceRow{Name: ws.Name, Dir: ws.Dir, Services: len(ws.Services)})
			}
			app.Printer.Line("registered %s (%s)", ws.Name, ws.Dir)
			return nil
		},
	}
}

func newWorkspaceForgetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "forget <dir>",
		Short: "Unregister a workspace (the directory itself is untouched)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return errs.Wrap(errs.Internal, err, "resolving %q", args[0])
			}
			if err := config.RemoveWorkspace(abs); err != nil {
				return err
			}
			app.Printer.Line("forgot %s (the directory is untouched)", abs)
			// A forgotten workspace should also stop being served: close
			// it on the running daemon, best effort. Being unregistered is
			// what keeps it closed afterwards.
			if closed, err := closeOnDaemon(cmd.Context(), app, abs); err != nil {
				app.Printer.Line("%s", app.Printer.Dim("not closed on the daemon: "+err.Error()))
			} else if closed {
				app.Printer.Line("closed it on the running daemon")
			}
			return nil
		},
	}
}

// newWorkspaceStatusCmd is `sapien workspace status`: the workspace's own
// git repository (PLAN §7b) -- the team's shared copy of the workspace
// tier -- read from refs already on disk, no network.
func newWorkspaceStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the workspace's own git repository status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			st, err := eng.Repo().Status(cmd.Context())
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(st)
			}
			printRepoStatus(app.Printer, st)
			return nil
		},
	}
}

// printRepoStatus renders one human-readable block for `workspace status`:
// whether the workspace is in git at all; its branch and upstream; each of
// "N commits from the team waiting" (Behind), "you have N unpushed
// commits" (Ahead), and "N uncommitted files" (Dirty) that applies -- all
// three can apply at once, so each gets its own line rather than picking
// one; when the workspace was last fetched, or "never fetched"; and the
// last fetch's error, if any.
func printRepoStatus(p *Printer, st *domain.RepoStatus) {
	if !st.InGit {
		p.Line("not a git repository")
		return
	}
	branch := st.Branch
	if branch == "" {
		branch = "(detached)"
	}
	if st.Upstream != "" {
		p.Line("branch %s, tracking %s", branch, st.Upstream)
	} else {
		p.Line("branch %s, no upstream", branch)
	}

	noted := false
	if st.Behind > 0 {
		p.Line("%d commits from the team waiting", st.Behind)
		noted = true
	}
	if st.Ahead > 0 {
		p.Line("you have %d unpushed commits (sapien workspace push)", st.Ahead)
		noted = true
	}
	if st.Dirty > 0 {
		p.Line("%d uncommitted files", st.Dirty)
		noted = true
	}
	if !noted {
		p.Line("up to date, nothing uncommitted")
	}

	if st.FetchedAt.IsZero() {
		p.Line("never fetched")
	} else {
		p.Line("last fetched %s", relativeTime(st.FetchedAt))
	}
	if st.FetchError != "" {
		p.Line("%s", p.Dim("fetch error: "+st.FetchError))
	}
}

// relativeTime renders t as a short "ago" duration for a human status
// line.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// newWorkspacePullCmd is `sapien workspace pull`: fast-forward the
// workspace repository onto its upstream. The engine refuses (errs.
// Conflict) a dirty tree, a missing upstream, or a diverged branch; that
// error renders the way every CLI error does, through the root command's
// own error formatting.
func newWorkspacePullCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Fast-forward the workspace repository onto its upstream",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			st, err := eng.Repo().Pull(cmd.Context())
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(st)
			}
			app.Printer.Line("%s", repoPullLine(st))
			return nil
		},
	}
}

// newWorkspaceSyncCmd is `sapien workspace sync`: what "sync everything"
// does for the repository alone -- fetch, then pull when the tree is
// clean and behind. Unlike pull, this never errors just because a pull
// was not possible; it says why instead.
func newWorkspaceSyncCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Fetch the workspace repository, pulling fast-forward when the tree is clean",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			st, err := eng.Repo().Sync(cmd.Context())
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(st)
			}
			app.Printer.Line("%s", repoPullLine(st))
			return nil
		},
	}
}

// newWorkspacePushCmd is `sapien workspace push`: send the workspace
// repository's unpushed commits to its upstream. The engine refuses
// (errs.Conflict) a branch that is behind its upstream, since a pull must
// come first; that error renders the way every CLI error does, through
// the root command's own error formatting. Never a force push, never a
// service repository (PLAN §7b) -- this is the one place Sapien pushes at
// all, and only because a human ran this command.
func newWorkspacePushCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Push the workspace repository's unpushed commits to its upstream",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			st, err := eng.Repo().Push(cmd.Context())
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(st)
			}
			app.Printer.Line("%s", repoPushLine(st))
			return nil
		},
	}
}

// repoPushLine renders the outcome of a Repo().Push call: "pushed N
// commits", or "nothing to push" when Ahead was already zero. A behind
// branch never reaches here: the engine refuses with errs.Conflict before
// returning a status, which the CLI's normal error formatting reports
// instead.
func repoPushLine(st *domain.RepoStatus) string {
	if st.Pushed {
		return fmt.Sprintf("pushed %d commits", st.PushedCount)
	}
	return "nothing to push"
}

// repoPullLine renders the outcome of a Repo().Pull or Repo().Sync call:
// "pulled N commits", "not pulled: <skipped>" (Sync only -- Pull refuses
// with an error instead of ever setting Skipped), or "already current".
// Shared by `workspace pull`, `workspace sync`, and `service sync`'s
// team-repo line.
func repoPullLine(st *domain.RepoStatus) string {
	switch {
	case st.Pulled:
		return fmt.Sprintf("pulled %d commits", st.PulledCount)
	case st.Skipped != "":
		return fmt.Sprintf("not pulled: %s", st.Skipped)
	default:
		return "already current"
	}
}

// resolveWorkspaceArg turns a CLI argument into a loaded workspace. A path
// is used directly; anything else is matched against the registered
// workspaces by name, so `sapien workspace use platform` works without
// typing the path the picker already knows.
func resolveWorkspaceArg(arg string) (*domain.Workspace, error) {
	abs, err := filepath.Abs(arg)
	if err == nil {
		if ws, loadErr := workspace.Load(filepath.Join(abs, domain.WorkspaceFileName)); loadErr == nil {
			return ws, nil
		}
	}

	dirs, err := config.KnownWorkspaces()
	if err != nil {
		return nil, err
	}
	var matches []*domain.Workspace
	for _, dir := range dirs {
		ws, loadErr := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
		if loadErr == nil && ws.Name == arg {
			matches = append(matches, ws)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, errs.New(errs.WorkspaceNotFound, "no workspace at %q, and none registered under that name", arg).
			WithHint("`sapien workspace list` shows the registered ones")
	default:
		dirsList := make([]string, 0, len(matches))
		for _, m := range matches {
			dirsList = append(dirsList, m.Dir)
		}
		return nil, errs.New(errs.Invalid, "%d registered workspaces are named %q", len(matches), arg).
			WithHint(fmt.Sprintf("name one by path: %v", dirsList))
	}
}

// newWorkspaceCloseCmd is `sapien workspace close <dir|name>`: close a
// workspace's engine on the running daemon without unregistering it. The
// next request naming it reopens it; `forget` is the durable form.
func newWorkspaceCloseCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "close <dir|name>",
		Short: "Close a workspace on the running daemon (it stays registered)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceArg(args[0])
			if err != nil {
				return err
			}
			dir := ws.Dir
			closed, err := closeOnDaemon(cmd.Context(), app, dir)
			if err != nil {
				return err
			}
			if !closed {
				app.Printer.Line("no daemon is running; nothing to close")
				return nil
			}
			app.Printer.Line("closed %s on the daemon", dir)
			return nil
		},
	}
}

// closeOnDaemon asks the daemon serving this invocation's workspace (the
// default one, or --workspace) to close dir. It reports false with no
// error when no daemon is running or SAPIEN_NO_DAEMON is set, since there
// is then nothing holding the workspace open.
func closeOnDaemon(ctx context.Context, app *App, dir string) (bool, error) {
	if os.Getenv("SAPIEN_NO_DAEMON") != "" {
		return false, nil
	}
	ws, err := app.Workspace()
	if err != nil {
		return false, nil
	}
	info, err := daemon.Find(ctx, ws, Version)
	if err != nil || info == nil { // Find reports no daemon as (nil, nil)
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("http://127.0.0.1:%d/v1/workspaces?dir=%s", info.Port, url.QueryEscape(dir)), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, errs.Wrap(errs.DaemonUnavailable, err, "closing %s on the daemon", dir)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return true, nil
	}
	var envelope struct {
		Error *errs.Error `json:"error"`
	}
	if json.NewDecoder(resp.Body).Decode(&envelope) == nil && envelope.Error != nil {
		return false, envelope.Error
	}
	return false, errs.New(errs.Internal, "closing %s on the daemon: HTTP %d", dir, resp.StatusCode)
}

// newWorkspaceChangesCmd is `sapien workspace changes` (PLAN §34f item 1):
// the CLI's view of the Changes page -- every changed file the workspace
// repository (and any bound local service checkout) knows about.
func newWorkspaceChangesCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "changes",
		Short: "Show every changed file the workspace repository (and bound service checkouts) knows about",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			out, err := eng.Repo().Changes(cmd.Context())
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(out)
			}
			printRepoChanges(app.Printer, out)
			return nil
		},
	}
}

// repoKindOrder is the group order printRepoChanges renders Files in.
var repoKindOrder = []string{
	domain.RepoKindFlow, domain.RepoKindMemory, domain.RepoKindExample,
	domain.RepoKindEnvironment, domain.RepoKindWorkspace, domain.RepoKindOther,
}

// printRepoChanges renders `workspace changes`' grouped, tree-ish plain
// output: every file grouped by kind (flow, memory, example, environment,
// workspace, other; PLAN §34f item 1's rollup, folder -> repository, one
// level flatter than the UI's tree since a terminal has no fold state),
// then one entry per registered service.
func printRepoChanges(p *Printer, out *domain.RepoChanges) {
	if !out.Status.InGit {
		p.Line("not a git repository")
		return
	}
	if len(out.Files) == 0 {
		p.Line("nothing changed")
	} else {
		byKind := map[string][]domain.RepoFileChange{}
		for _, f := range out.Files {
			byKind[f.Kind] = append(byKind[f.Kind], f)
		}
		for _, kind := range repoKindOrder {
			files := byKind[kind]
			if len(files) == 0 {
				continue
			}
			p.Line("%s:", repoKindLabel(kind))
			for _, f := range files {
				p.Line("  %s", repoFileChangeLine(p, f))
			}
		}
	}

	for _, svc := range out.Services {
		switch svc.Mode {
		case domain.BindingLocal:
			line := fmt.Sprintf("service %s: local %s", svc.Name, svc.Path)
			if svc.Branch != "" {
				line += " (branch " + svc.Branch + ")"
			}
			if svc.Dirty {
				line += ", dirty"
			}
			p.Line("%s", line)
			for _, f := range svc.Files {
				p.Line("  %s  %s", repoChangeStateLabel(f.State), f.Path)
			}
		case domain.BindingTeam:
			ref := svc.Ref
			if ref == "" {
				ref = "default"
			}
			p.Line("service %s: team @ %s", svc.Name, ref)
		}
	}
}

// repoFileChangeLine renders one Files entry: state, path (and, for a
// rename, the path it came from), and a dimmed title when the catalog
// recognized it.
func repoFileChangeLine(p *Printer, f domain.RepoFileChange) string {
	line := repoChangeStateLabel(f.State) + "  " + f.Path
	if f.OldPath != "" {
		line += " (was " + f.OldPath + ")"
	}
	if f.Title != "" {
		line += "  " + p.Dim(f.Title)
	}
	return line
}

func repoKindLabel(kind string) string {
	switch kind {
	case domain.RepoKindFlow:
		return "flows"
	case domain.RepoKindMemory:
		return "memories"
	case domain.RepoKindExample:
		return "examples"
	case domain.RepoKindEnvironment:
		return "environments"
	case domain.RepoKindWorkspace:
		return "workspace"
	default:
		return "other"
	}
}

func repoChangeStateLabel(state string) string {
	switch state {
	case domain.ChangeUntracked:
		return "untracked"
	case domain.ChangeModified:
		return "modified"
	case domain.ChangeDeleted:
		return "deleted"
	case domain.ChangeRenamed:
		return "renamed"
	case domain.ChangeConflicted:
		return "conflicted"
	case domain.ChangeUnpushed:
		return "unpushed"
	default:
		return state
	}
}

// newWorkspaceCommitCmd is `sapien workspace commit -m <msg> [--all |
// <paths>...]` (PLAN §34f item 1): the standalone commit for the workspace
// repository, covering every file in it -- including sapien.workspace.yaml,
// environments/ and .gitignore, which no per-kind (flow/memory/example)
// commit reaches. Never pushes, never amends, never passes --no-verify.
func newWorkspaceCommitCmd(app *App) *cobra.Command {
	var message string
	var all bool
	cmd := &cobra.Command{
		Use:   "commit [-m <msg>] [--all | <paths>...]",
		Short: "Commit paths in the workspace repository with one commit; never pushes",
		Long: `Commit stages and commits exactly the given paths -- or, with --all, every
untracked, modified, deleted or renamed file "workspace changes" reports --
in the workspace repository, with one commit. Never pushes, never amends,
never passes --no-verify.

Paths are resolved against the current directory, like any other path a
shell command takes; a path outside the workspace repository, or one git
ignores, is refused.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case all && len(args) > 0:
				return errs.New(errs.Invalid, "workspace commit: pass paths or --all, not both")
			case !all && len(args) == 0:
				return errs.New(errs.Invalid, "workspace commit: requires at least one path, or --all")
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			status, err := eng.Repo().Status(cmd.Context())
			if err != nil {
				return err
			}
			if !status.InGit {
				return errs.New(errs.Invalid, "workspace is not a git repository")
			}

			var paths []string
			if all {
				changes, err := eng.Repo().Changes(cmd.Context())
				if err != nil {
					return err
				}
				for _, f := range changes.Files {
					switch f.State {
					case domain.ChangeUntracked, domain.ChangeModified, domain.ChangeDeleted, domain.ChangeRenamed:
						paths = append(paths, f.Path)
					}
				}
				if len(paths) == 0 {
					if app.Printer.IsJSON() {
						return app.Printer.JSON(domain.RepoCommitResult{Status: *status})
					}
					app.Printer.Line("nothing to commit")
					return nil
				}
			} else {
				for _, a := range args {
					rel, err := repoRelativePath(status.Root, a)
					if err != nil {
						return err
					}
					paths = append(paths, rel)
				}
			}

			out, err := eng.Repo().Commit(cmd.Context(), paths, message)
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(out)
			}
			app.Printer.Line("committed %s (%d file(s))", out.Commit[:min(len(out.Commit), 12)], len(out.Committed))
			for _, p := range out.Committed {
				app.Printer.Line("  %s", p)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message (required)")
	cmd.Flags().BoolVar(&all, "all", false, "commit every untracked, modified, deleted or renamed file `workspace changes` reports")
	return cmd
}

// repoRelativePath resolves arg (a path typed relative to the current
// working directory, or absolute) against root (the workspace repository's
// root, RepoStatus.Root) to the repo-root-relative, "/"-separated form
// Repo().Commit expects.
func repoRelativePath(root, arg string) (string, error) {
	abs := absolutizeAgainstCwd(arg)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errs.New(errs.Invalid, "%s is outside the workspace repository at %s", arg, root).
			WithDetail("path", arg)
	}
	return filepath.ToSlash(rel), nil
}
