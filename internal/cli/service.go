package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/gitsrc"

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
	var showAccepted bool

	cmd := &cobra.Command{
		Use:   "add <path|git-url>",
		Short: "Register a service from a local path or a git URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc := args[0]
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

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

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
	return cmd
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
					s.Name, string(s.Status), strconv.Itoa(s.OperationCount), coverageCell(s.Coverage), strconv.Itoa(len(s.AcceptedWarnings)), sourceString(s.Source), last,
				})
			}
			app.Printer.Table([]string{"NAME", "STATUS", "OPS", "DOCS", "ACCEPTED", "SOURCE", "LAST INDEXED"}, rows)
			return nil
		},
	}
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
