package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/growsimplee/sapien/internal/errs"
)

// HostClients lists the MCP hosts HostConfig can render an entry for.
var HostClients = []string{"claude-code", "codex", "cursor", "cowork", "generic"}

// ClaudeScopes lists the values `claude mcp add --scope` accepts.
var ClaudeScopes = []string{"user", "local", "project"}

// DefaultClaudeScope is the scope Sapien installs Claude Code entries under
// when none is given. The entry bakes in the workspace path, so nothing
// ties it to the directory the command happened to run in, and a
// user-scoped entry is what makes Sapien available to an agent in every
// repo on the machine (README "Make it operational"). "local" restricts it
// to sessions started in the current directory; "project" writes a
// .mcp.json there for the whole team.
const DefaultClaudeScope = "user"

// ServerArgs returns the argument list that runs Sapien's MCP server for
// workspaceDir: args (extra arguments to place before the subcommand,
// usually none) followed by `mcp --workspace <workspaceDir>`.
func ServerArgs(args []string, workspaceDir string) []string {
	return append(append([]string{}, args...), "mcp", "--workspace", workspaceDir)
}

// HostConfig renders the MCP server entry a given host needs to talk to
// Sapien's MCP server for one workspace (PLAN §23.3): `command` and `args`
// are how to invoke the sapien binary (typically the sapien executable with
// no arguments); HostConfig always runs it as `<command> <args...> mcp
// --workspace <workspaceDir>`. scope applies to claude-code only and
// defaults to DefaultClaudeScope when empty.
func HostConfig(client, command string, args []string, workspaceDir, scope string) (string, error) {
	fullArgs := ServerArgs(args, workspaceDir)

	switch client {
	case "claude-code":
		scope, err := NormalizeClaudeScope(scope)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("claude mcp add --scope %s sapien -- %s", scope,
			strings.Join(append([]string{command}, fullArgs...), " ")), nil

	case "codex":
		var b strings.Builder
		b.WriteString("[mcp_servers.sapien]\n")
		fmt.Fprintf(&b, "command = %s\n", tomlString(command))
		b.WriteString("args = [")
		for i, a := range fullArgs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(tomlString(a))
		}
		b.WriteString("]\n")
		return b.String(), nil

	case "cursor", "cowork", "generic":
		doc := map[string]any{
			"mcpServers": map[string]any{
				"sapien": map[string]any{
					"command": command,
					"args":    fullArgs,
				},
			},
		}
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "marshaling host config")
		}
		return string(b), nil

	default:
		return "", errs.New(errs.Invalid, "unknown MCP host client %q; expected %s", client, strings.Join(HostClients, ", "))
	}
}

// NormalizeClaudeScope validates a `claude mcp add --scope` value, mapping
// "" to DefaultClaudeScope.
func NormalizeClaudeScope(scope string) (string, error) {
	if scope == "" {
		return DefaultClaudeScope, nil
	}
	for _, s := range ClaudeScopes {
		if s == scope {
			return s, nil
		}
	}
	return "", errs.New(errs.Invalid, "unknown claude-code scope %q; expected %s", scope, strings.Join(ClaudeScopes, ", "))
}

// MergeMCPServersFile installs, or replaces, the "sapien" entry in the
// mcpServers object of the JSON file at path -- the shape Cursor
// (~/.cursor/mcp.json) and other generic hosts read -- creating the file
// and its directory when they do not exist and leaving every other key in
// the document untouched.
func MergeMCPServersFile(path, command string, args []string) error {
	doc := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &doc); err != nil {
				return errs.Wrap(errs.Invalid, err, "parsing %s", path)
			}
		}
	case os.IsNotExist(err):
	default:
		return errs.Wrap(errs.Internal, err, "reading %s", path)
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["sapien"] = map[string]any{"command": command, "args": args}
	doc["mcpServers"] = servers

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "marshaling %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "creating %s", filepath.Dir(path))
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return errs.Wrap(errs.Internal, err, "writing %s", path)
	}
	return nil
}

// tomlString renders s as a quoted TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ClaudeDesktopConfigPath returns where Claude Desktop (and therefore
// Cowork, which runs inside it) reads its `mcpServers` from for the given
// OS and home directory: macOS "~/Library/Application Support/Claude/
// claude_desktop_config.json", Windows "%APPDATA%\Claude\
// claude_desktop_config.json" (appData is used when non-empty, else
// home\AppData\Roaming), Linux "~/.config/Claude/claude_desktop_config.json".
func ClaudeDesktopConfigPath(goos, home, appData string) string {
	const file = "claude_desktop_config.json"
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", file)
	case "windows":
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Claude", file)
	default:
		return filepath.Join(home, ".config", "Claude", file)
	}
}
