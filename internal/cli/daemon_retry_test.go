package cli_test

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/engine/remote"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/server"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// startFlakyDaemon is startFakeDaemon with a stall in front of it: the same
// real internal/server stack, but its GET /v1/health refuses the first
// `failures` probes, which is how daemon.Find sees a daemon that is running
// yet busy (errs.DaemonUnavailable). It returns the probe counter, so a test
// can prove exactly how many attempts findDaemon made.
func startFlakyDaemon(t *testing.T, ws *domain.Workspace, version string, failures int64) *atomic.Int64 {
	t.Helper()

	fake := enginetest.New(&domain.Workspace{Dir: t.TempDir(), Name: "flaky-daemon-backing"})
	enginetest.Seed(fake)

	token := "test-token-flaky-" + version
	inner := server.New(server.Options{Engine: fake, Token: token, Version: version}).Handler()

	var probes atomic.Int64
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" && probes.Add(1) <= failures {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(httpSrv.Close)

	u, err := url.Parse(httpSrv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	// os.Getpid(): the process must be alive (nothing here ever signals it)
	// for daemon.Find to reach the DaemonUnavailable branch at all.
	require.NoError(t, daemon.Write(ws, &daemon.Info{
		PID: os.Getpid(), Port: port, Token: token,
		Version: version, Started: time.Now(), Workspace: ws.Dir,
	}))
	return &probes
}

// A daemon that is alive but slow to answer its health probe is busy, not
// broken. Find reports errs.DaemonUnavailable for it, and the caller waits
// that out instead of failing on the first attempt.
func TestFindDaemon_RetriesUntilABusyDaemonAnswers(t *testing.T) {
	defer cli.SetFindDaemonRetry(3, 10*time.Millisecond)()

	ws, err := workspace.Init(t.TempDir(), "retry-busy")
	require.NoError(t, err)
	probes := startFlakyDaemon(t, ws, cli.Version, 1)

	info, err := cli.FindDaemon(context.Background(), ws, cli.Version)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, int64(2), probes.Load(), "one failed probe, then the retry that succeeded")
}

// The retry is bounded, and exhausting it returns the daemon's own
// DaemonUnavailable error -- never a fall back to engine.Local, which would
// open a second engine on the live daemon's database, and never a deletion
// of daemon.json, which would orphan it.
func TestFindDaemon_GivesUpWithDaemonUnavailableAfterTheLastAttempt(t *testing.T) {
	defer cli.SetFindDaemonRetry(3, 10*time.Millisecond)()

	ws, err := workspace.Init(t.TempDir(), "retry-exhausted")
	require.NoError(t, err)
	probes := startFlakyDaemon(t, ws, cli.Version, math.MaxInt64)

	info, err := cli.FindDaemon(context.Background(), ws, cli.Version)
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Equal(t, errs.DaemonUnavailable, errs.CodeOf(err))
	assert.Equal(t, int64(3), probes.Load(), "exactly the configured number of attempts")
	assert.FileExists(t, daemon.Path(ws), "discovery is preserved, not deleted")
}

// Only DaemonUnavailable is retried. A daemon from another build answers
// perfectly well; waiting for it to change its version would be absurd, so
// the conflict comes back from the first attempt as it always has.
func TestFindDaemon_DoesNotRetryAVersionConflict(t *testing.T) {
	defer cli.SetFindDaemonRetry(3, 30*time.Second)()

	ws, err := workspace.Init(t.TempDir(), "retry-conflict")
	require.NoError(t, err)
	probes := startFlakyDaemon(t, ws, "not-"+cli.Version, 0)

	info, err := cli.FindDaemon(context.Background(), ws, cli.Version)
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Equal(t, int64(1), probes.Load(), "a conflict is answered, not waited out")
}

// Nor is "no daemon here" retried: it is the ordinary case for every
// one-shot command on a workspace with no daemon, and it must stay as fast
// as it is today. The 30s delay above would show up immediately if it were.
func TestFindDaemon_DoesNotRetryWhenThereIsNoDaemon(t *testing.T) {
	defer cli.SetFindDaemonRetry(3, 30*time.Second)()

	ws, err := workspace.Init(t.TempDir(), "retry-absent")
	require.NoError(t, err)

	start := time.Now()
	info, err := cli.FindDaemon(context.Background(), ws, cli.Version)
	require.NoError(t, err)
	assert.Nil(t, info)
	assert.Less(t, time.Since(start), time.Second)
}

// The retry loop belongs to the caller's context: a cancelled command stops
// waiting at once rather than sitting out the rest of the budget.
func TestFindDaemon_StopsWaitingWhenTheContextIsCancelled(t *testing.T) {
	defer cli.SetFindDaemonRetry(3, 30*time.Second)()

	ws, err := workspace.Init(t.TempDir(), "retry-cancelled")
	require.NoError(t, err)
	startFlakyDaemon(t, ws, cli.Version, math.MaxInt64)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	info, err := cli.FindDaemon(ctx, ws, cli.Version)
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Equal(t, errs.DaemonUnavailable, errs.CodeOf(err))
	assert.Less(t, time.Since(start), time.Second)
}

// The one-shot CLI's engine selection goes through the same retry: a daemon
// that stalls once still gets used over HTTP, where before the stall failed
// the command outright.
func TestNewEngine_UsesTheDaemonAfterABriefStall(t *testing.T) {
	defer cli.SetFindDaemonRetry(3, 10*time.Millisecond)()

	ws, err := workspace.Init(t.TempDir(), "retry-remote")
	require.NoError(t, err)
	startFlakyDaemon(t, ws, cli.Version, 1)

	eng, err := cli.NewEngine(ws)
	require.NoError(t, err)
	defer eng.Close()

	_, ok := eng.(*remote.Remote)
	assert.True(t, ok, "expected *remote.Remote after the stall passed, got %T", eng)
}
