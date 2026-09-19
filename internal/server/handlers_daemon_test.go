package server

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func TestDaemonGet(t *testing.T) {
	started := time.Now().Add(-time.Hour).UTC()
	_, ts := newTestServer(t, func(o *Options) {
		o.Version = "1.2.3"
		o.Commit = "abc1234"
		o.Port = 7717
		o.Started = started
	})

	resp := doReq(t, ts, http.MethodGet, "/v1/daemon", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got daemonInfoResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "1.2.3", got.Version)
	assert.Equal(t, "abc1234", got.Commit)
	assert.Equal(t, 7717, got.Port)
	assert.Equal(t, os.Getpid(), got.PID)
	assert.WithinDuration(t, started, got.Started, time.Second)
	assert.NotEmpty(t, got.Executable)
	assert.NotEmpty(t, got.InstallMethod)
	assert.Equal(t, 1, got.WorkspacesOpen, "a single-Engine test server reports itself as the one open workspace")
	assert.Equal(t, 0, got.ActiveRuns)
	assert.Equal(t, 0, got.Terminals)
}

func TestDaemonGet_ActiveRunsCountsRunningRuns(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SeedRun(domain.Run{Status: domain.RunRunning})
	fake.SeedRun(domain.Run{Status: domain.RunRunning})
	fake.SeedRun(domain.Run{Status: domain.RunPassed}) // not in flight

	resp := doReq(t, ts, http.MethodGet, "/v1/daemon", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got daemonInfoResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, 2, got.ActiveRuns)
}

func TestDaemonRestart_NoHookIsNotImplemented(t *testing.T) {
	_, ts := newTestServer(t, nil) // RestartHook left nil

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/daemon/restart", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	assert.Equal(t, errs.NotImplemented, decodeErrBody(t, resp).Code)
}

func TestDaemonRestart_RefusesWhenRunsInFlightAndNotForced(t *testing.T) {
	fake, ts := newTestServer(t, func(o *Options) {
		o.RestartHook = func() {}
	})
	fake.SeedRun(domain.Run{Status: domain.RunRunning})

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/daemon/restart", "test-token", []byte(`{}`))
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	e := decodeErrBody(t, resp)
	assert.Equal(t, errs.Conflict, e.Code)
	assert.EqualValues(t, 1, e.Details["active_runs"])
}

func TestDaemonRestart_ForceBypassesActiveRunsCheck(t *testing.T) {
	called := make(chan struct{}, 1)
	fake, ts := newTestServer(t, func(o *Options) {
		o.RestartHook = func() { called <- struct{}{} }
	})
	fake.SeedRun(domain.Run{Status: domain.RunRunning})

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/daemon/restart", "test-token", []byte(`{"force": true}`))
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("RestartHook was not called")
	}
}

func TestDaemonRestart_NoRunsInFlightCallsHook(t *testing.T) {
	called := make(chan struct{}, 1)
	_, ts := newTestServer(t, func(o *Options) {
		o.RestartHook = func() { called <- struct{}{} }
	})

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/daemon/restart", "test-token", []byte(`{}`))
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("RestartHook was not called")
	}
}
