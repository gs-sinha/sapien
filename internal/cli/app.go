package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/growsimplee/sapien/internal/config"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/workspace"
)

// NewEngine is the seam through which the orchestrator wires up a real
// Engine implementation (engine.Local, and later engine.Remote) once one
// exists. It is nil until then, and App.Engine returns errs.NotImplemented
// in that case. Exactly one assignment, at process startup, is expected;
// commands must not read or write it directly — go through App.Engine.
var NewEngine func(ws *domain.Workspace) (engine.Engine, error)

// App is the shared, per-invocation context passed to every command.
// Exactly one App is created per call to Execute, so its fields never leak
// state between invocations (important for in-process tests that call
// Execute repeatedly).
type App struct {
	Stdout io.Writer
	Stderr io.Writer

	// Printer is ready to use from any command's RunE: it is populated by
	// the root command's PersistentPreRunE, which always runs before any
	// subcommand's RunE.
	Printer *Printer

	// Global persistent flags, bound directly by newRootCmd via pflag's
	// *Var functions; commands may read them after flag parsing (i.e. from
	// inside RunE) but must not write them.
	JSON         bool
	WorkspaceDir string
	Verbose      bool
	EnvName      string
	NoColor      bool

	ws       *domain.Workspace
	wsErr    error
	wsLoaded bool
}

// workspaceEnv names the environment variable that stands in for
// --workspace when the flag is not given.
const workspaceEnv = "SAPIEN_WORKSPACE"

// Workspace resolves the workspace for this invocation, in order:
// --workspace if set (loaded directly, no upward search), else
// $SAPIEN_WORKSPACE, else Discover from the current
// directory. The result is cached after the first call.
func (a *App) Workspace() (*domain.Workspace, error) {
	if a.wsLoaded {
		return a.ws, a.wsErr
	}
	a.wsLoaded = true

	if a.WorkspaceDir == "" {
		if envDir := os.Getenv(workspaceEnv); envDir != "" {
			a.WorkspaceDir = envDir
		}
	}

	if a.WorkspaceDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			a.wsErr = errs.Wrap(errs.Internal, err, "getting working directory")
			return nil, a.wsErr
		}
		a.ws, a.wsErr = workspace.Discover(cwd)
		if a.wsErr == nil || errs.CodeOf(a.wsErr) != errs.WorkspaceNotFound {
			return a.ws, a.wsErr
		}
		// Nothing above cwd: fall back to the user's default workspace,
		// the one `sapien mcp config --write` bound to their agent host, so
		// the CLI works from inside a service repo the same way the MCP
		// server does.
		def, err := config.DefaultWorkspace()
		if err != nil {
			a.wsErr = err
			return nil, a.wsErr
		}
		if def == "" {
			a.wsErr = errs.As(a.wsErr).WithHint("run `sapien init`, pass --workspace, set $" + workspaceEnv +
				", or set default_workspace in " + config.UserPath() + " (`sapien mcp config --write` does that)")
			return nil, a.wsErr
		}
		a.WorkspaceDir = def
	}

	abs, err := filepath.Abs(a.WorkspaceDir)
	if err != nil {
		a.wsErr = errs.Wrap(errs.Internal, err, "resolving --workspace %q", a.WorkspaceDir)
		return nil, a.wsErr
	}
	a.ws, a.wsErr = workspace.Load(filepath.Join(abs, domain.WorkspaceFileName))
	return a.ws, a.wsErr
}

// Engine resolves the workspace and returns its engine, via NewEngine.
func (a *App) Engine() (engine.Engine, error) {
	ws, err := a.Workspace()
	if err != nil {
		return nil, err
	}
	if NewEngine == nil {
		return nil, errs.New(errs.NotImplemented, "engine not wired yet")
	}
	return NewEngine(ws)
}

// EnvironmentName returns --env if set, else the workspace's default
// environment (which may itself be "").
func (a *App) EnvironmentName() (string, error) {
	if a.EnvName != "" {
		return a.EnvName, nil
	}
	ws, err := a.Workspace()
	if err != nil {
		return "", err
	}
	return workspace.DefaultEnvironment(ws), nil
}
