package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// newMultiWorkspaceServer builds a server whose Manager can open a second
// workspace, which is what create/clone need (they register the new
// directory with the Manager).
func newMultiWorkspaceServer(t *testing.T) (*httptest.Server, *workspaces.Manager) {
	t.Helper()
	t.Setenv("SAPIEN_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")

	primaryDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(primaryDir, domain.WorkspaceFileName),
		[]byte("version: 1\nname: primary\n"), 0o644))

	primary := enginetest.New(&domain.Workspace{Version: 1, Name: "primary", Dir: primaryDir})
	mgr := workspaces.New(primary.Workspace(), primary, workspaces.Options{
		Open: func(ws *domain.Workspace, _ local.Options) (engine.Engine, error) {
			return enginetest.New(ws), nil
		},
	})
	t.Cleanup(func() { _ = mgr.Close() })

	srv := New(Options{
		Engine:     primary,
		Workspaces: mgr,
		Token:      "test-token",
		Version:    "1.2.3",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, mgr
}

// initBareRepoWithCommit creates a local bare repository carrying one
// commit, hermetically (no user git config, no network), and returns its
// path and file:// URL.
func initBareRepoWithCommit(t *testing.T) (dir, url string) {
	t.Helper()
	env := append(os.Environ(),
		"HOME="+t.TempDir(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	dir = filepath.Join(t.TempDir(), "repo.git")
	runGit("", "init", "--bare", "-b", "main", dir)

	work := t.TempDir()
	runGit("", "clone", dir, work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "README.md"), []byte("hello\n"), 0o644))
	runGit(work, "add", "-A")
	runGit(work, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "init")
	runGit(work, "push", "origin", "HEAD")

	return dir, "file://" + dir
}

func TestWorkspaceCreate(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	dir := filepath.Join(t.TempDir(), "new-ws")
	body, err := json.Marshal(createWorkspaceRequest{Dir: dir, Name: "created"})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/create", "test-token", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var info workspaces.Info
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&info))
	assert.Equal(t, dir, info.Dir)
	assert.Equal(t, "created", info.Name)
	assert.True(t, info.Open)
	assert.FileExists(t, filepath.Join(dir, domain.WorkspaceFileName))
}

func TestWorkspaceCreate_GitInit(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	dir := filepath.Join(t.TempDir(), "new-ws")
	body, err := json.Marshal(createWorkspaceRequest{Dir: dir, Name: "created", GitInit: true})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/create", "test-token", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.DirExists(t, filepath.Join(dir, ".git"))
}

func TestWorkspaceCreate_NonEmptyDirIsConflict(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644))

	body, err := json.Marshal(createWorkspaceRequest{Dir: dir})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/create", "test-token", body)

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, errs.Conflict, decodeErrBody(t, resp).Code)
}

func TestWorkspaceClone(t *testing.T) {
	_, url := initBareRepoWithCommit(t)
	ts, _ := newMultiWorkspaceServer(t)

	dir := filepath.Join(t.TempDir(), "cloned-ws")
	body, err := json.Marshal(cloneWorkspaceRequest{URL: url, Dir: dir, Name: "cloned"})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var info workspaces.Info
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&info))
	assert.Equal(t, dir, info.Dir)
	assert.Equal(t, "cloned", info.Name)
	assert.FileExists(t, filepath.Join(dir, "README.md"))
	assert.FileExists(t, filepath.Join(dir, domain.WorkspaceFileName))

	// The new workspace shows up in the listing the picker reads.
	listResp := doReq(t, ts, http.MethodGet, "/v1/workspaces", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var list []workspaces.Info
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))

	found := false
	for _, w := range list {
		if w.Dir == dir {
			found = true
		}
	}
	assert.True(t, found, "cloned workspace %s should be listed", dir)
}

func TestWorkspaceClone_InvalidURL(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	body, err := json.Marshal(cloneWorkspaceRequest{URL: "/not/a/git/url", Dir: filepath.Join(t.TempDir(), "x")})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", body)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestFSDirs(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "zeta"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "Alpha"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "node_modules"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".hidden"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644))

	resp := doReq(t, ts, http.MethodGet, "/v1/fs/dirs?path="+url.QueryEscape(root), reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var listing fsDirListing
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listing))
	assert.Equal(t, root, listing.Path)
	assert.Equal(t, filepath.Dir(root), listing.Parent)
	require.Len(t, listing.Entries, 2)
	assert.Equal(t, "Alpha", listing.Entries[0].Name)
	assert.Equal(t, "zeta", listing.Entries[1].Name)
	assert.Equal(t, filepath.Join(root, "Alpha"), listing.Entries[0].Path)
}

func TestFSDirs_EmptyEntriesIsArray(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	root := t.TempDir()
	resp := doReq(t, ts, http.MethodGet, "/v1/fs/dirs?path="+url.QueryEscape(root), reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var listing fsDirListing
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listing))
	require.NotNil(t, listing.Entries)
	assert.Empty(t, listing.Entries)
}

func TestFSDirs_RelativePathRejected(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	resp := doReq(t, ts, http.MethodGet, "/v1/fs/dirs?path=relative/dir", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestWorkspaceCreateClone_SingleWorkspaceServer(t *testing.T) {
	_, ts := newTestServer(t, nil)

	createResp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/create", "test-token", []byte(`{"dir":"/tmp/whatever"}`))
	assert.Equal(t, http.StatusConflict, createResp.StatusCode)
	assert.Equal(t, errs.Conflict, decodeErrBody(t, createResp).Code)

	cloneResp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", []byte(`{"url":"file:///tmp/nope.git","dir":"/tmp/whatever"}`))
	assert.Equal(t, http.StatusConflict, cloneResp.StatusCode)
	assert.Equal(t, errs.Conflict, decodeErrBody(t, cloneResp).Code)
}

// A relative dir would resolve against the daemon's own working directory,
// which no caller means, so it is refused rather than silently made absolute.
func TestWorkspaceCreate_RelativeDirRejected(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	body, err := json.Marshal(createWorkspaceRequest{Dir: "relative/ws", Name: "x"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/create", "test-token", body)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestWorkspaceClone_RelativeDirRejected(t *testing.T) {
	_, url := initBareRepoWithCommit(t)
	ts, _ := newMultiWorkspaceServer(t)

	body, err := json.Marshal(cloneWorkspaceRequest{URL: url, Dir: "relative/ws"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", body)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

// A url starting with "-" is rejected as a URL before git can read it as an
// option (option injection).
func TestWorkspaceClone_LeadingDashURLRejected(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	body, err := json.Marshal(cloneWorkspaceRequest{URL: "-u@host:path", Dir: filepath.Join(t.TempDir(), "x")})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", body)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

// Re-running create on the workspace it already made adopts it instead of
// failing on "not empty", so a retry after a later step failed can succeed.
func TestWorkspaceCreate_AdoptsExistingWorkspace(t *testing.T) {
	ts, _ := newMultiWorkspaceServer(t)

	dir := filepath.Join(t.TempDir(), "new-ws")
	body, err := json.Marshal(createWorkspaceRequest{Dir: dir, Name: "created"})
	require.NoError(t, err)

	first := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/create", "test-token", body)
	require.Equal(t, http.StatusOK, first.StatusCode)

	second := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/create", "test-token", body)
	assert.Equal(t, http.StatusOK, second.StatusCode)
}

// Re-running clone into a completed workspace clone adopts it rather than
// hitting CloneInto's non-empty refusal.
func TestWorkspaceClone_AdoptsCompletedClone(t *testing.T) {
	_, url := initBareRepoWithCommit(t)
	ts, _ := newMultiWorkspaceServer(t)

	dir := filepath.Join(t.TempDir(), "cloned-ws")
	body, err := json.Marshal(cloneWorkspaceRequest{URL: url, Dir: dir, Name: "cloned"})
	require.NoError(t, err)

	first := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", body)
	require.Equal(t, http.StatusOK, first.StatusCode)

	second := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", body)
	assert.Equal(t, http.StatusOK, second.StatusCode)
}

// TestWorkspaceClone_RefusesAnotherRepositorysCheckout: a destination that
// already holds a workspace cloned from a DIFFERENT repository is a name
// collision, not a retry -- adopting it would answer 200 for a URL that was
// never cloned.
func TestWorkspaceClone_RefusesAnotherRepositorysCheckout(t *testing.T) {
	_, firstURL := initBareRepoWithCommit(t)
	_, otherURL := initBareRepoWithCommit(t)
	ts, _ := newMultiWorkspaceServer(t)

	dir := filepath.Join(t.TempDir(), "cloned-ws")
	first, err := json.Marshal(cloneWorkspaceRequest{URL: firstURL, Dir: dir, Name: "cloned"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", first)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	other, err := json.Marshal(cloneWorkspaceRequest{URL: otherURL, Dir: dir, Name: "cloned"})
	require.NoError(t, err)
	resp = doReqBodyReal(t, ts, http.MethodPost, "/v1/workspaces/clone", "test-token", other)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}
