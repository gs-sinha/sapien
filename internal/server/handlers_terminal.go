package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/coder/websocket"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/terminal"
)

// Phase 7b (PLAN §34c): an agent pane that spawns the user's own `claude`
// or `codex` in a PTY, streamed to xterm.js over a WebSocket. This
// endpoint runs arbitrary processes on the user's machine, so the
// allowlist below is deliberately tight and is enforced entirely
// server-side: the browser only ever sends a command *name* (never a
// full command line) and a directory, both checked against fixed sets
// before anything is spawned. No user text is ever interpolated into a
// shell command -- the browser's keystrokes reach the process only as
// stdin bytes, after the process already exists.

// resolveShell returns the user's $SHELL, or /bin/sh if it's unset, as
// the allowlist's third allowed command (PLAN §34c Phase 7b item 2).
func resolveShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// allowedCommands is the exact, fixed set of commands the terminal
// endpoint will ever spawn: the two coding agents this pane exists for,
// plus a plain shell as a fallback. Order matters only for
// terminalTargetsResponse.Commands, which lists whichever of these
// actually resolve on PATH.
func allowedCommands() []string {
	return []string{"claude", "codex", resolveShell()}
}

// validateCommand checks command against allowedCommands and resolves it
// to an absolute executable path via exec.LookPath, exactly as PLAN §34c
// Phase 7b item 2 specifies. Anything else -- an unlisted command, or an
// allowed name that isn't actually installed -- is E_INVALID.
func validateCommand(command string) (string, error) {
	allowed := false
	for _, c := range allowedCommands() {
		if command == c {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errs.New(errs.Invalid, "command %q is not allowed", command).
			WithHint("only claude, codex, or your shell may be started here")
	}
	path, err := resolveTerminalCommand(command)
	if err != nil {
		return "", errs.New(errs.Invalid, "command %q was not found on PATH", command)
	}
	return path, nil
}

// Agents installed in ~/.local/bin should also be available when Sapien
// was started by a launcher with a minimal PATH. Prefer PATH so explicit
// installations keep their usual precedence; never expand arbitrary names.
func resolveTerminalCommand(command string) (string, error) {
	path, err := exec.LookPath(command)
	if err == nil || (command != "codex" && command != "claude") {
		return path, err
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		if path, fallbackErr := exec.LookPath(filepath.Join(home, ".local", "bin", command)); fallbackErr == nil {
			return path, nil
		}
	}
	return "", err
}

// cleanAbsDir returns p as a cleaned absolute path, or "" if that fails.
func cleanAbsDir(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// terminalDir is one entry of GET /v1/terminal/targets's "dirs" list.
type terminalDir struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// terminalDirs enumerates the directories the terminal endpoint accepts
// (PLAN §34c Phase 7b item 2): the workspace directory, every registered
// service's PackageDir and (for a local source) its resolved repo
// directory, and the user's home directory. Entries are de-duplicated by
// resolved path and skipped if the path doesn't actually exist as a
// directory -- both so the picker never offers a dead end and so
// validateDir (which checks a request's dir against exactly this list)
// can't be tricked by a path that merely looks equal.
func (s *Server) terminalDirs(ctx context.Context) []terminalDir {
	var dirs []terminalDir
	seen := map[string]bool{}
	add := func(label, path string) {
		clean := cleanAbsDir(path)
		if clean == "" || seen[clean] {
			return
		}
		if fi, err := os.Stat(clean); err != nil || !fi.IsDir() {
			return
		}
		seen[clean] = true
		dirs = append(dirs, terminalDir{Label: label, Path: clean})
	}

	ws := engineFrom(ctx).Workspace()
	if ws != nil {
		add("Workspace", ws.Dir)
	}

	if svcs, err := engineFrom(ctx).Services().List(ctx); err == nil {
		for _, svc := range svcs {
			add(svc.Name, svc.PackageDir)
			if svc.Source.Kind == domain.SourceLocal && svc.Source.Path != "" {
				repoDir := svc.Source.Path
				if !filepath.IsAbs(repoDir) && ws != nil {
					repoDir = filepath.Join(ws.Dir, repoDir)
				}
				add(svc.Name+" (repo)", repoDir)
			}
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		add("Home", home)
	}

	return dirs
}

// validateDir checks dir against terminalDirs and returns its resolved,
// cleaned absolute form. Anything not in that list -- including a
// subdirectory of an allowed one -- is E_INVALID: the allowlist is exact
// entries, not prefixes.
func (s *Server) validateDir(ctx context.Context, dir string) (string, error) {
	clean := cleanAbsDir(dir)
	if clean != "" {
		for _, d := range s.terminalDirs(ctx) {
			if d.Path == clean {
				return clean, nil
			}
		}
	}
	// The allowlist is per-workspace (terminalDirs reads the request's own
	// engine), so the message names the workspace the request actually
	// landed in. By far the commonest way to reach this error is a client
	// that offered one workspace's directories and then sent the chosen
	// one with a different workspace selected -- "not allowed" alone would
	// leave the reader looking at a path that plainly does exist.
	if ws := engineFrom(ctx).Workspace(); ws != nil {
		return "", errs.New(errs.Invalid, "dir %q is not an allowed terminal directory in workspace %s", dir, ws.Dir).
			WithHint("pick a directory from GET /v1/terminal/targets for this workspace")
	}
	return "", errs.New(errs.Invalid, "dir %q is not an allowed terminal directory", dir).
		WithHint("pick a directory from GET /v1/terminal/targets")
}

// terminalTargetsResponse is GET /v1/terminal/targets's body: what the
// UI's picker offers before starting a session.
type terminalTargetsResponse struct {
	Commands []string      `json:"commands"`
	Dirs     []terminalDir `json:"dirs"`
}

// handleTerminalTargets implements GET /v1/terminal/targets.
func (s *Server) handleTerminalTargets(w http.ResponseWriter, r *http.Request) {
	var commands []string
	for _, c := range allowedCommands() {
		if _, err := resolveTerminalCommand(c); err == nil {
			commands = append(commands, c)
		}
	}
	writeJSON(w, http.StatusOK, terminalTargetsResponse{
		Commands: commands,
		Dirs:     s.terminalDirs(r.Context()),
	})
}

// terminalControlMessage is a text-frame control message the client may
// send; today the only kind is "resize". Any other frame content (or an
// unparseable one) is silently ignored rather than closing the
// connection -- a forward-compatible no-op, not a protocol error.
type terminalControlMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// terminalExitMessage is the final text frame sent before the socket
// closes.
type terminalExitMessage struct {
	Type string `json:"type"`
	Code int    `json:"code"`
}

// handleTerminal implements GET /v1/terminal?command=&dir=&cols=&rows=: it
// validates command and dir, spawns a PTY session, and upgrades to a
// WebSocket that relays binary frames as PTY output (server->client) and
// stdin (client->server), with a JSON resize control message and a final
// JSON exit message (see terminalControlMessage/terminalExitMessage).
// Auth and Origin checks are the same as GET /v1/events (authMiddleware,
// hostOriginMiddleware); Accept is likewise told to skip its own
// same-origin check since hostOriginMiddleware already ran.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	command := r.URL.Query().Get("command")
	dir := r.URL.Query().Get("dir")
	cols := queryInt(r, "cols", 80)
	rows := queryInt(r, "rows", 24)

	cmdPath, err := validateCommand(command)
	if err != nil {
		writeError(w, err)
		return
	}
	workDir, err := s.validateDir(r.Context(), dir)
	if err != nil {
		writeError(w, err)
		return
	}

	env := os.Environ()
	env = append(env, "TERM=xterm-256color")
	// The pane's workspace is the request's workspace, not the daemon's
	// primary one: engineFrom resolves whatever the header or `?workspace=`
	// named (workspacectx.go), and this is the variable an agent started in
	// the pane reads to decide which workspace its own MCP/CLI calls talk
	// to. Sending the selector on the upgrade is therefore all a client has
	// to do to make the pane belong to the workspace the user is looking at.
	if ws := engineFrom(r.Context()).Workspace(); ws != nil {
		env = append(env, "SAPIEN_WORKSPACE="+ws.Dir)
	}

	sess, err := s.terminal.Start(context.Background(), terminal.Spec{
		Command: cmdPath,
		Dir:     workDir,
		Env:     env,
		Cols:    cols,
		Rows:    rows,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		sess.Close()
		return
	}

	s.idle.wsOpened()
	defer s.idle.wsClosed()
	defer sess.Close()

	// Deliberately not wrapped in context.WithCancel: coder/websocket treats
	// cancelling a context passed to Read/Write as a reason to close the
	// whole connection outright (see Conn.setupReadTimeout/
	// setupWriteTimeout), not just to abort that one call. Cancelling our
	// own derived context the moment the process exited used to race the
	// exit frame below out of existence -- the connection was gone before
	// it could be sent. Using r.Context() as-is and tearing the connection
	// down ourselves, only via Close after the exit frame is written,
	// avoids that.
	ctx := r.Context()

	// Relay PTY output to the client for as long as the session produces
	// it; ends on its own once the process exits (Output() closes).
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		for chunk := range sess.Output() {
			if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				return
			}
		}
	}()

	// Read stdin/control frames from the client until it disconnects (or
	// the connection is closed out from under it below).
	readErr := make(chan struct{})
	go func() {
		defer close(readErr)
		for {
			msgType, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			switch msgType {
			case websocket.MessageBinary:
				_, _ = sess.Write(data)
			case websocket.MessageText:
				var msg terminalControlMessage
				if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
					_ = sess.Resize(msg.Cols, msg.Rows)
				}
			}
		}
	}()

	select {
	case <-relayDone:
		// The process exited on its own; the client may still be
		// connected and the reader goroutine still blocked in Read, which
		// is fine -- Close below (after the exit frame) unblocks it.
	case <-readErr:
		// The client disconnected first; kill the session so readLoop's
		// drain of Output() (relayDone only closes once that channel
		// does) doesn't hang waiting for a process that will never
		// produce more output for a peer that's gone.
		sess.Close()
		<-relayDone
	}

	_ = sess.Wait()
	exitMsg, _ := json.Marshal(terminalExitMessage{Type: "exit", Code: sess.ExitCode()})
	_ = conn.Write(ctx, websocket.MessageText, exitMsg) // best-effort if the client already left
	conn.Close(websocket.StatusNormalClosure, "")
	<-readErr
}
