package gitsrc

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/errs"
)

func TestNew_Defaults(t *testing.T) {
	m := New(Options{})

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".sapien", "repos"), m.cacheDir)
	assert.Equal(t, "git", m.git)
	assert.Equal(t, defaultTimeout, m.timeout)
	require.NotNil(t, m.logger)
}

func TestNew_ExplicitOptions(t *testing.T) {
	logger := slog.Default()
	m := New(Options{
		CacheDir: "/tmp/custom-cache",
		Git:      "/usr/local/bin/git",
		Timeout:  5 * time.Second,
		Logger:   logger,
		Env:      []string{"FOO=bar"},
	})

	assert.Equal(t, "/tmp/custom-cache", m.cacheDir)
	assert.Equal(t, "/usr/local/bin/git", m.git)
	assert.Equal(t, 5*time.Second, m.timeout)
	assert.Same(t, logger, m.logger)
	assert.Equal(t, []string{"FOO=bar"}, m.env)
}

func TestManager_Dir_Stability(t *testing.T) {
	m := New(Options{CacheDir: "/cache"})

	d1 := m.Dir("git@github.com:company/allocation-service.git")
	d2 := m.Dir("git@github.com:company/allocation-service.git")
	assert.Equal(t, d1, d2, "Dir must be stable for the same URL")
	assert.True(t, strings.HasPrefix(d1, "/cache/"))
	assert.Contains(t, d1, "allocation-service")

	other := m.Dir("git@github.com:company/rider-service.git")
	assert.NotEqual(t, d1, other, "different URLs must map to different dirs")
}

func TestManager_Dir_EmptySlugFallsBackToRepo(t *testing.T) {
	m := New(Options{CacheDir: "/cache"})
	// A URL whose slug extraction yields nothing meaningful still produces a
	// stable, non-empty directory name.
	d := m.Dir("file:///")
	assert.True(t, strings.HasSuffix(d, "-repo"))
}

func TestIsGitURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"scp-like", "git@github.com:company/allocation-service.git", true},
		{"scp-like no .git suffix", "git@github.com:company/allocation-service", true},
		{"ssh scheme", "ssh://git@github.com/company/allocation-service.git", true},
		{"https scheme", "https://github.com/company/allocation-service.git", true},
		{"https no suffix", "https://github.com/company/allocation-service", true},
		{"http scheme", "http://internal.example.com/repo.git", true},
		{"git scheme", "git://github.com/company/allocation-service.git", true},
		{"file scheme", "file:///tmp/bare-repos/allocation-service.git", true},
		{"uppercase scheme", "HTTPS://GITHUB.COM/company/repo.git", true},
		{"empty", "", false},
		{"absolute path", "/Users/dev/code/allocation-service", false},
		{"relative path", "../allocation-service", false},
		{"tilde path", "~/code/allocation-service", false},
		{"windows path", `C:\Users\dev\allocation-service`, false},
		{"bare name", "allocation-service", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsGitURL(tc.url))
		})
	}
}

func TestRepoSlug(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"git@github.com:company/allocation-service.git", "allocation-service"},
		{"https://github.com/company/allocation-service.git", "allocation-service"},
		{"https://github.com/company/allocation-service", "allocation-service"},
		{"file:///tmp/bare-repos/order-service.git", "order-service"},
		{"ssh://git@host:2222/org/Rider_Service.git", "rider-service"},
		{"file:///", ""},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			assert.Equal(t, tc.want, repoSlug(tc.url))
		})
	}
}

func TestSlugify(t *testing.T) {
	assert.Equal(t, "allocation-service", slugify("Allocation_Service!!"))
	assert.Equal(t, "", slugify("---"))
	assert.Equal(t, "a-b", slugify("a...b"))
}

func TestBuildEnv_ForcesTerminalPromptOff(t *testing.T) {
	env := buildEnv([]string{"GIT_TERMINAL_PROMPT=1", "FOO=bar"})

	found := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			found[kv[:i]] = kv[i+1:]
		}
	}
	assert.Equal(t, "0", found["GIT_TERMINAL_PROMPT"], "GIT_TERMINAL_PROMPT must always be forced to 0")
	assert.Equal(t, "bar", found["FOO"])

	// No duplicate keys.
	seen := map[string]int{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			seen[kv[:i]]++
		}
	}
	for k, n := range seen {
		assert.Equalf(t, 1, n, "key %s appeared %d times", k, n)
	}
}

func TestHintFor(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   string
	}{
		{"publickey", "Permission denied (publickey).", "check your SSH agent or credential helper; Sapien stores no credentials"},
		{"auth failed", "remote: HTTP Basic: Access denied\nfatal: Authentication failed for 'https://example.com/repo.git/'", "check your SSH agent or credential helper; Sapien stores no credentials"},
		{"terminal disabled", "fatal: could not read Username for 'https://example.com': terminal prompts disabled", "check your SSH agent or credential helper; Sapien stores no credentials"},
		{"unknown ref", "error: pathspec 'no-such-branch' did not match any file(s) known to git", "unknown ref: check the branch, tag, or commit configured for this service"},
		{"unknown revision", "fatal: unknown revision or path not in the working tree.", "unknown ref: check the branch, tag, or commit configured for this service"},
		{"repo not found", "remote: Repository not found.\nfatal: repository 'https://example.com/nope.git/' not found", "check the repository URL and that you have access to it"},
		{"does not exist", "fatal: '/no/such/path' does not exist", "check the repository URL and that you have access to it"},
		{"not a git repo", "fatal: not a git repository (or any of the parent directories): .git", "not a git repository"},
		{"unrecognized", "fatal: something else entirely went wrong", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hintFor(tc.stderr))
		})
	}
}

func TestWithDetail(t *testing.T) {
	assert.Nil(t, withDetail(nil, "k", "v"))

	err := errs.New(errs.ServiceSource, "boom")
	wrapped := withDetail(err, "url", "https://example.com/repo.git")
	e := errs.As(wrapped)
	assert.Equal(t, "https://example.com/repo.git", e.Details["url"])
}

func TestRun_ContextAlreadyDone(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.run(ctx, "", "version")
	require.Error(t, err)
	assert.Equal(t, errs.Cancelled, errs.CodeOf(err))
}

func TestRun_TimeoutExceeded(t *testing.T) {
	m := New(Options{Timeout: 1 * time.Nanosecond, Env: hermeticGitEnv(t)})

	_, err := m.run(context.Background(), "", "version")
	require.Error(t, err)
	assert.Equal(t, errs.Cancelled, errs.CodeOf(err))
}

func TestRun_CommandFailure_ServiceSource(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))

	_, err := m.run(context.Background(), "", "not-a-real-git-subcommand")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
	e := errs.As(err)
	assert.NotEmpty(t, e.Details["stderr"])
}
