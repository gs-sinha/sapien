package selfupdate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/selfupdate"
)

func TestDetectMethod_Homebrew(t *testing.T) {
	cases := []string{
		"/usr/local/Cellar/sapien/1.3.1/bin/sapien",
		"/opt/homebrew/Cellar/sapien/1.3.1/bin/sapien",
		"/home/linuxbrew/.linuxbrew/Cellar/sapien/1.3.1/bin/sapien",
	}
	for _, p := range cases {
		assert.Equal(t, selfupdate.MethodHomebrew, selfupdate.DetectMethod("1.3.1", p), "path %q", p)
	}
}

func TestDetectMethod_Go(t *testing.T) {
	t.Setenv("GOBIN", "")
	gopath := t.TempDir()
	t.Setenv("GOPATH", gopath)

	p := filepath.Join(gopath, "bin", "sapien")
	assert.Equal(t, selfupdate.MethodGo, selfupdate.DetectMethod("1.3.1", p))
}

func TestDetectMethod_GoBin(t *testing.T) {
	gobin := t.TempDir()
	t.Setenv("GOBIN", gobin)

	p := filepath.Join(gobin, "sapien")
	assert.Equal(t, selfupdate.MethodGo, selfupdate.DetectMethod("1.3.1", p))
}

func TestDetectMethod_DevByVersion(t *testing.T) {
	assert.Equal(t, selfupdate.MethodDev, selfupdate.DetectMethod("dev", "/tmp/whatever/sapien"))
}

func TestDetectMethod_DevByModuleCheckout(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", t.TempDir())

	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module github.com/gs-sinha/sapien\n\ngo 1.25\n"), 0o644))
	bin := filepath.Join(repo, "bin", "sapien")
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("x"), 0o755))

	// A git-describe version well past "dev" -- the module-checkout check
	// alone must be enough.
	assert.Equal(t, selfupdate.MethodDev, selfupdate.DetectMethod("v1.3.1-5-gabc1234", bin))
}

func TestDetectMethod_ScriptDefault(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", t.TempDir())

	p := filepath.Join(t.TempDir(), "sapien")
	assert.Equal(t, selfupdate.MethodScript, selfupdate.DetectMethod("v1.3.1", p))
}

func TestDetectMethod_OtherModuleCheckoutIsNotDev(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", t.TempDir())

	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/someone-elses-project\n"), 0o644))
	bin := filepath.Join(repo, "bin", "sapien")
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("x"), 0o755))

	assert.Equal(t, selfupdate.MethodScript, selfupdate.DetectMethod("v1.3.1", bin))
}

func TestCommand(t *testing.T) {
	assert.Equal(t, "brew upgrade gs-sinha/tap/sapien", selfupdate.Command(selfupdate.MethodHomebrew))
	assert.Equal(t, "go install github.com/gs-sinha/sapien/cmd/sapien@latest", selfupdate.Command(selfupdate.MethodGo))
	assert.Equal(t, "git pull && make build", selfupdate.Command(selfupdate.MethodDev))
	assert.Equal(t, "sapien upgrade", selfupdate.Command(selfupdate.MethodScript))
}

func TestCanSelfUpgrade(t *testing.T) {
	assert.True(t, selfupdate.CanSelfUpgrade(selfupdate.MethodScript))
	assert.False(t, selfupdate.CanSelfUpgrade(selfupdate.MethodHomebrew))
	assert.False(t, selfupdate.CanSelfUpgrade(selfupdate.MethodGo))
	assert.False(t, selfupdate.CanSelfUpgrade(selfupdate.MethodDev))
}

// TestExecutable_ReturnsAnExistingFile exercises Executable() itself: under
// `go test`, os.Executable() names the compiled test binary, not a
// symlink, so this cannot prove the symlink-resolution step -- that is
// covered directly by TestDetectMethod's fixtures, which build their own
// symlink-free paths -- only that Executable() resolves to a real,
// absolute file, which is the contract DetectMethod's callers rely on.
func TestExecutable_ReturnsAnExistingFile(t *testing.T) {
	p, err := selfupdate.Executable()
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(p))
	fi, err := os.Stat(p)
	require.NoError(t, err)
	assert.False(t, fi.IsDir())
}
