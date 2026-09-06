package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine/enginetest"
	"github.com/growsimplee/sapien/internal/errs"
)

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
	assert.Contains(t, paths, pkgDir, "registered service's package dir should be offered")
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
