package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// --- GET /v1/settings/semantic -------------------------------------------

func TestSettingsSemanticGet_RoundTrip(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetSemanticSettings(domain.SemanticSettings{
		Enabled: true, Kind: "ollama", BaseURL: "http://127.0.0.1:11434", Model: "nomic-embed-text",
		BatchSize: 8, APIKeySet: false, Source: "user",
		Status: domain.SemanticStatus{State: domain.SemanticReady, Model: "nomic-embed-text", Dim: 768, Embedded: 10, Total: 10},
	})

	resp := doReq(t, ts, http.MethodGet, "/v1/settings/semantic", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out domain.SemanticSettings
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.True(t, out.Enabled)
	assert.Equal(t, "ollama", out.Kind)
	assert.Equal(t, "nomic-embed-text", out.Model)
	assert.Equal(t, "user", out.Source)
	assert.Equal(t, domain.SemanticReady, out.Status.State)
	assert.Equal(t, 10, out.Status.Embedded)
	assert.True(t, hasRecordedCall(fake, "Settings.GetSemantic"))
}

// TestSettingsSemanticGet_NeverLeaksAPIKey proves the response body never
// contains the literal string "api_key" mapping to anything but the
// api_key_set boolean -- the struct itself has no field for the real value,
// but this also catches a future field added carelessly.
func TestSettingsSemanticGet_NeverLeaksAPIKey(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fake.SetSemanticSettings(domain.SemanticSettings{Enabled: true, Kind: "openai", Model: "m", APIKeySet: true, Source: "user"})

	resp := doReq(t, ts, http.MethodGet, "/v1/settings/semantic", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var raw map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	_, hasKey := raw["api_key"]
	assert.False(t, hasKey, "the response must never carry an api_key field")
	assert.Equal(t, true, raw["api_key_set"])
}

// --- PUT /v1/settings/semantic --------------------------------------------

func TestSettingsSemanticPut_RoundTrip(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	body, err := json.Marshal(engine.SemanticPutRequest{Enabled: true, Kind: "ollama", Model: "nomic-embed-text"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/settings/semantic", "test-token", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out domain.SemanticSettings
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.True(t, out.Enabled)
	assert.Equal(t, "ollama", out.Kind)
	assert.Equal(t, "nomic-embed-text", out.Model)

	call := recordedCall(t, fake, "Settings.PutSemantic")
	req, ok := call.Args.(engine.SemanticPutRequest)
	require.True(t, ok)
	assert.True(t, req.Enabled)
	assert.Equal(t, "ollama", req.Kind)
}

// TestSettingsSemanticPut_InvalidIsBadRequest proves the engine's own
// validation error (kind required when enabled) reaches the client as a
// plain 400/E_INVALID.
func TestSettingsSemanticPut_InvalidIsBadRequest(t *testing.T) {
	_, ts := newTestServer(t, nil)

	body, err := json.Marshal(engine.SemanticPutRequest{Enabled: true})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/settings/semantic", "test-token", body)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

// TestSettingsSemanticPut_ReappliesOtherOpenWorkspaces proves a
// "user"-scope PUT (Scope left "") propagates to every OTHER open
// workspace on a multi-workspace daemon by calling ReloadSemantic on it,
// while a "workspace"-scope PUT does not.
func TestSettingsSemanticPut_ReappliesOtherOpenWorkspaces(t *testing.T) {
	primaryDir := newFakeWorkspaceDir(t, "primary")
	primaryWS, err := workspace.Load(filepath.Join(primaryDir, domain.WorkspaceFileName))
	require.NoError(t, err)
	primary := enginetest.New(primaryWS)
	enginetest.Seed(primary)

	otherDir := newFakeWorkspaceDir(t, "other")
	otherWS, err := workspace.Load(filepath.Join(otherDir, domain.WorkspaceFileName))
	require.NoError(t, err)
	other := &reloadTrackingEngine{Fake: enginetest.New(otherWS)}

	mgr := newFakeWorkspaceManager(t, primaryWS, map[string]engine.Engine{
		primaryDir: primary,
		otherDir:   other,
	})

	ts := newTestServerWithWorkspaces(t, primary, mgr)

	body, err := json.Marshal(engine.SemanticPutRequest{Enabled: true, Kind: "ollama", Model: "m"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/settings/semantic", "test-token", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, other.reloadCalls, "a user-scope PUT must reapply to every other open workspace")

	// A workspace-scope PUT must not touch the others.
	other.reloadCalls = 0
	body, err = json.Marshal(engine.SemanticPutRequest{Enabled: true, Kind: "ollama", Model: "m", Scope: "workspace"})
	require.NoError(t, err)
	resp = doReqBodyReal(t, ts, http.MethodPut, "/v1/settings/semantic", "test-token", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, other.reloadCalls, "a workspace-scope PUT must not reapply to other workspaces")
}

// --- POST /v1/settings/semantic/test --------------------------------------

func TestSettingsSemanticTest_RoundTrip(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	body, err := json.Marshal(domain.SemanticProbe{Kind: "ollama", BaseURL: "http://127.0.0.1:11434", Model: "m"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/settings/semantic/test", "test-token", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out domain.SemanticTestResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.True(t, out.OK)
	assert.True(t, hasRecordedCall(fake, "Settings.TestSemantic"))
}

// TestSettingsSemanticTest_FailureIsStill200 proves a provider-level
// rejection (the fake's TestSemantic returns ok:false for an unsupported
// kind) is still HTTP 200 -- never surfaced as a transport error.
func TestSettingsSemanticTest_FailureIsStill200(t *testing.T) {
	_, ts := newTestServer(t, nil)

	body, err := json.Marshal(domain.SemanticProbe{Kind: "not-a-real-kind", Model: "m"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/settings/semantic/test", "test-token", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out domain.SemanticTestResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.False(t, out.OK)
	assert.NotEmpty(t, out.Error)
}

// --- POST /v1/settings/semantic/reindex -----------------------------------

func TestSettingsSemanticReindex_Returns202(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodPost, "/v1/settings/semantic/reindex", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.True(t, hasRecordedCall(fake, "Settings.ReindexSemantic"))
}

// --- GET /v1/settings/semantic/ollama --------------------------------------

func TestSettingsOllamaStatus_RoundTrip(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/settings/semantic/ollama?base_url=http://127.0.0.1:11434", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out domain.OllamaStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.True(t, out.Reachable)
	assert.NotEmpty(t, out.Models)

	call := recordedCall(t, fake, "Settings.OllamaStatus")
	assert.Equal(t, "http://127.0.0.1:11434", call.Args)
}

// --- POST /v1/settings/semantic/ollama/pull --------------------------------

func TestSettingsOllamaPull_Returns202(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	body, err := json.Marshal(domain.OllamaPullRequest{Model: "nomic-embed-text"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/settings/semantic/ollama/pull", "test-token", body)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.True(t, hasRecordedCall(fake, "Settings.OllamaPull"))
}

// --- test helpers for the multi-workspace fan-out test --------------------

// reloadTrackingEngine wraps an *enginetest.Fake and records how many times
// ReloadSemantic was called on it -- the method
// applySemanticToOtherWorkspaces type-asserts for (semanticReloader), since
// *enginetest.Fake itself has no reason to implement it.
type reloadTrackingEngine struct {
	*enginetest.Fake
	reloadCalls int
}

func (e *reloadTrackingEngine) ReloadSemantic(ctx context.Context) error {
	e.reloadCalls++
	return nil
}

var _ semanticReloader = (*reloadTrackingEngine)(nil)

// newFakeWorkspaceDir writes a minimal workspace file and returns its
// directory, mirroring internal/workspaces' own test helper -- a
// workspaces.Manager always loads the file from disk, even for a workspace
// whose engine is already open in memory.
func newFakeWorkspaceDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	body := "version: 1\nname: " + name + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, domain.WorkspaceFileName), []byte(body), 0o644))
	return dir
}

// newFakeWorkspaceManager builds a workspaces.Manager whose primary is
// primaryWS (adopted, not opened) and which opens every other directory in
// engines to the given fake engine (via a stubbed local.Options.Open),
// registering each with config.AddWorkspace so Manager.Engine accepts a
// request naming it, and opening every one eagerly so Engines() enumerates
// it without a test needing to make a request against it first.
func newFakeWorkspaceManager(t *testing.T, primaryWS *domain.Workspace, engines map[string]engine.Engine) *workspaces.Manager {
	t.Helper()
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	for dir := range engines {
		if dir == primaryWS.Dir {
			continue
		}
		require.NoError(t, config.AddWorkspace(dir))
	}

	mgr := workspaces.New(primaryWS, engines[primaryWS.Dir], workspaces.Options{
		PID: os.Getpid(),
		Open: func(ws *domain.Workspace, _ local.Options) (engine.Engine, error) {
			if e, ok := engines[ws.Dir]; ok {
				return e, nil
			}
			return nil, errs.New(errs.Internal, "no fake engine registered for %s", ws.Dir)
		},
	})
	t.Cleanup(func() { _ = mgr.Close() })

	for dir := range engines {
		if dir == primaryWS.Dir {
			continue
		}
		_, err := mgr.Engine(dir)
		require.NoError(t, err)
	}
	return mgr
}

// newTestServerWithWorkspaces builds a Server over primaryEngine and mgr
// (unlike newTestServer, which always serves a single Fake with no
// Manager), so a test can exercise workspaceMiddleware's Manager path and
// applySemanticToOtherWorkspaces' fan-out.
func newTestServerWithWorkspaces(t *testing.T, primaryEngine engine.Engine, mgr *workspaces.Manager) *httptest.Server {
	t.Helper()
	srv := New(Options{
		Engine:     primaryEngine,
		Workspaces: mgr,
		Token:      "test-token",
		Version:    "1.2.3",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}
