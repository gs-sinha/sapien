package selfupdate_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/selfupdate"
)

// buildFakeArchive tar.gz's a single "sapien" file containing content, and
// returns the archive bytes plus its sha256 hex digest.
func buildFakeArchive(t *testing.T, content string) (archive []byte, sha256Hex string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "sapien",
		Mode: 0o755,
		Size: int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// serveFakeRelease starts an httptest.Server that serves
// /releases/download/<version>/<archiveName> and .../checksums.txt, with
// checksums.txt's entry for archiveName taken from checksumHex (letting a
// test deliberately corrupt it to prove the mismatch is caught).
func serveFakeRelease(t *testing.T, version, archiveName string, archive []byte, checksumHex string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	base := "/releases/download/" + version + "/"
	mux.HandleFunc(base+archiveName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc(base+"checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", checksumHex, archiveName)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestApply_DownloadsVerifiesExtractsAndReplaces(t *testing.T) {
	const version, goos, goarch = "v1.4.0", "linux", "amd64"
	archiveName := fmt.Sprintf("sapien_%s_%s_%s.tar.gz", "1.4.0", goos, goarch)

	archive, sum := buildFakeArchive(t, "new sapien binary content")
	ts := serveFakeRelease(t, version, archiveName, archive, sum)

	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "sapien")
	require.NoError(t, os.WriteFile(target, []byte("old sapien binary content"), 0o755))

	result, err := selfupdate.Apply(context.Background(), selfupdate.ApplyOptions{
		Version:        version,
		BaseURL:        ts.URL,
		GOOS:           goos,
		GOARCH:         goarch,
		ExecutablePath: target,
	})
	require.NoError(t, err)
	assert.Equal(t, version, result.Version)
	assert.Equal(t, target, result.Executable)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new sapien binary content", string(data))

	fi, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
}

func TestApply_ChecksumMismatchIsRefused(t *testing.T) {
	const version, goos, goarch = "v1.4.0", "linux", "amd64"
	archiveName := fmt.Sprintf("sapien_%s_%s_%s.tar.gz", "1.4.0", goos, goarch)

	archive, _ := buildFakeArchive(t, "new sapien binary content")
	wrongSum := strings.Repeat("0", 64)
	ts := serveFakeRelease(t, version, archiveName, archive, wrongSum)

	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "sapien")
	require.NoError(t, os.WriteFile(target, []byte("old sapien binary content"), 0o755))

	_, err := selfupdate.Apply(context.Background(), selfupdate.ApplyOptions{
		Version:        version,
		BaseURL:        ts.URL,
		GOOS:           goos,
		GOARCH:         goarch,
		ExecutablePath: target,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")

	// The old binary must be untouched: a failed verification refuses to
	// install anything at all.
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "old sapien binary content", string(data))
}

func TestApply_MissingChecksumEntryIsRefused(t *testing.T) {
	const version, goos, goarch = "v1.4.0", "linux", "amd64"
	archiveName := fmt.Sprintf("sapien_%s_%s_%s.tar.gz", "1.4.0", goos, goarch)
	otherName := fmt.Sprintf("sapien_%s_%s_%s.tar.gz", "1.4.0", "darwin", "arm64")

	archive, sum := buildFakeArchive(t, "content")

	mux := http.NewServeMux()
	base := "/releases/download/" + version + "/"
	mux.HandleFunc(base+archiveName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	// checksums.txt is reachable, but names a different archive entirely --
	// exactly what a mismatched release/platform combination would produce.
	mux.HandleFunc(base+"checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, otherName)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	target := filepath.Join(t.TempDir(), "sapien")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o755))

	_, err := selfupdate.Apply(context.Background(), selfupdate.ApplyOptions{
		Version: version, BaseURL: ts.URL, GOOS: goos, GOARCH: goarch, ExecutablePath: target,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no checksum entry")
}

func TestApply_ResolvesLatestWhenVersionEmpty(t *testing.T) {
	const goos, goarch = "linux", "amd64"
	archiveName := fmt.Sprintf("sapien_%s_%s_%s.tar.gz", "1.6.0", goos, goarch)

	archive, sum := buildFakeArchive(t, "content")
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/v1.6.0", http.StatusFound)
	})
	mux.HandleFunc("/releases/download/v1.6.0/"+archiveName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/releases/download/v1.6.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, archiveName)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	target := filepath.Join(t.TempDir(), "sapien")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o755))

	result, err := selfupdate.Apply(context.Background(), selfupdate.ApplyOptions{
		BaseURL: ts.URL, GOOS: goos, GOARCH: goarch, ExecutablePath: target,
	})
	require.NoError(t, err)
	assert.Equal(t, "v1.6.0", result.Version)
}
