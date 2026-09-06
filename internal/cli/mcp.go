package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/growsimplee/sapien/internal/config"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine/local"
	"github.com/growsimplee/sapien/internal/engine/remote"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/mcp"
)

func init() { Register(newMCPCmd) }

// mcpConfigPaths returns the ordered list of files that make up ws's MCP
// permission configuration (PLAN §23.2): the workspace's own
// <ws>/.sapien/mcp.yaml, then the user's global ~/.sapien/config.yaml.
// Both are optional. Callers pass this same list to mcp.LoadConfig for the
// initial load (loadMCPConfig) and to Options.ConfigPaths so the running
// server hot-reloads permissions from exactly those files instead of a
// snapshot taken at startup -- see permissions.go's configSource. Without
// that, an edit to mcp.yaml (e.g. granting execute_mutation) would do
// nothing until the client, or the daemon it's bridged to, restarted --
// which for the daemon can also happen on its own, on the idle timeout.
func mcpConfigPaths(ws *domain.Workspace) []string {
	paths := []string{filepath.Join(ws.Dir, domain.WorkspaceStateDir, "mcp.yaml")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".sapien", "config.yaml"))
	}
	return paths
}

// loadMCPConfig loads the merged MCP permission configuration for ws from
// mcpConfigPaths(ws), exactly as mcp.LoadConfig's doc comment prescribes.
func loadMCPConfig(ws *domain.Workspace) (mcp.Config, error) {
	return mcp.LoadConfig(mcpConfigPaths(ws)...)
}

// newMCPCmd is `sapien mcp [--http]` (PLAN §4, §23): always daemon-backed.
// It finds (or starts) the workspace's daemon and bridges stdio to it over
// HTTP, so several hosts attached at once share one engine, one watcher,
// and one live-run state. SAPIEN_NO_DAEMON=1 instead serves stdio directly
// over an in-process engine.Local, for tests and for hosts that
// deliberately want isolation rather than the shared daemon.
func newMCPCmd(app *App) *cobra.Command {
	var httpMode bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the Sapien MCP server (PLAN §23), bridging stdio to the workspace's daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			if os.Getenv(noDaemonEnv) == "1" {
				if httpMode {
					return errs.New(errs.Invalid, "--http requires a daemon; unset %s", noDaemonEnv)
				}
				eng, err := local.Open(ws, local.Options{})
				if err != nil {
					return err
				}
				defer eng.Close()

				paths := mcpConfigPaths(ws)
				cfg, err := mcp.LoadConfig(paths...)
				if err != nil {
					return err
				}
				return mcp.ServeStdio(ctx, mcp.Options{Engine: eng, Config: cfg, ConfigPaths: paths, Version: Version})
			}

			info, err := findOrStartDaemon(ctx, ws, Version)
			if err != nil {
				return err
			}

			if httpMode {
				url := fmt.Sprintf("http://127.0.0.1:%d/mcp", info.Port)
				if app.Printer.IsJSON() {
					return app.Printer.JSON(map[string]string{
						"url":           url,
						"authorization": "Bearer " + info.Token,
					})
				}
				app.Printer.Line("%s", url)
				app.Printer.Line("Authorization: Bearer %s", info.Token)
				return nil
			}

			// The resolver re-finds (or respawns) ws's daemon on demand, so
			// a daemon recycled by `serve --restart` or the idle timeout
			// (PLAN §4) doesn't orphan this bridge for the rest of the
			// session: do() calls it once a connection-level failure or a
			// stale token shows the daemon behind info is gone.
			resolver := func(ctx context.Context) (string, string, error) {
				info, err := findOrStartDaemon(ctx, ws, Version)
				if err != nil {
					return "", "", err
				}
				return fmt.Sprintf("http://127.0.0.1:%d", info.Port), info.Token, nil
			}
			remoteEng, err := remote.New(fmt.Sprintf("http://127.0.0.1:%d", info.Port), info.Token,
				remote.WithEndpointResolver(resolver))
			if err != nil {
				return err
			}
			defer remoteEng.Close()

			paths := mcpConfigPaths(ws)
			cfg, err := mcp.LoadConfig(paths...)
			if err != nil {
				return err
			}
			return mcp.ServeStdio(ctx, mcp.Options{Engine: remoteEng, Config: cfg, ConfigPaths: paths, Version: Version})
		},
	}

	cmd.Flags().BoolVar(&httpMode, "http", false, "print the daemon's /mcp URL and bearer token instead of bridging stdio")
	cmd.AddCommand(newMCPConfigCmd(app))
	return cmd
}

// newMCPConfigCmd is `sapien mcp config --client ... [--scope ...]
// [--write]` (PLAN §23.3): prints (or installs) the host's MCP server
// entry for this workspace, always invoking the currently-running sapien
// executable as `<exe> mcp --workspace <dir>`. Because the workspace path
// is baked into the entry, the Claude Code entry defaults to the user
// scope, which makes Sapien available to an agent in every repo on the
// machine -- the onboarding journey in README "Make it operational".
func newMCPConfigCmd(app *App) *cobra.Command {
	var client, scope string
	var write bool

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print or install the MCP host entry for this workspace",
		Long: `Print or install the MCP host entry that runs Sapien's MCP server for this
workspace. Run it inside the workspace, or pass --workspace.

Clients: claude-code (runs "claude mcp add"), codex (~/.codex/config.toml),
cursor (~/.cursor/mcp.json), cowork (Claude Desktop's claude_desktop_config.json),
and generic (an mcpServers JSON block to paste). --scope applies to claude-code only: user (default; every Claude
Code session on this machine), local (sessions started in this directory),
or project (a .mcp.json in this directory for the team).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}

			exePath, err := os.Executable()
			if err != nil {
				return errs.Wrap(errs.Internal, err, "resolving the running executable's path")
			}
			if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
				exePath = resolved
			}

			text, err := mcp.HostConfig(client, exePath, nil, ws.Dir, scope)
			if err != nil {
				return err
			}

			if write {
				return writeMCPHostConfig(app, client, exePath, ws, scope, text)
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"client": client, "config": text})
			}
			app.Printer.Line("%s", text)
			return nil
		},
	}

	cmd.Flags().StringVar(&client, "client", "", "claude-code|codex|cursor|cowork|generic")
	cmd.Flags().StringVar(&scope, "scope", mcp.DefaultClaudeScope, "claude-code only: user|local|project")
	cmd.Flags().BoolVar(&write, "write", false, "install the entry instead of printing it")
	_ = cmd.MarkFlagRequired("client")
	return cmd
}

// writeMCPHostConfig implements `mcp config --write` per host (PLAN
// §23.3): claude-code runs `claude mcp add` when the claude CLI is on
// PATH (replacing an existing sapien entry in that scope); codex appends
// to ~/.codex/config.toml when that file already exists; cursor merges the
// entry into ~/.cursor/mcp.json and cowork into Claude Desktop's
// claude_desktop_config.json, creating either if needed. claude-code and
// codex fall back to printing the entry when they can't install it
// directly, exactly like cowork and generic (which have no local config
// file to install into at all) always do.
func writeMCPHostConfig(app *App, client, exePath string, ws *domain.Workspace, scope, text string) error {
	switch client {
	case "claude-code":
		claudePath, err := exec.LookPath("claude")
		if err != nil {
			app.Printer.Line("claude CLI not found on PATH; run this yourself:")
			app.Printer.Line("%s", text)
			return nil
		}
		scope, err := mcp.NormalizeClaudeScope(scope)
		if err != nil {
			return err
		}
		if err := claudeMCPAdd(app, claudePath, exePath, ws.Dir, scope); err != nil {
			return err
		}
		app.Printer.Line("installed sapien for claude-code (scope %s, workspace %s)", scope, ws.Dir)
		recordDefaultWorkspace(app, ws)
		return nil

	case "codex":
		home, err := os.UserHomeDir()
		if err != nil {
			return errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		cfgPath := filepath.Join(home, ".codex", "config.toml")
		if _, statErr := os.Stat(cfgPath); statErr != nil {
			app.Printer.Line("%s not found; add this yourself:", cfgPath)
			app.Printer.Line("%s", text)
			return nil
		}
		f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return errs.Wrap(errs.Internal, err, "opening %s", cfgPath)
		}
		defer f.Close()
		if _, err := fmt.Fprintf(f, "\n%s", text); err != nil {
			return errs.Wrap(errs.Internal, err, "appending to %s", cfgPath)
		}
		app.Printer.Line("appended sapien's MCP entry to %s", cfgPath)
		recordDefaultWorkspace(app, ws)
		return nil

	case "cursor":
		home, err := os.UserHomeDir()
		if err != nil {
			return errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		cfgPath := filepath.Join(home, ".cursor", "mcp.json")
		if err := mcp.MergeMCPServersFile(cfgPath, exePath, mcp.ServerArgs(nil, ws.Dir)); err != nil {
			return err
		}
		app.Printer.Line("installed sapien in %s (workspace %s)", cfgPath, ws.Dir)
		recordDefaultWorkspace(app, ws)
		return nil

	case "cowork":
		home, err := os.UserHomeDir()
		if err != nil {
			return errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		cfgPath := mcp.ClaudeDesktopConfigPath(runtime.GOOS, home, os.Getenv("APPDATA"))
		if err := mcp.MergeMCPServersFile(cfgPath, exePath, mcp.ServerArgs(nil, ws.Dir)); err != nil {
			return err
		}
		app.Printer.Line("installed sapien in %s (workspace %s)", cfgPath, ws.Dir)
		app.Printer.Line("restart Claude Desktop so Cowork picks it up")
		recordDefaultWorkspace(app, ws)
		return nil

	default: // generic: no host-local config file to install into.
		if app.Printer.IsJSON() {
			return app.Printer.JSON(map[string]string{"client": client, "config": text})
		}
		app.Printer.Line("%s", text)
		return nil
	}
}

// claudeMCPAdd runs `claude mcp add --scope <scope> sapien -- <exe> mcp
// --workspace <ws>`. When Claude Code reports that a sapien entry already
// exists in that scope, it removes the old entry and adds again, so
// re-running `mcp config --write` after moving the workspace or rebuilding
// sapien replaces the entry instead of failing.
func claudeMCPAdd(app *App, claudePath, exePath, wsDir, scope string) error {
	add := func() ([]byte, error) {
		c := exec.Command(claudePath, "mcp", "add", "--scope", scope, "sapien", "--", exePath, "mcp", "--workspace", wsDir)
		return c.CombinedOutput()
	}

	out, err := add()
	if err != nil && strings.Contains(strings.ToLower(string(out)), "already exists") {
		rm := exec.Command(claudePath, "mcp", "remove", "--scope", scope, "sapien")
		if rmOut, rmErr := rm.CombinedOutput(); rmErr != nil {
			return errs.Wrap(errs.Internal, rmErr, "replacing the existing sapien entry (claude mcp remove): %s", strings.TrimSpace(string(rmOut)))
		}
		out, err = add()
	}
	if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
		app.Printer.Line("%s", trimmed)
	}
	if err != nil {
		return errs.Wrap(errs.Internal, err, "running claude mcp add")
	}
	return nil
}

// recordDefaultWorkspace makes ws the user's default workspace
// (default_workspace in the user config, see internal/config) the first
// time an MCP entry is installed, so `sapien` commands typed inside a
// service repo resolve to the same workspace the agent host talks to. An
// existing default is left alone: a user with several workspaces chose it
// deliberately.
func recordDefaultWorkspace(app *App, ws *domain.Workspace) {
	current, err := config.DefaultWorkspace()
	if err != nil || current != "" {
		return
	}
	if err := config.SetDefaultWorkspace(ws.Dir); err != nil {
		app.Printer.Line("note: could not record %s as the default workspace: %v", ws.Dir, err)
		return
	}
	app.Printer.Line("recorded %s as default_workspace in %s, so sapien commands run from any directory use it", ws.Dir, config.UserPath())
}
