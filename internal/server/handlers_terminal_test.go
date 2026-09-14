package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

func TestTerminalCodexOutsidePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	bin := filepath.Join(home, ".local", "bin")
	require.NoError(t, os.MkdirAll(bin, 0755))
	codex := filepath.Join(bin, "codex")
	require.NoError(t, os.WriteFile(codex, []byte("#!/bin/sh\nexit 0\n"), 0755))

	path, err := validateCommand("codex")
	require.NoError(t, err)
	assert.Equal(t, codex, path)

	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/terminal/targets", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body terminalTargetsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Contains(t, body.Commands, "codex")

	// A PATH installation takes precedence over the fallback.
	preferred := filepath.Join(os.Getenv("PATH"), "codex")
	require.NoError(t, os.WriteFile(preferred, []byte("#!/bin/sh\nexit 0\n"), 0755))
	path, err = validateCommand("codex")
	require.NoError(t, err)
	assert.Equal(t, preferred, path)
}

func TestTerminalRejectsDisallowedCommand(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/terminal?command=rm&dir="+url.QueryEscape(t.TempDir()), reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestTerminalRejectsDisallowedDir(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet,
		"/v1/terminal?command="+url.QueryEscape("/bin/sh")+"&dir="+url.QueryEscape("/etc"),
		reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestTerminalRejectsUnknownShellBinary(t *testing.T) {
	t.Setenv("SHELL", "/no/such/shell-binary")
	_, ts := newTestServer(t, nil)

	// Allowed by name, but exec.LookPath can't find it: still E_INVALID, not
	// a 500 -- a nonexistent $SHELL is a request-time client mistake, not a
	// server fault.
	resp := doReq(t, ts, http.MethodGet,
		"/v1/terminal?command="+url.QueryEscape("/no/such/shell-binary")+"&dir="+url.QueryEscape(t.TempDir()),
		reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestTerminalTargets(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")

	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)

	pkgDir := t.TempDir()
	_, err := fake.Services().Add(context.Background(), "svc1", domain.Source{Kind: domain.SourceLocal, Path: pkgDir})
	require.NoError(t, err)
	// A service read from its team git source has no writable place on this
	// machine: its clone is a cache the daemon resets. Binding it to a
	// checkout is what makes it appear, at the checkout.
	_, err = fake.Services().Add(context.Background(), "team-svc", domain.Source{Kind: domain.SourceGit, URL: "git@github.com:org/team-svc.git", Ref: "main"})
	require.NoError(t, err)
	_, err = fake.Services().Add(context.Background(), "bound-svc", domain.Source{Kind: domain.SourceGit, URL: "git@github.com:org/bound-svc.git", Ref: "main"})
	require.NoError(t, err)
	boundDir := t.TempDir()
	_, err = fake.Services().Bind(context.Background(), "bound-svc", boundDir)
	require.NoError(t, err)

	srv := New(Options{
		Engine:  fake,
		Token:   "test-token",
		Version: "1.0.0",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := doReq(t, ts, http.MethodGet, "/v1/terminal/targets", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body terminalTargetsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Contains(t, body.Commands, "/bin/sh")
	assert.NotContains(t, body.Commands, "rm")
	for _, c := range body.Commands {
		assert.Contains(t, []string{"claude", "codex", "/bin/sh"}, c, "targets must only ever list the allowlisted commands")
	}

	var paths []string
	for _, d := range body.Dirs {
		paths = append(paths, d.Path)
	}
	assert.Contains(t, paths, ws.Dir, "workspace dir should be offered")
	assert.Contains(t, paths, pkgDir, "a local service's checkout should be offered")
	assert.Contains(t, paths, boundDir, "a bound service's checkout should be offered")
	labels := map[string]string{}
	for _, d := range body.Dirs {
		labels[d.Label] = d.Path
	}
	assert.Equal(t, pkgDir, labels["svc1"], "the checkout is labelled by service name, no (repo) suffix")
	assert.Equal(t, boundDir, labels["bound-svc"])
	assert.NotContains(t, labels, "team-svc", "a service read from its team git source has no writable directory here")
	for _, d := range body.Dirs {
		assert.NotContains(t, d.Label, "(repo)")
	}
}

// wsFrame is one message read off a terminal WebSocket by readTerminalFrames.
type wsFrame struct {
	typ  websocket.MessageType
	data []byte
}

// readTerminalFrames drains conn in the background so the test can select
// against an overall deadline instead of passing a short-lived context to
// Read -- coder/websocket closes the connection when a Read's context
// expires, so a per-call timeout can't be used as a retry loop.
func readTerminalFrames(conn *websocket.Conn) <-chan wsFrame {
	ch := make(chan wsFrame, 64)
	go func() {
		defer close(ch)
		for {
			typ, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			ch <- wsFrame{typ, data}
		}
	}()
	return ch
}

// TestTerminalEchoResizeAndExit exercises the whole WebSocket protocol
// against a real /bin/sh PTY: binary frames as stdin/stdout, a resize
// control frame, and the final exit frame once the shell exits.
func TestTerminalEchoResizeAndExit(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")

	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)

	srv := New(Options{
		Engine:  fake,
		Token:   "test-token",
		Version: "1.0.0",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := srv.ListenAndServe(ctx, "127.0.0.1:0")
	require.NoError(t, err)

	u := "ws://" + addr + "/v1/terminal?command=" + url.QueryEscape("/bin/sh") +
		"&dir=" + url.QueryEscape(ws.Dir) + "&cols=80&rows=24"

	conn, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer test-token"}},
	})
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	frames := readTerminalFrames(conn)

	require.NoError(t, conn.Write(context.Background(), websocket.MessageBinary, []byte("echo hello-terminal\n")))

	var out strings.Builder
	waitForOutput := func(want string) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case f, ok := <-frames:
				if !ok {
					t.Fatalf("connection closed before %q appeared; got %q", want, out.String())
				}
				if f.typ == websocket.MessageBinary {
					out.Write(f.data)
				}
				if strings.Contains(out.String(), want) {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %q in output; got %q", want, out.String())
			}
		}
	}
	waitForOutput("hello-terminal")

	// A resize frame must not disturb the session.
	resizeMsg, err := json.Marshal(terminalControlMessage{Type: "resize", Cols: 120, Rows: 40})
	require.NoError(t, err)
	require.NoError(t, conn.Write(context.Background(), websocket.MessageText, resizeMsg))

	// The connection should still carry stdin through after the resize.
	require.NoError(t, conn.Write(context.Background(), websocket.MessageBinary, []byte("echo still-alive\n")))
	waitForOutput("still-alive")

	// Exiting the shell should produce the final exit frame.
	require.NoError(t, conn.Write(context.Background(), websocket.MessageBinary, []byte("exit 0\n")))

	var exitMsg terminalExitMessage
	found := false
	deadline := time.After(10 * time.Second)
loop:
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				break loop
			}
			if f.typ == websocket.MessageText {
				var m terminalExitMessage
				if json.Unmarshal(f.data, &m) == nil && m.Type == "exit" {
					exitMsg = m
					found = true
					break loop
				}
			}
		case <-deadline:
			break loop
		}
	}
	require.True(t, found, "did not receive an exit frame")
	assert.Equal(t, 0, exitMsg.Code)
}

// ---- multi-workspace behaviour ----
//
// One daemon serves many workspaces (workspacectx.go), and the terminal
// endpoint's allowlist is per-workspace: terminalDirs reads the *request's*
// engine, so which directories exist -- and therefore which a start is
// allowed to use -- depends on the header or `?workspace=` the caller sent.
// The tests below pin that down from both ends.

// terminalWorkspaces is a two-workspace daemon plus the directories the
// terminal endpoint should derive from each, so a test can assert that the
// two lists really are disjoint rather than merely non-empty.
type terminalWorkspaces struct {
	ts         *httptest.Server
	primaryDir string
	primaryPkg string
	otherDir   string
	otherPkg   string
}

// newTerminalWorkspaces stands up the multi-workspace server the same way
// workspaces_e2e_test.go does (a real workspaces.Manager over fake engines,
// the secondary opened on demand), giving each workspace one registered
// service so the two GET /v1/terminal/targets answers differ in more than
// the workspace directory itself.
func newTerminalWorkspaces(t *testing.T) terminalWorkspaces {
	t.Helper()
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	w := terminalWorkspaces{
		primaryDir: writeWorkspace(t, "primary"),
		otherDir:   writeWorkspace(t, "other"),
		primaryPkg: t.TempDir(),
		otherPkg:   t.TempDir(),
	}

	primary, err := workspace.Load(filepath.Join(w.primaryDir, domain.WorkspaceFileName))
	require.NoError(t, err)

	primaryEng := enginetest.New(primary)
	_, err = primaryEng.Services().Add(context.Background(), "primary-svc",
		domain.Source{Kind: domain.SourceLocal, Path: w.primaryPkg})
	require.NoError(t, err)

	otherPkg := w.otherPkg
	mgr := workspaces.New(primary, primaryEng, workspaces.Options{
		PID: os.Getpid(),
		Open: func(ws *domain.Workspace, _ local.Options) (engine.Engine, error) {
			eng := enginetest.New(ws)
			if _, addErr := eng.Services().Add(context.Background(), "other-svc",
				domain.Source{Kind: domain.SourceLocal, Path: otherPkg}); addErr != nil {
				return nil, addErr
			}
			return eng, nil
		},
	})
	t.Cleanup(func() { _ = mgr.Close() })

	srv := New(Options{
		Engine:     primaryEng,
		Workspaces: mgr,
		Token:      "test-token",
		Version:    "1.2.3",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	w.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(w.ts.Close)
	return w
}

// getTerminalTargetsIn asks GET /v1/terminal/targets for one workspace ("" =
// the primary) and returns the directory paths it offers. reqOpts
// (server_test.go) has no field for the workspace header, so the request is
// built here.
func getTerminalTargetsIn(t *testing.T, ts *httptest.Server, workspaceDir string) []string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/terminal/targets", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	if workspaceDir != "" {
		req.Header.Set(WorkspaceHeader, workspaceDir)
	}

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body terminalTargetsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	var paths []string
	for _, d := range body.Dirs {
		paths = append(paths, d.Path)
	}
	return paths
}

// The agent pane's picker offers whatever this endpoint lists, so with a
// second workspace selected it has to list that workspace's directories --
// and none of the primary's. Offering the primary's was what started the
// pane in the wrong workspace even though the UI's header said otherwise.
func TestTerminalTargetsFollowTheSelectedWorkspace(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	w := newTerminalWorkspaces(t)

	other := getTerminalTargetsIn(t, w.ts, w.otherDir)
	assert.Contains(t, other, w.otherDir, "the selected workspace's own directory should be offered")
	assert.Contains(t, other, w.otherPkg, "the selected workspace's service should be offered")
	assert.NotContains(t, other, w.primaryDir, "the primary's directory must not leak into another workspace's targets")
	assert.NotContains(t, other, w.primaryPkg, "the primary's services must not leak into another workspace's targets")

	// And the reverse: naming nothing still means the primary, unchanged.
	primary := getTerminalTargetsIn(t, w.ts, "")
	assert.Contains(t, primary, w.primaryDir)
	assert.Contains(t, primary, w.primaryPkg)
	assert.NotContains(t, primary, w.otherDir)
	assert.NotContains(t, primary, w.otherPkg)
}

// A start is validated against the allowlist of the workspace the request
// names, not the daemon's primary: the primary's own directory is refused
// once `?workspace=` selects another one, and the error says which workspace
// it was checked against so the message is actionable.
func TestTerminalRejectsAnotherWorkspacesDir(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	w := newTerminalWorkspaces(t)

	resp := doReq(t, w.ts, http.MethodGet,
		"/v1/terminal?command="+url.QueryEscape("/bin/sh")+
			"&dir="+url.QueryEscape(w.primaryDir)+
			"&workspace="+url.QueryEscape(w.otherDir),
		reqOpts{token: "test-token"})

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := decodeErrBody(t, resp)
	assert.Equal(t, errs.Invalid, body.Code)
	assert.Contains(t, body.Message, w.otherDir, "the error should name the workspace the dir was checked against")
}

// `?workspace=` is the WebSocket route's equivalent of the header (a browser
// cannot set headers on an upgrade), and it must reach further than the
// allowlist: the PTY's SAPIEN_WORKSPACE has to name the selected workspace
// too, or an agent started in the pane talks to the primary while the pane
// sits in another workspace's directory.
func TestTerminalSapienWorkspaceEnvNamesTheSelectedWorkspace(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	w := newTerminalWorkspaces(t)

	// cols is generous so the PTY does not wrap the (long, temp-dir) path
	// being echoed back and break the substring match below.
	u := "ws://" + strings.TrimPrefix(w.ts.URL, "http://") +
		"/v1/terminal?command=" + url.QueryEscape("/bin/sh") +
		"&dir=" + url.QueryEscape(w.otherDir) +
		"&workspace=" + url.QueryEscape(w.otherDir) +
		"&cols=400&rows=24"

	conn, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer test-token"}},
	})
	require.NoError(t, err, "the other workspace's own directory must be an allowed start")
	defer conn.Close(websocket.StatusNormalClosure, "")

	frames := readTerminalFrames(conn)
	require.NoError(t, conn.Write(context.Background(), websocket.MessageBinary,
		[]byte("printf 'WS=[%s]\\n' \"$SAPIEN_WORKSPACE\"\n")))

	want := "WS=[" + w.otherDir + "]"
	var out strings.Builder
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("connection closed before %q appeared; got %q", want, out.String())
			}
			if f.typ == websocket.MessageBinary {
				out.Write(f.data)
			}
			// Strip the PTY's line endings before matching: the shell echoes
			// the typed line back before running it, so the value arrives on
			// a later line of the same stream.
			if strings.Contains(strings.NewReplacer("\r", "", "\n", "").Replace(out.String()), want) {
				assert.NotContains(t, out.String(), "WS=[]", "SAPIEN_WORKSPACE must not be empty")
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q in output; got %q", want, out.String())
		}
	}
}
