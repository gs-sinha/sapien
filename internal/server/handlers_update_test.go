package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/selfupdate"
)

// isolateHome points $HOME (and, so config.Load never falls through to a
// real file either, $SAPIEN_CONFIG) at fresh temp directories, so a test
// that reaches config.Load or selfupdate's cache/token paths (both derived
// from config.UserDir, i.e. $HOME) never touches the developer's real
// ~/.sapien.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SAPIEN_CONFIG", filepath.Join(home, ".sapien", "config.yaml"))
}

// fakeGitHubReleases mirrors selfupdate's own test helper: a releases/latest
// redirect to releases/tag/<tag>.
func fakeGitHubReleases(t *testing.T, latestTag string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+latestTag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>release page</html>"))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestUpdateGet_NeverCheckedYet(t *testing.T) {
	isolateHome(t)
	_, ts := newTestServer(t, func(o *Options) { o.Version = "v1.0.0" })

	resp := doReq(t, ts, http.MethodGet, "/v1/update", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got updateInfoResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "v1.0.0", got.Current)
	assert.Empty(t, got.Latest)
	assert.False(t, got.Available)
	assert.True(t, got.CheckedAt.IsZero())
	assert.True(t, got.CheckEnabled, "checking is on by default")
	assert.NotEmpty(t, got.InstallMethod)
	assert.NotEmpty(t, got.Command)
}

func TestUpdateGet_CheckEnabledReflectsConfig(t *testing.T) {
	isolateHome(t)
	require.NoError(t, config.SetUpdatesCheck(false))

	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/update", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got updateInfoResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.False(t, got.CheckEnabled)
}

func TestUpdateCheck_PopulatesLatestAndAvailable(t *testing.T) {
	isolateHome(t)
	rel := fakeGitHubReleases(t, "v9.9.9")

	_, ts := newTestServer(t, func(o *Options) {
		o.Version = "v1.0.0"
		o.UpdateBaseURL = rel.URL
	})

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/update/check", "test-token", []byte(`{}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got updateInfoResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "v9.9.9", got.Latest)
	assert.True(t, got.Available)
	assert.Equal(t, "https://github.com/gs-sinha/sapien/releases/tag/v9.9.9", got.ReleaseURL)
	assert.False(t, got.CheckedAt.IsZero())

	// GET afterwards reads the now-populated cache without hitting the
	// network again.
	rel.Close()
	resp = doReq(t, ts, http.MethodGet, "/v1/update", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got2 updateInfoResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got2))
	assert.Equal(t, "v9.9.9", got2.Latest)
}

func TestUpdateCheck_NetworkFailureReportsErrorNot500(t *testing.T) {
	isolateHome(t)
	rel := fakeGitHubReleases(t, "v9.9.9")
	rel.Close() // nothing listening

	_, ts := newTestServer(t, func(o *Options) { o.UpdateBaseURL = rel.URL })

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/update/check", "test-token", []byte(`{}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got updateInfoResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.NotEmpty(t, got.Error)
}

func TestUpdateSettingsSet_TogglesCheckEnabled(t *testing.T) {
	isolateHome(t)
	_, ts := newTestServer(t, nil)

	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/settings/updates", "test-token", []byte(`{"check": false}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got updateInfoResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.False(t, got.CheckEnabled)

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.False(t, cfg.Updates.Enabled())

	resp = doReqBodyReal(t, ts, http.MethodPut, "/v1/settings/updates", "test-token", []byte(`{"check": true}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.True(t, got.CheckEnabled)
}

// --- POST /v1/update/apply ---
//
// The go-test binary that runs these tests resolves to install method
// "script" in this environment (nothing GOBIN/GOPATH/Cellar-shaped sits
// above it) -- so, unlike a real script install, the *happy* path here
// would have selfupdate.Apply replace the actual running test binary on
// disk, since handleUpdateApply always targets Executable() and there is
// no seam to redirect it. That is out of scope for a unit test even though
// it is technically safe (Unix keeps a running process's already-open
// inode valid across the rename); internal/selfupdate's own apply_test.go
// already covers Apply's download/verify/extract/replace behavior in full
// against a throwaway target file. What is tested here is what the handler
// adds on top: the install-method gate (forced non-script via GOBIN, since
// nothing else about the real test binary's path is controllable), and
// that an Apply failure that happens before any file is touched (a
// checksum mismatch) is reported as a normal error response rather than
// left half-applied.
func TestUpdateApply_NonScriptInstallIs409(t *testing.T) {
	isolateHome(t)

	exe, err := selfupdate.Executable()
	require.NoError(t, err)
	t.Setenv("GOBIN", filepath.Dir(exe)) // makes DetectMethod see MethodGo, not MethodScript

	_, ts := newTestServer(t, nil)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/update/apply", "test-token", []byte(`{}`))
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	e := decodeErrBody(t, resp)
	assert.Equal(t, errs.Conflict, e.Code)
	assert.Equal(t, string(selfupdate.MethodGo), e.Details["install_method"])
}

func TestUpdateApply_ChecksumMismatchIsReportedNotPanicked(t *testing.T) {
	isolateHome(t)

	archiveName := fmt.Sprintf("sapien_1.2.3_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archive, _ := buildFakeArchive(t, "irrelevant content")
	wrongSum := strings.Repeat("0", 64)

	mux := http.NewServeMux()
	base := "/releases/download/v1.2.3/"
	mux.HandleFunc(base+archiveName, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc(base+"checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", wrongSum, archiveName)
	})
	rel := httptest.NewServer(mux)
	defer rel.Close()

	_, ts := newTestServer(t, func(o *Options) {
		o.Version = "1.2.3"
		o.UpdateBaseURL = rel.URL
	})

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/update/apply", "test-token", []byte(`{"version": "v1.2.3"}`))
	// The install method here is genuinely "script" (see the file doc
	// comment), so this reaches selfupdate.Apply and fails there.
	require.NotEqual(t, http.StatusAccepted, resp.StatusCode)
	e := decodeErrBody(t, resp)
	assert.Contains(t, e.Message, "checksum mismatch")
}

func buildFakeArchive(t *testing.T, content string) (archive []byte, sha256Hex string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "sapien", Mode: 0o755, Size: int64(len(content))}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}
