package cli_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/selfupdate"
)

// buildFakeArchive tar.gz's a single "sapien" file containing content, and
// returns the archive bytes plus its sha256 hex digest.
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

// TestUpgrade_NonScriptInstall_PrintsCommandWithoutChangingAnything forces
// a non-script install method the same way handlers_update_test.go does
// (GOBIN pointed at the real test binary's own directory), and proves
// `sapien upgrade` refuses to touch anything: it prints the right command
// and exits 0.
func TestUpgrade_NonScriptInstall_PrintsCommandWithoutChangingAnything(t *testing.T) {
	exe, err := selfupdate.Executable()
	require.NoError(t, err)
	t.Setenv("GOBIN", filepath.Dir(exe))

	stdout, stderr, code := run(t, "upgrade", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, false, got["upgraded"])
	assert.Equal(t, string(selfupdate.MethodGo), got["install_method"])
	assert.Equal(t, "go install github.com/gs-sinha/sapien/cmd/sapien@latest", got["command"])
}

func TestUpgrade_Check_ReportsAvailable(t *testing.T) {
	rel := fakeGitHubReleases(t, "v99.0.0")
	defer cli.SetReleasesBaseURL(rel.URL)()
	// IsNewer(current, latest) never reports an update for current=="dev"
	// (cli.Version's ldflags-unset default, always true under `go test`);
	// a concrete version is what makes "available" meaningful to assert.
	defer setCLIVersion(t, "v1.0.0")()

	stdout, stderr, code := run(t, "upgrade", "--check", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "v99.0.0", got["latest"])
	assert.Equal(t, true, got["available"])
}

func TestUpgrade_Check_UpToDate(t *testing.T) {
	defer setCLIVersion(t, "v1.0.0")()
	rel := fakeGitHubReleases(t, "v1.0.0")
	defer cli.SetReleasesBaseURL(rel.URL)()

	stdout, stderr, code := run(t, "upgrade", "--check")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "up to date")
}

// setCLIVersion temporarily overrides cli.Version (ldflags set it at build
// time; under `go test` it is always the "dev" default), returning a func
// that restores it. DetectMethod treats "dev" as its own install method
// regardless of path, so a test that needs to exercise a *different*
// method against the real (non-dev) test binary path must look like a
// release build's version first.
func setCLIVersion(t *testing.T, v string) func() {
	t.Helper()
	old := cli.Version
	cli.Version = v
	return func() { cli.Version = old }
}

// TestUpgrade_ScriptInstall_DownloadsVerifiesReplacesAndRestarts drives the
// full self-upgrade path against a throwaway target file (never the real
// go-test binary -- see SetUpgradeExecutablePath) and a fake release
// server, with a fake spawnDaemonFunc standing in for the successor
// (SetSpawnDaemonFunc, same reasoning as daemon_restart_test.go), proving
// end to end: download, checksum verification, atomic replace, and
// restarting a daemon that was already running for the resolved workspace.
func TestUpgrade_ScriptInstall_DownloadsVerifiesReplacesAndRestarts(t *testing.T) {
	defer setCLIVersion(t, "v1.9.0")()

	wsDir := t.TempDir()
	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	ws := loadWorkspace(t, wsDir)
	t.Setenv("SAPIEN_WORKSPACE", wsDir)

	oldPID := spawnKillableProcess(t)
	startFakeDaemon(t, ws, cli.Version, oldPID)

	const version = "v2.0.0"
	archiveName := fmt.Sprintf("sapien_2.0.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archive, sum := buildFakeArchive(t, "new sapien binary content")

	mux := http.NewServeMux()
	base := "/releases/download/" + version + "/"
	mux.HandleFunc(base+archiveName, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc(base+"checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, archiveName)
	})
	rel := httptest.NewServer(mux)
	defer rel.Close()
	defer cli.SetReleasesBaseURL(rel.URL)()

	target := filepath.Join(t.TempDir(), "sapien")
	require.NoError(t, os.WriteFile(target, []byte("old sapien binary content"), 0o755))
	defer cli.SetUpgradeExecutablePath(target)()

	newInfo := &daemon.Info{PID: 555555, Port: 5555, Version: cli.Version}
	spawnCalls := 0
	defer cli.SetSpawnDaemonFunc(func(ctx context.Context, ws2 *domain.Workspace, v string) (*daemon.Info, error) {
		spawnCalls++
		return newInfo, nil
	})()

	stdout, stderr, code := run(t, "upgrade", "--version", version, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new sapien binary content", string(data))

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, true, got["upgraded"])
	assert.Equal(t, version, got["version"])
	assert.Equal(t, true, got["restarted"])
	assert.Equal(t, 1, spawnCalls)
	assert.Error(t, syscall.Kill(oldPID, 0), "the old daemon must have been stopped as part of the restart")
}

func TestUpgrade_ChecksumMismatchIsRefused(t *testing.T) {
	defer setCLIVersion(t, "v1.9.0")()

	const version = "v2.0.0"
	archiveName := fmt.Sprintf("sapien_2.0.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archive, _ := buildFakeArchive(t, "content")
	wrongSum := strings.Repeat("0", 64)

	mux := http.NewServeMux()
	base := "/releases/download/" + version + "/"
	mux.HandleFunc(base+archiveName, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc(base+"checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", wrongSum, archiveName)
	})
	rel := httptest.NewServer(mux)
	defer rel.Close()
	defer cli.SetReleasesBaseURL(rel.URL)()

	target := filepath.Join(t.TempDir(), "sapien")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o755))
	defer cli.SetUpgradeExecutablePath(target)()

	_, stderr, code := run(t, "upgrade", "--version", version)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "checksum mismatch")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "old", string(data), "a failed verification must never touch the target file")
}
