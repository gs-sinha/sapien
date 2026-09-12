package daemon_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/server"
)

func testWorkspace(t *testing.T) *domain.Workspace {
	t.Helper()
	return &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
}

func TestPath(t *testing.T) {
	ws := testWorkspace(t)
	assert.Equal(t, filepath.Join(ws.Dir, ".sapien", "daemon.json"), daemon.Path(ws))
}

func TestWriteReadRoundTrip(t *testing.T) {
	ws := testWorkspace(t)
	info := &daemon.Info{
		PID:       12345,
		Port:      54321,
		Token:     daemon.NewToken(),
		Version:   "1.2.3",
		Started:   time.Now().UTC().Truncate(time.Second),
		Workspace: ws.Dir,
	}

	require.NoError(t, daemon.Write(ws, info))

	fi, err := os.Stat(daemon.Path(ws))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fi.Mode().Perm())

	got, err := daemon.Read(ws)
	require.NoError(t, err)
	assert.Equal(t, info.PID, got.PID)
	assert.Equal(t, info.Port, got.Port)
	assert.Equal(t, info.Token, got.Token)
	assert.Equal(t, info.Version, got.Version)
	assert.True(t, info.Started.Equal(got.Started))
	assert.Equal(t, info.Workspace, got.Workspace)
}

func TestWriteIsAtomic(t *testing.T) {
	ws := testWorkspace(t)
	require.NoError(t, daemon.Write(ws, &daemon.Info{PID: 1, Port: 1}))
	// A second Write must not leave stray temp files behind and must
	// replace the file's contents cleanly.
	require.NoError(t, daemon.Write(ws, &daemon.Info{PID: 2, Port: 2}))

	entries, err := os.ReadDir(filepath.Dir(daemon.Path(ws)))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "expected exactly daemon.json, no leftover temp files")

	got, err := daemon.Read(ws)
	require.NoError(t, err)
	assert.Equal(t, 2, got.PID)
}

func TestReadMissingFile(t *testing.T) {
	ws := testWorkspace(t)
	_, err := daemon.Read(ws)
	require.Error(t, err)
	assert.Equal(t, errs.DaemonUnavailable, errs.CodeOf(err))
}

func TestRemove(t *testing.T) {
	ws := testWorkspace(t)

	// Removing when nothing exists is not an error.
	require.NoError(t, daemon.Remove(ws))

	require.NoError(t, daemon.Write(ws, &daemon.Info{PID: 1, Port: 1}))
	require.NoError(t, daemon.Remove(ws))

	_, err := os.Stat(daemon.Path(ws))
	assert.True(t, os.IsNotExist(err))
}

func TestNewToken(t *testing.T) {
	a := daemon.NewToken()
	b := daemon.NewToken()
	assert.Len(t, a, 64) // 32 random bytes, hex-encoded
	assert.NotEqual(t, a, b)
	for _, c := range a {
		assert.Contains(t, "0123456789abcdef", string(c))
	}
}

func TestAliveNilInfo(t *testing.T) {
	assert.False(t, daemon.Alive(context.Background(), nil))
}

func TestAliveInvalidPID(t *testing.T) {
	assert.False(t, daemon.Alive(context.Background(), &daemon.Info{PID: 0, Port: 1}))
	assert.False(t, daemon.Alive(context.Background(), &daemon.Info{PID: -1, Port: 1}))
}

// TestWriteMkdirAllFails exercises Write's error path when it can't create
// the .sapien directory: ws.Dir is nested under a plain file, so MkdirAll
// fails with ENOTDIR.
func TestWriteMkdirAllFails(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0644))

	ws := &domain.Workspace{Dir: filepath.Join(blocker, "nested")}
	err := daemon.Write(ws, &daemon.Info{PID: 1, Port: 1})
	require.Error(t, err)
	assert.Equal(t, errs.Internal, errs.CodeOf(err))
}

// deadPID runs a trivial subprocess to completion and returns its PID,
// which the OS is guaranteed not to report as alive once Wait returns.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

func TestAliveDeadProcess(t *testing.T) {
	info := &daemon.Info{PID: deadPID(t), Port: 1}
	assert.False(t, daemon.Alive(context.Background(), info))
}

// freePort asks the OS for a port and immediately releases it, so nothing
// is listening there for Alive's health probe to find.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func TestAliveProcessAliveButNoServer(t *testing.T) {
	info := &daemon.Info{PID: os.Getpid(), Port: freePort(t)}
	assert.False(t, daemon.Alive(context.Background(), info))
}

// runningServer starts a real internal/server on 127.0.0.1:0 and returns
// its port, so Alive's GET /v1/health probe has something real to find.
func runningServer(t *testing.T, version string) (port int, token string) {
	t.Helper()
	ws := testWorkspace(t)
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	token = daemon.NewToken()
	srv := server.New(server.Options{
		Engine:  fake,
		Token:   token,
		Version: version,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr, err := srv.ListenAndServe(ctx, "127.0.0.1:0")
	require.NoError(t, err)

	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	p, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return p, token
}

func TestAliveLiveDaemon(t *testing.T) {
	port, token := runningServer(t, "1.0.0")
	info := &daemon.Info{PID: os.Getpid(), Port: port, Token: token, Version: "1.0.0"}
	assert.True(t, daemon.Alive(context.Background(), info))
}

func TestFindNoFile(t *testing.T) {
	ws := testWorkspace(t)
	info, err := daemon.Find(context.Background(), ws, "1.0.0")
	require.NoError(t, err)
	assert.Nil(t, info)
}

func TestFindStaleRemovesFile(t *testing.T) {
	ws := testWorkspace(t)
	require.NoError(t, daemon.Write(ws, &daemon.Info{PID: deadPID(t), Port: freePort(t), Version: "1.0.0"}))

	info, err := daemon.Find(context.Background(), ws, "1.0.0")
	require.NoError(t, err)
	assert.Nil(t, info)

	_, statErr := os.Stat(daemon.Path(ws))
	assert.True(t, os.IsNotExist(statErr), "stale daemon.json should have been removed")
}

func TestFindVersionMismatch(t *testing.T) {
	ws := testWorkspace(t)
	port, token := runningServer(t, "1.0.0")
	require.NoError(t, daemon.Write(ws, &daemon.Info{PID: os.Getpid(), Port: port, Token: token, Version: "1.0.0"}))

	info, err := daemon.Find(context.Background(), ws, "2.0.0")
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	e := errs.As(err)
	assert.Equal(t, "1.0.0", e.Details["daemon_version"])

	// A version mismatch on a live daemon must not delete daemon.json --
	// the daemon is still there, just not what the caller wanted.
	_, statErr := os.Stat(daemon.Path(ws))
	assert.NoError(t, statErr)
}

func TestFindLiveMatchingVersion(t *testing.T) {
	ws := testWorkspace(t)
	port, token := runningServer(t, "1.0.0")
	want := &daemon.Info{PID: os.Getpid(), Port: port, Token: token, Version: "1.0.0", Workspace: ws.Dir}
	require.NoError(t, daemon.Write(ws, want))

	got, err := daemon.Find(context.Background(), ws, "1.0.0")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.PID, got.PID)
	assert.Equal(t, want.Port, got.Port)
	assert.Equal(t, want.Token, got.Token)
	assert.Equal(t, want.Version, got.Version)
}

func TestFindUnresponsiveProcessPreservesDiscovery(t *testing.T) {
	ws := testWorkspace(t)
	require.NoError(t, daemon.Write(ws, &daemon.Info{PID: os.Getpid(), Port: freePort(t), Version: "1.0.0"}))
	info, err := daemon.Find(context.Background(), ws, "1.0.0")
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Equal(t, errs.DaemonUnavailable, errs.CodeOf(err))
	_, err = daemon.Read(ws)
	require.NoError(t, err)
}

func TestAliveAllowsBusyDaemonAndHonorsCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(750 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer ts.Close()
	_, portString, err := net.SplitHostPort(ts.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portString)
	require.NoError(t, err)
	info := &daemon.Info{PID: os.Getpid(), Port: port}
	assert.True(t, daemon.Alive(context.Background(), info))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, daemon.Alive(ctx, info))
}

// Running is the process half of Alive, and the distinction is the whole
// point: a daemon with nothing answering on its port -- swapped out,
// mid-index, SIGSTOPped -- fails the health check while its process is
// still there to be signalled.
func TestRunningTrueForAnUnresponsiveProcess(t *testing.T) {
	info := &daemon.Info{PID: os.Getpid(), Port: freePort(t)}
	require.False(t, daemon.Alive(context.Background(), info))
	assert.True(t, daemon.Running(info))
}

func TestRunningFalseForAnExitedProcess(t *testing.T) {
	assert.False(t, daemon.Running(&daemon.Info{PID: deadPID(t), Port: 1}))
}

func TestRunningFalseForNilOrInvalidPID(t *testing.T) {
	assert.False(t, daemon.Running(nil))
	assert.False(t, daemon.Running(&daemon.Info{PID: 0}))
	assert.False(t, daemon.Running(&daemon.Info{PID: -1}))
}
