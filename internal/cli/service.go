package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/gitsrc"
	"github.com/gs-sinha/sapien/internal/workspace"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/domain"
)

func init() { Register(newServiceCmd) }

func newServiceCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage the workspace's registered services",
	}
	cmd.AddCommand(
		newServiceAddCmd(app),
		newServiceListCmd(app),
		newServiceRemoveCmd(app),
		newServiceSyncCmd(app),
		newServiceBindCmd(app),
		newServiceUnbindCmd(app),
	)
	return cmd
}

// looksLikeGitURL reports whether loc is a git remote rather than a local
// path (PLAN §21): an SSH shorthand ("git@host:org/repo.git"), an ssh://
// URL, or an https:// URL ending in ".git".
func looksLikeGitURL(loc string) bool {
	return gitsrc.IsGitURL(loc)
}

func newServiceAddCmd(app *App) *cobra.Command {
	var name, ref, subdir, contract string
	var showAccepted, localOnly, team, force bool

	cmd := &cobra.Command{
		Use:   "add <path|git-url>",
		Short: "Register a service from a local path or a git URL",
		Long: `Add registers a service.

A git URL is always committed as a git source, exactly as before.

For a local path, in a workspace whose sapien.workspace.yaml is itself
committed to a git repository with a remote (workspace.IsShared), Add
defaults to committing the checkout's own origin as a git source and
binding the checkout here (PLAN §7b's add-from-checkout), so every clone
of the team repo gets a usable source at once instead of one machine's
absolute path. --local forces the old behaviour: the path itself is
committed, verbatim, local-only. --team additionally allows a path that is
a subdirectory of its repository (a monorepo service) to be committed
with that subdirectory recorded, instead of falling back to --local
automatically (with an explanatory line) the way an unqualified add does
for such a path.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc := args[0]

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if !looksLikeGitURL(loc) && !localOnly {
				if svc, addErr, handled := tryAddFromCheckout(cmd, app, eng, loc, name, ref, force, team); handled {
					if app.Printer.IsJSON() {
						if svc != nil {
							if jerr := app.Printer.JSON(svc); jerr != nil {
								return jerr
							}
						}
						return addErr
					}
					if svc != nil {
						app.Printer.Line("%s", addFromCheckoutLine(svc))
						if showAccepted {
							printAcceptedWarnings(app.Printer, svc)
						}
						printMissingEnvHint(app, svc)
					}
					return addErr
				}
			}

			src := domain.Source{Contract: contract}
			if looksLikeGitURL(loc) {
				src.Kind = domain.SourceGit
				src.URL = loc
				src.Ref = ref
				src.Subdir = subdir
			} else {
				src.Kind = domain.SourceLocal
				src.Path = absolutizeAgainstCwd(loc)
			}

			svc, addErr := eng.Services().Add(cmd.Context(), name, src)
			addErr = addConflictHint(addErr, name)

			if app.Printer.IsJSON() {
				if svc != nil {
					if jerr := app.Printer.JSON(svc); jerr != nil {
						return jerr
					}
				}
				return addErr
			}

			if svc != nil {
				printServiceHuman(app.Printer, svc)
				if showAccepted {
					printAcceptedWarnings(app.Printer, svc)
				}
				printMissingEnvHint(app, svc)
			}
			return addErr
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "service name (default: derived from service.yaml or the contract's info.title)")
	cmd.Flags().StringVar(&ref, "ref", "", "git ref: branch, tag, or commit (git sources only)")
	cmd.Flags().StringVar(&subdir, "subdir", "", "package directory inside the repo, default \"api\" (git sources only)")
	cmd.Flags().StringVar(&contract, "contract", "", "explicit contract file, overriding discovery")
	cmd.Flags().BoolVar(&showAccepted, "show-accepted", false, "also print each accepted warning and the reason it was accepted")
	cmd.Flags().BoolVar(&localOnly, "local", false, "commit the local path itself, even in a shared workspace")
	cmd.Flags().BoolVar(&team, "team", false, "allow committing a repository subdirectory as the source, with this path recorded as its subdir")
	cmd.Flags().BoolVar(&force, "force", false, "register even when no API package is found yet under the checkout")
	return cmd
}

// tryAddFromCheckout is service add's team-aware default for a local path
// (PLAN §7b): in a shared workspace, commit the checkout's own origin as a
// git source and bind the checkout here, instead of committing loc itself
// -- an absolute path only this machine has. handled is false when the
// caller must fall back to the plain Add below: either this workspace
// isn't shared, or AddFromCheckout refused loc (no git origin, or a
// repository subdirectory without --team) and the developer did not
// insist with --team -- in which case the fallback reason is printed here
// before returning.
func tryAddFromCheckout(cmd *cobra.Command, app *App, eng engine.Engine, loc, name, ref string, force, team bool) (svc *domain.Service, addErr error, handled bool) {
	ws, err := app.Workspace()
	if err != nil || !workspace.IsShared(ws) {
		return nil, nil, false
	}

	path := absolutizeAgainstCwd(loc)
	svc, addErr = eng.Services().AddFromCheckout(cmd.Context(), name, path, engine.AddFromCheckoutOptions{
		Ref: ref, Force: force, AllowSubdir: team,
	})
	if addErr == nil {
		return svc, nil, true
	}
	if team {
		return svc, addErr, true
	}

	e := errs.As(addErr)
	if localAdd, _ := e.Details["local_add"].(bool); localAdd {
		if !app.Printer.IsJSON() {
			app.Printer.Line("%s", app.Printer.Dim(fmt.Sprintf(
				"committing as a local path instead: %s; pass --team to commit the repository instead", e.Message)))
		}
		return nil, nil, false
	}
	return svc, addErr, true
}

// addFromCheckoutLine renders what AddFromCheckout committed (the team git
// source) and bound (the checkout this machine reads), e.g. "committed
// order-service as git@github.com:org/order-service.git @ default; this
// machine reads /path (branch main)".
func addFromCheckoutLine(svc *domain.Service) string {
	team := svc.Source
	where := svc.PackageDir
	branch := ""
	if svc.Binding != nil {
		if svc.Binding.Team != nil {
			team = *svc.Binding.Team
		}
		if svc.Binding.Local != nil {
			where = svc.Binding.Local.Path
			branch = svc.Binding.Local.Branch
		}
	}
	line := fmt.Sprintf("committed %s as %s @ %s; this machine reads %s", svc.Name, sourceString(team), teamRef(&team), where)
	if branch != "" {
		line += fmt.Sprintf(" (branch %s)", branch)
	}
	return line
}

// addConflictHint adds "already registered; run `sapien service sync
// <name>`" to an E_CONFLICT error from Services().Add, so the CLI still
// fails the command (unlike the MCP add_service tool, which re-syncs
// automatically) but tells the caller exactly how to recover. name is the
// service name the conflict was actually reported for, preferring the
// error's own "name" detail (set by workspace.AddService, which knows the
// derived name even when the caller passed none) over the --name flag.
func addConflictHint(err error, flagName string) error {
	if err == nil || errs.CodeOf(err) != errs.Conflict {
		return err
	}
	e := errs.As(err)
	conflictName := flagName
	if v, ok := e.Details["name"].(string); ok && v != "" {
		conflictName = v
	}
	return e.WithHint(fmt.Sprintf("already registered; run `sapien service sync %s`", conflictName))
}

// warningLocation renders a LintWarning's source as "file[:line]", or "" if
// it has none.
func warningLocation(w domain.LintWarning) string {
	if w.Source == nil || w.Source.File == "" {
		return ""
	}
	loc := w.Source.File
	if w.Source.Line > 0 {
		loc += fmt.Sprintf(":%d", w.Source.Line)
	}
	return loc
}

// printServiceHuman renders one service's human-mode summary: name,
// operation count, status, how many warnings were accepted (reviewed), and
// every unaccepted warning (code, message, file:line). Accepted warnings
// themselves are only printed by printAcceptedWarnings, behind --show-accepted.
func printServiceHuman(p *Printer, svc *domain.Service) {
	p.Line("%s: %d operations, status %s, %d warnings accepted", svc.Name, svc.OperationCount, svc.Status, len(svc.AcceptedWarnings))
	if line := coverageLine(svc.Coverage); line != "" {
		p.Line("%s", line)
	}
	if svc.Error != "" {
		p.Line("error: %s", svc.Error)
	}
	for _, w := range svc.Warnings {
		if loc := warningLocation(w); loc != "" {
			p.Line("warning [%s] %s (%s)", w.Code, w.Message, loc)
		} else {
			p.Line("warning [%s] %s", w.Code, w.Message)
		}
	}
}

// coverageLine states how much of the service an agent in another repo can
// actually learn from Sapien: how many operations the narrative docs reach and
// how many of the ones that take a body show what one looks like. It is
// printed next to the operation count because that number alone reads like
// completeness while saying nothing about whether the service is
// understandable.
func coverageLine(cov *domain.DocCoverage) string {
	if cov == nil || cov.Operations == 0 {
		return ""
	}
	out := fmt.Sprintf("docs: %d/%d operations documented", cov.Documented, cov.Operations)
	if cov.Deprecated > 0 {
		// Printed beside an operation count that includes them, so say why
		// the two numbers differ rather than leaving it to be guessed.
		out += fmt.Sprintf(" (%d deprecated, not counted)", cov.Deprecated)
	}
	if cov.NeedExample > 0 {
		out += fmt.Sprintf("; examples: %d/%d operations that take a body", cov.WithExample, cov.NeedExample)
	}
	return out
}

// printAcceptedWarnings prints one "accepted [CODE] message (reason)" line
// per accepted warning. Called only when --show-accepted is set: by
// default an accepted warning's count is enough (printServiceHuman already
// shows it), since accepting one is meant to close the matter, not add more
// output to read past.
func printAcceptedWarnings(p *Printer, svc *domain.Service) {
	for _, aw := range svc.AcceptedWarnings {
		p.Line("accepted [%s] %s (%s)", aw.Code, aw.Message, aw.Reason)
	}
}

func newServiceListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered services",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			svcs, err := eng.Services().List(cmd.Context())
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(svcs)
			}

			rows := make([][]string, 0, len(svcs))
			for _, s := range svcs {
				last := ""
				if !s.LastIndexed.IsZero() {
					last = s.LastIndexed.UTC().Format(time.RFC3339)
				}
				rows = append(rows, []string{
					s.Name, readsCell(s), string(s.Status), strconv.Itoa(s.OperationCount), coverageCell(s.Coverage), strconv.Itoa(len(s.AcceptedWarnings)), sourceString(s.Source), last,
				})
			}
			app.Printer.Table([]string{"NAME", "READS", "STATUS", "OPS", "DOCS", "ACCEPTED", "SOURCE", "LAST INDEXED"}, rows)
			return nil
		},
	}
}

// readsCell is the READS column: which source this machine reads the
// service from (PLAN §7b). "local <branch>" for a checkout git can
// describe, "local" for one it cannot, "team <ref>" for the managed clone
// of the committed git source. It sits right after NAME because on a team
// workspace it is the one thing that differs between two machines running
// the same command. A row indexed before bindings existed has no Binding;
// its effective Source says the same thing, minus the branch.
func readsCell(svc domain.Service) string {
	if b := svc.Binding; b != nil {
		switch b.Mode {
		case domain.BindingLocal:
			if b.Local != nil && b.Local.Branch != "" {
				return "local " + b.Local.Branch + driftSuffix(b.Local)
			}
			return "local"
		case domain.BindingTeam:
			return "team " + teamRef(b.Team)
		}
	}
	if svc.Source.Kind == domain.SourceGit {
		return "team " + teamRef(&svc.Source)
	}
	return "local"
}

// driftSuffix renders how a bound checkout compares to the team ref, e.g.
// " +2/-3" (2 ahead, 3 behind), " -3" (behind only), or "" when both are
// zero -- either exactly current, or the team ref is unknown to this
// clone (never fetched), which reads the same to a developer either way.
func driftSuffix(co *domain.LocalCheckout) string {
	if co.Ahead == 0 && co.Behind == 0 {
		return ""
	}
	var parts []string
	if co.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("+%d", co.Ahead))
	}
	if co.Behind > 0 {
		parts = append(parts, fmt.Sprintf("-%d", co.Behind))
	}
	return " " + strings.Join(parts, "/")
}

// teamRef names the ref a team source tracks, "default" when the
// workspace entry leaves it to the remote's default branch.
func teamRef(src *domain.Source) string {
	if src == nil || src.Ref == "" {
		return "default"
	}
	return src.Ref
}

// coverageCell is the "DOCS" column: documented operations over total, so a
// service that is indexed but unexplained is visible in the listing rather
// than only in its own detail output.
func coverageCell(cov *domain.DocCoverage) string {
	if cov == nil || cov.Operations == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", cov.Documented, cov.Operations)
}

// sourceString renders a Source for the "SOURCE" column.
func sourceString(src domain.Source) string {
	switch src.Kind {
	case domain.SourceLocal:
		return src.Path
	case domain.SourceGit:
		return src.URL
	default:
		return string(src.Kind)
	}
}

func newServiceRemoveCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			name := args[0]
			if err := eng.Services().Remove(cmd.Context(), name); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"removed": name})
			}
			app.Printer.Line("removed %s", name)
			return nil
		},
	}
}

func newServiceSyncCmd(app *App) *cobra.Command {
	var showAccepted bool

	cmd := &cobra.Command{
		Use:   "sync [name]",
		Short: "Re-read a service's source and reindex it (every service, if name is omitted)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			svcs, syncErr := eng.Services().Sync(cmd.Context(), name)

			if app.Printer.IsJSON() {
				if jerr := app.Printer.JSON(svcs); jerr != nil {
					return jerr
				}
				return syncErr
			}

			for _, svc := range svcs {
				printServiceHuman(app.Printer, &svc)
				if showAccepted {
					printAcceptedWarnings(app.Printer, &svc)
				}
				printMissingEnvHint(app, &svc)
			}
			return syncErr
		},
	}

	cmd.Flags().BoolVar(&showAccepted, "show-accepted", false, "also print each accepted warning and the reason it was accepted")
	return cmd
}

func newServiceBindCmd(app *App) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "bind [name] <path>",
		Short: "Read a service from a local checkout on this machine instead of its committed source",
		Long: `Bind makes this machine read <name> from the checkout at <path> instead of
the git source committed in sapien.workspace.yaml. The override is recorded
in sapien.workspace.local.yaml (gitignored, per machine), never in the
committed file, so teammates keep reading the team source. A bound service
is reindexed on every save, and its service-scoped memories, examples and
flows become writable: they land in the checkout and ride your own branch
and pull request. Sapien never commits, pushes or checks out there.

With one argument, <name> is inferred from the checkout's own git origin:
exactly one registered service must be cloned from it, or bind errors out
naming the name to pass (or the ambiguity, when more than one matches).

Unless --force, the checkout is validated first: its origin must match the
service's committed git source (or it must have none registered to check
against), and it must have a discoverable API package.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			name, path := "", args[0]
			if len(args) == 2 {
				name, path = args[0], args[1]
			}

			svc, bindErr := eng.Services().BindWith(cmd.Context(), name, absolutizeAgainstCwd(path), engine.BindOptions{Force: force})
			return printBindingResult(app, svc, bindErr)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "bind even when the checkout's origin does not match the team source, or it has no API package yet")
	return cmd
}

func newServiceUnbindCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "unbind <name>",
		Short: "Read a service from its committed source again, dropping this machine's override",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			svc, unbindErr := eng.Services().Unbind(cmd.Context(), args[0])
			return printBindingResult(app, svc, unbindErr)
		},
	}
}

// printBindingResult renders the service a bind or unbind produced: the
// Service under --json, otherwise one line saying what the service reads
// now. Like add and sync, the record is printed even when the resync
// failed, since the override was still recorded and the error says what
// to fix.
func printBindingResult(app *App, svc *domain.Service, err error) error {
	if app.Printer.IsJSON() {
		if svc != nil {
			if jerr := app.Printer.JSON(svc); jerr != nil {
				return jerr
			}
		}
		return err
	}
	if svc != nil {
		app.Printer.Line("%s", bindingLine(svc))
		if svc.Error != "" {
			app.Printer.Line("error: %s", svc.Error)
		}
	}
	return err
}

// bindingLine states what svc reads on this machine and what that means
// for writing knowledge into it, in one line.
func bindingLine(svc *domain.Service) string {
	b := svc.Binding
	if b != nil && b.Mode == domain.BindingLocal {
		where := svc.Source.Path
		branch := ""
		if b.Local != nil {
			where = b.Local.Path
			branch = b.Local.Branch
		}
		reads := "local"
		if branch != "" {
			reads += " " + branch
		}
		team := ""
		if b.Team != nil && b.Team.Kind == domain.SourceGit {
			team = " (team source: " + b.Team.URL + ")"
		}
		return svc.Name + ": reads " + reads + " at " + where + team + "; service-scoped memories, examples and flows are writable"
	}
	src := &svc.Source
	if b != nil && b.Team != nil {
		src = b.Team
	}
	return svc.Name + ": reads team " + teamRef(src) + " from " + sourceString(*src) + "; service-scoped knowledge is read-only until bound"
}

// absolutizeAgainstCwd resolves a relative local path against the current
// working directory, as every other shell command would, before it is
// handed to the engine -- the engine resolves stored relative paths against
// the workspace directory, which is not where the user typed the command
// from. Absolute and "~"-prefixed paths pass through unchanged; the engine
// then stores a path inside the workspace relative to it again
// (workspace.NormalizeLocalPath).
func absolutizeAgainstCwd(p string) string {
	if p == "" || filepath.IsAbs(p) || p == "~" || strings.HasPrefix(p, "~/") {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// printMissingEnvHint tells the user when a service declares environments
// (service.yaml hints) that this workspace has no file for, so `--env stage`
// does not fail later with no explanation; `sapien env scaffold` creates
// them.
func printMissingEnvHint(app *App, svc *domain.Service) {
	ws, err := app.Workspace()
	if err != nil || svc == nil {
		return
	}
	if missing := env.MissingForService(ws, *svc); len(missing) > 0 {
		app.Printer.Line("environments declared by %s but not defined in this workspace: %s; run `sapien env scaffold` to create them", svc.Name, strings.Join(missing, ", "))
	}
}
