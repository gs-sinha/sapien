package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gs-sinha/sapien/internal/errs"
)

// ApplyOptions configures Apply.
type ApplyOptions struct {
	// Version is the release tag to install, e.g. "v1.4.0". Empty means
	// "whatever Latest reports right now".
	Version string
	// BaseURL overrides defaultReleasesBase for both the release-download
	// URLs and (when Version is empty) the Latest lookup, so a test can
	// point Apply at an httptest.Server serving a fake tarball and
	// checksums.txt.
	BaseURL string
	Client  *http.Client
	// GOOS/GOARCH override runtime.GOOS/runtime.GOARCH, for a test that
	// wants a deterministic asset name without depending on the platform
	// the test happens to run on.
	GOOS, GOARCH string
	// ExecutablePath overrides Executable() as the file Apply replaces.
	ExecutablePath string
}

// ApplyResult is what Apply installed.
type ApplyResult struct {
	Version    string
	Executable string
}

// Apply downloads a release archive and checksums.txt (asset naming
// mirrors scripts/install.sh and .goreleaser.yaml:
// "sapien_<version>_<os>_<arch>.tar.gz"), verifies the archive's sha256
// against the matching checksums.txt entry, extracts the sapien binary, and
// replaces ExecutablePath (or the running binary) with it via a
// same-directory temp file plus atomic rename -- safe even while the
// binary being replaced is the one currently running. It does not restart
// anything; callers (the `sapien upgrade` CLI command, POST
// /v1/update/apply) do that themselves once Apply returns.
func Apply(ctx context.Context, opts ApplyOptions) (*ApplyResult, error) {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}

	version := opts.Version
	if version == "" {
		latest, err := Latest(ctx, LatestOptions{BaseURL: opts.BaseURL, Client: client})
		if err != nil {
			return nil, errs.Wrap(errs.Internal, err, "resolving the latest release")
		}
		version = latest
	}

	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	versionNum := strings.TrimPrefix(version, "v")
	archiveName := fmt.Sprintf("sapien_%s_%s_%s.tar.gz", versionNum, goos, goarch)
	base := releasesBase(opts.BaseURL) + "/releases/download/" + version

	tmpDir, err := os.MkdirTemp("", "sapien-upgrade-*")
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "creating a temp directory")
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, archiveName)
	if err := downloadFile(ctx, client, base+"/"+archiveName, archivePath); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "downloading %s", archiveName)
	}
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := downloadFile(ctx, client, base+"/checksums.txt", checksumsPath); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "downloading checksums.txt")
	}

	if err := verifyChecksum(archivePath, checksumsPath, archiveName); err != nil {
		return nil, err
	}

	binPath, err := extractBinary(archivePath, tmpDir)
	if err != nil {
		return nil, err
	}

	target := opts.ExecutablePath
	if target == "" {
		target, err = Executable()
		if err != nil {
			return nil, err
		}
	}
	if err := replaceBinary(binPath, target); err != nil {
		return nil, err
	}

	return &ApplyResult{Version: version, Executable: target}, nil
}

// downloadFile GETs url and writes its body to dest, failing on any
// non-200 status.
func downloadFile(ctx context.Context, client *http.Client, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// verifyChecksum finds archiveName's entry in checksumsPath (goreleaser's
// `sha256sum`-style "<hex>  <filename>" lines, one per archive) and
// compares it against archivePath's own sha256, refusing a mismatch.
func verifyChecksum(archivePath, checksumsPath, archiveName string) error {
	data, err := os.ReadFile(checksumsPath)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "reading checksums.txt")
	}

	var want string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archiveName {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return errs.New(errs.Invalid, "no checksum entry for %s in checksums.txt", archiveName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "opening %s", archivePath)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return errs.Wrap(errs.Internal, err, "hashing %s", archivePath)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return errs.New(errs.Invalid, "checksum mismatch for %s: got %s, want %s", archiveName, got, want).
			WithDetail("archive", archiveName).WithDetail("got", got).WithDetail("want", want)
	}
	return nil
}

// extractBinary reads the "sapien" entry out of the tar.gz at archivePath
// and writes it (mode 0755) under destDir, returning its path.
func extractBinary(archivePath, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "opening %s", archivePath)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "opening gzip stream in %s", archivePath)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "reading tar archive")
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "sapien" {
			continue
		}

		out := filepath.Join(destDir, "sapien")
		wf, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "writing %s", out)
		}
		if _, err := io.Copy(wf, tr); err != nil {
			_ = wf.Close()
			return "", errs.Wrap(errs.Internal, err, "extracting sapien binary")
		}
		if err := wf.Close(); err != nil {
			return "", errs.Wrap(errs.Internal, err, "closing %s", out)
		}
		return out, nil
	}
	return "", errs.New(errs.Invalid, "release archive has no sapien binary")
}

// replaceBinary copies src over target: a temp file in target's own
// directory (so the final rename is same-filesystem, hence atomic),
// chmod 0755, then rename. Renaming over a file that is currently
// executing (target may be this very process's own binary) is safe on
// both Linux and macOS: the running process keeps its already-open inode:
// only the directory entry changes.
func replaceBinary(src, target string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "reading %s", src)
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".sapien.*.tmp")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "creating temp file in %s", dir)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errs.Wrap(errs.Internal, err, "writing %s", tmpName)
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "closing %s", tmpName)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "chmod %s", tmpName)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return errs.Wrap(errs.Internal, err, "renaming %s to %s", tmpName, target)
	}
	return nil
}
