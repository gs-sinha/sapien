package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
)

// --- service add: git-URL detection builds a domain.Source{Kind: git, ...},
// recorded verbatim by the Fake's Add. ---

func TestServiceAdd_GitURL_SSHShorthand(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "service", "add", "git@github.com:acme/widgets.git",
		"--name", "widgets", "--ref", "main", "--subdir", "api", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	src := got["source"].(map[string]any)
	assert.Equal(t, "git", src["type"])
	assert.Equal(t, "git@github.com:acme/widgets.git", src["url"])
	assert.Equal(t, "main", src["ref"])
	assert.Equal(t, "api", src["subdir"])

	call := lastCall(fake, "Services.Add")
	args := call.Args.(map[string]any)
	src2 := args["source"].(domain.Source)
	assert.Equal(t, domain.SourceGit, src2.Kind)
	assert.Equal(t, "main", src2.Ref)
	assert.Equal(t, "api", src2.Subdir)
}

func TestServiceAdd_GitURL_SSHScheme(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "ssh://git@github.com/acme/widgets.git", "--name", "w2", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceGit, src.Kind)
}

func TestServiceAdd_GitURL_HTTPS(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "https://github.com/acme/widgets.git", "--name", "w3", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceGit, src.Kind)
}

func TestServiceAdd_HTTPSWithoutGitSuffix_IsGit(t *testing.T) {
	// No ".git" suffix: looksLikeGitURL's https:// branch requires it, so
	// this falls through to the "local path" branch even though it's a URL.
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "https://example.com/not-git", "--name", "w4", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceGit, src.Kind)
	assert.Equal(t, "https://example.com/not-git", src.URL)
}

func TestServiceAdd_LocalPath(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "./my-service", "--name", "w5", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceLocal, src.Kind)
}

func TestServiceAdd_Conflict(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "./order-service", "--name", "order-service", "--json")
	assert.Equal(t, 2, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_CONFLICT", got["code"])

	stdout2, stderr2, code2 := run(t, "--workspace", dir, "service", "add", "./order-service", "--name", "order-service")
	assert.Equal(t, 2, code2)
	assert.Empty(t, stdout2)
	assert.Contains(t, stderr2, "E_CONFLICT")
}

// lastCall returns the most recent recorded Fake call with the given method
// name, failing the test if none was recorded.
func lastCall(fake *enginetest.Fake, method string) enginetest.Call {
	for i := len(fake.Calls) - 1; i >= 0; i-- {
		if fake.Calls[i].Method == method {
			return fake.Calls[i]
		}
	}
	panic("no recorded call for " + method)
}

// --- service list: human table, including both Local and Git SOURCE
// rendering ---

func TestServiceList_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, _, code := run(t, "--workspace", dir, "service", "add", "git@github.com:acme/widgets.git", "--name", "widgets")
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "service", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "order-service")
	assert.Contains(t, stdout, "services/order-service") // local source path from Seed
	assert.Contains(t, stdout, "widgets")
	assert.Contains(t, stdout, "git@github.com:acme/widgets.git") // git source URL
}

// --- service sync ---

func TestServiceSync_All(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderrOut, code := run(t, "--workspace", dir, "service", "sync", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderrOut)
	var svcs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &svcs))
	assert.NotEmpty(t, svcs)

	stdoutHuman, stderrOut, code := run(t, "--workspace", dir, "service", "sync")
	require.Equal(t, 0, code, "stderr: %s", stderrOut)
	assert.Contains(t, stdoutHuman, "order-service")
}

func TestServiceSync_ByName(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "service", "sync", "order-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var svcs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &svcs))
	require.Len(t, svcs, 1)
	assert.Equal(t, "order-service", svcs[0]["name"])
}

func TestServiceSync_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "sync", "no-such-service", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_SERVICE_NOT_FOUND", got["code"])

	_, stderr2, code2 := run(t, "--workspace", dir, "service", "sync", "no-such-service")
	assert.Equal(t, 2, code2)
	assert.Contains(t, stderr2, "E_SERVICE_NOT_FOUND")
}

// --- service remove ---

func TestServiceRemove_Success(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "service", "remove", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "order-service")

	stdout2, stderr2, code2 := run(t, "--workspace", dir, "service", "remove", "order-service", "--json")
	assert.Equal(t, 2, code2)
	assert.Empty(t, stdout2)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr2), &got))
	assert.Equal(t, "E_SERVICE_NOT_FOUND", got["code"])
}

func TestServiceRemove_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "remove", "nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_SERVICE_NOT_FOUND", got["code"])
}

// --- reindex: human mode, and --json via the Fake (which has no Stats()) ---

func TestReindex_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "reindex")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "order-service")
	assert.Contains(t, stdout, "rider-service")
	assert.Contains(t, stdout, "services:")
}

func TestReindex_JSON_Fake(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "reindex", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Contains(t, got, "services")
	assert.Contains(t, got, "stats")
}

// --- service bind / unbind and the READS column ---

func TestServiceBind_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	checkout := t.TempDir()
	stdout, stderr, code := run(t, "--workspace", dir, "service", "bind", "order-service", checkout, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	binding := got["binding"].(map[string]any)
	assert.Equal(t, "local", binding["mode"])
	assert.Equal(t, true, binding["writable"])

	call := lastCall(fake, "Services.Bind")
	args := call.Args.(map[string]any)
	assert.Equal(t, "order-service", args["name"])
	assert.Equal(t, checkout, args["path"])
}

func TestServiceBind_RelativePathIsResolvedAgainstCwd(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "bind", "order-service", "./checkout")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	path := lastCall(fake, "Services.Bind").Args.(map[string]any)["path"].(string)
	assert.True(t, filepath.IsAbs(path), "expected an absolute path, got %q", path)
	assert.Equal(t, "checkout", filepath.Base(path))
}

func TestServiceBind_Human_ThenUnbind(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, _, code := run(t, "--workspace", dir, "service", "add", "git@github.com:acme/widgets.git", "--name", "widgets", "--ref", "main")
	require.Equal(t, 0, code)

	checkout := t.TempDir()
	stdout, stderr, code := run(t, "--workspace", dir, "service", "bind", "widgets", checkout)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "widgets: reads local at "+checkout)
	assert.Contains(t, stdout, "git@github.com:acme/widgets.git")
	assert.Contains(t, stdout, "writable")

	stdout, stderr, code = run(t, "--workspace", dir, "service", "unbind", "widgets")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "widgets: reads team main from git@github.com:acme/widgets.git")
	assert.Contains(t, stdout, "read-only")
}

func TestServiceBind_UnknownService(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "bind", "nope", t.TempDir(), "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_SERVICE_NOT_FOUND", got["code"])
}

func TestServiceList_ReadsColumn(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, _, code := run(t, "--workspace", dir, "service", "add", "git@github.com:acme/widgets.git", "--name", "widgets", "--ref", "main")
	require.Equal(t, 0, code)
	_, _, code = run(t, "--workspace", dir, "service", "add", "git@github.com:acme/gadgets.git", "--name", "gadgets")
	require.Equal(t, 0, code)
	_, _, code = run(t, "--workspace", dir, "service", "bind", "gadgets", t.TempDir())
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "service", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.NotEmpty(t, lines)
	header := strings.Fields(lines[0])
	require.GreaterOrEqual(t, len(header), 2)
	assert.Equal(t, "NAME", header[0])
	assert.Equal(t, "READS", header[1], "READS sits right after NAME")

	reads := map[string]string{}
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		cell := f[1]
		if f[1] == "team" || f[1] == "local" {
			// "team main" / "local feature" span two fields; a bare "local"
			// is followed by the STATUS column.
			if f[2] != "ok" && f[2] != "error" && f[2] != "pending" {
				cell += " " + f[2]
			}
		}
		reads[f[0]] = cell
	}
	assert.Equal(t, "local", reads["order-service"], "a committed local source reads local")
	assert.Equal(t, "team main", reads["widgets"])
	assert.Equal(t, "local", reads["gadgets"], "bound to a checkout git cannot describe")
}

// --- service add: the team-aware default (AddFromCheckout) for a local
// path in a shared workspace, and its --local / --team overrides
// (PLAN §7b). ---

// hermeticGitEnvCLI mirrors the hermetic git test environment used
// throughout the repo's other git test suites (e.g.
// internal/gitsrc/testutil_test.go): a throwaway HOME plus a forced-off
// global git config, so these tests never touch the developer's real git
// configuration or the network.
func hermeticGitEnvCLI(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"HOME=" + home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	}
}

// applyHermeticEnv sets every KEY=value pair from a hermeticGitEnvCLI slice
// as the test process's own environment (via t.Setenv, restored
// automatically), so the app under test's own git invocations (through
// gitsrc.Manager, which merges Options.Env over os.Environ()) are just as
// hermetic as the test's own runGitCLI calls, not only sharing HOME.
func applyHermeticEnv(t *testing.T, env []string) {
	t.Helper()
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		require.True(t, ok)
		t.Setenv(k, v)
	}
}

func runGitCLI(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s (dir=%q): %v\n%s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// makeSharedWorkspace turns a `sapien init`-created workspace directory
// into one workspace.IsShared reports true for: a git repository with the
// committed file tracked and a remote configured -- the README
// quickstart's own inverse (a scratch workspace created *inside* another
// repo, whose file is never committed, must keep reading as unshared, see
// shared_test.go in internal/workspace).
func makeSharedWorkspace(t *testing.T, dir string, env []string) {
	t.Helper()
	runGitCLI(t, dir, env, "init", "-b", "main")
	runGitCLI(t, dir, env, "add", "sapien.workspace.yaml")
	runGitCLI(t, dir, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "init")
	runGitCLI(t, dir, env, "remote", "add", "origin", "git@example.com:acme/workspace.git")
}

func TestServiceAdd_TeamAware_SharedWorkspace(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	makeSharedWorkspace(t, dir, hermeticGitEnvCLI(t))

	checkout := t.TempDir()
	stdout, stderr, code := run(t, "--workspace", dir, "service", "add", checkout, "--name", "new-service", "--ref", "main", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	call := lastCall(fake, "Services.AddFromCheckout")
	args := call.Args.(map[string]any)
	assert.Equal(t, "new-service", args["name"])
	assert.Equal(t, checkout, args["path"])
	assert.Equal(t, "main", args["ref"])
	assert.Equal(t, false, args["force"])

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "new-service", got["name"])
}

func TestServiceAdd_LocalFlagOverridesTeamAware(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	makeSharedWorkspace(t, dir, hermeticGitEnvCLI(t))

	checkout := t.TempDir()
	_, stderr, code := run(t, "--workspace", dir, "service", "add", checkout, "--name", "new-service", "--local")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceLocal, src.Kind)
	assert.Equal(t, checkout, src.Path)

	for _, c := range fake.Calls {
		assert.NotEqual(t, "Services.AddFromCheckout", c.Method, "--local must skip the team-aware path entirely")
	}
}

func TestServiceAdd_NotSharedWorkspace_UsesPlainAdd(t *testing.T) {
	// setupFakeEngine's workspace directory is a plain temp dir, never a
	// git repository: IsShared is false, so a local path is committed
	// exactly as before.
	dir, fake := setupFakeEngine(t)

	checkout := t.TempDir()
	_, stderr, code := run(t, "--workspace", dir, "service", "add", checkout, "--name", "new-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceLocal, src.Kind)
}

// --- service bind: inferred name (one argument) ---

func TestServiceBind_OneArgInfersName(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	checkout := t.TempDir()

	_, stderr, code := run(t, "--workspace", dir, "service", "bind", "order-service", checkout)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", dir, "service", "bind", checkout, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "order-service", got["name"], "the name was inferred from the checkout already bound to this path")
}

func TestServiceBind_ForceFlagPassedThrough(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	checkout := t.TempDir()

	_, stderr, code := run(t, "--workspace", dir, "service", "bind", "order-service", checkout, "--force", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// The fake's Bind has nothing to validate, so this only proves the flag
	// reaches the engine without erroring; the real validation and its
	// exact error messages are covered against the real engine in
	// internal/engine/local/binding_test.go.
	call := lastCall(fake, "Services.Bind")
	assert.Equal(t, "order-service", call.Args.(map[string]any)["name"])
}

// --- service bind --force against a real, mismatched origin: the Fake
// above never validates anything, so this exercises the real engine
// (SAPIEN_NO_DAEMON=1) to check the exact wording the UI depends on. ---

func TestServiceBind_RealEngine_OriginMismatch(t *testing.T) {
	env := hermeticGitEnvCLI(t)

	teamBare := filepath.Join(t.TempDir(), "team.git")
	runGitCLI(t, "", env, "init", "--bare", "-b", "main", teamBare)
	teamURL := "file://" + teamBare
	seedMinimalPackage(t, teamURL, env)

	otherBare := filepath.Join(t.TempDir(), "other.git")
	runGitCLI(t, "", env, "init", "--bare", "-b", "main", otherBare)
	otherWork := t.TempDir()
	runGitCLI(t, "", env, "clone", otherBare, otherWork)
	require.NoError(t, os.WriteFile(filepath.Join(otherWork, "README.md"), []byte("other\n"), 0o644))
	runGitCLI(t, otherWork, env, "add", "-A")
	runGitCLI(t, otherWork, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "init")
	runGitCLI(t, otherWork, env, "push", "origin", "HEAD")

	dir := t.TempDir()
	_, stderr, code := run(t, "init", dir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	t.Setenv("SAPIEN_NO_DAEMON", "1")
	applyHermeticEnv(t, env)

	_, stderr, code = run(t, "--workspace", dir, "service", "add", teamURL, "--name", "order-service", "--ref", "main")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	mismatched := filepath.Join(t.TempDir(), "clone")
	runGitCLI(t, "", env, "clone", otherBare, mismatched)

	_, stderr, code = run(t, "--workspace", dir, "service", "bind", "order-service", mismatched)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "is a clone of "+otherBare)
	assert.Contains(t, stderr, "not of "+teamURL)
	assert.Contains(t, stderr, "--force")
}

// seedMinimalPackage pushes a minimal, valid api/openapi.yaml (order-service
// style) to bareURL's default branch, so a service registered against it
// indexes successfully.
func seedMinimalPackage(t *testing.T, bareURL string, env []string) {
	t.Helper()
	work := t.TempDir()
	runGitCLI(t, "", env, "clone", bareURL, work)
	apiDir := filepath.Join(work, "api")
	require.NoError(t, os.MkdirAll(apiDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(apiDir, "openapi.yaml"), []byte(minimalOpenAPI), 0o644))
	runGitCLI(t, work, env, "add", "-A")
	runGitCLI(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "seed")
	runGitCLI(t, work, env, "push", "origin", "HEAD")
}

const minimalOpenAPI = `openapi: 3.0.3
info:
  title: order-service
  version: "1.0"
paths:
  /orders:
    get:
      operationId: listOrders
      responses:
        "200":
          description: ok
`

// --- service add: the subdirectory fallback (PLAN §7b, coordinator
// refinement) -- a checkout that is a subdirectory of its repository is
// committed as a local path by default, with an explanatory line, unless
// --team says to commit the repository with the subdir recorded. The Fake
// has no filesystem, so this needs the real engine too. ---

func TestServiceAdd_RealEngine_SubdirectoryFallsBackToLocalWithoutTeam(t *testing.T) {
	env := hermeticGitEnvCLI(t)

	bare := filepath.Join(t.TempDir(), "mono.git")
	runGitCLI(t, "", env, "init", "--bare", "-b", "main", bare)
	bareURL := "file://" + bare

	work := t.TempDir()
	runGitCLI(t, "", env, "clone", bareURL, work)
	subdir := filepath.Join(work, "services", "order-service")
	apiDir := filepath.Join(subdir, "api")
	require.NoError(t, os.MkdirAll(apiDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(apiDir, "openapi.yaml"), []byte(minimalOpenAPI), 0o644))
	runGitCLI(t, work, env, "add", "-A")
	runGitCLI(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "monorepo import")
	runGitCLI(t, work, env, "push", "origin", "HEAD")

	dir := t.TempDir()
	_, stderr, code := run(t, "init", dir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	makeSharedWorkspace(t, dir, env)

	t.Setenv("SAPIEN_NO_DAEMON", "1")
	applyHermeticEnv(t, env)

	// Without --team: falls back to committing the subdirectory itself as
	// a local path, with a line explaining why.
	stdout, stderr, code := run(t, "--workspace", dir, "service", "add", subdir, "--name", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "pass --team")

	stdout, stderr, code = run(t, "--workspace", dir, "service", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var svcs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &svcs))
	found := false
	for _, s := range svcs {
		if s["name"] == "order-service" {
			found = true
			src := s["source"].(map[string]any)
			assert.Equal(t, "local", src["type"])
			assert.Equal(t, subdir, src["path"])
		}
	}
	assert.True(t, found)

	_, stderr, code = run(t, "--workspace", dir, "service", "remove", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// With --team: commits the repository as a git source with the
	// subdirectory recorded, and still binds the checkout here -- the
	// service is bound at once, so its effective Source (what indexing
	// reads) is the local checkout; the committed git source with the
	// subdir lives in Binding.Team, exactly as for `service bind`.
	stdout, stderr, code = run(t, "--workspace", dir, "service", "add", subdir, "--name", "order-service", "--team", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var svc map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &svc))
	src := svc["source"].(map[string]any)
	assert.Equal(t, "local", src["type"])
	assert.Equal(t, subdir, src["path"])

	binding := svc["binding"].(map[string]any)
	assert.Equal(t, "local", binding["mode"])
	team := binding["team"].(map[string]any)
	assert.Equal(t, "git", team["type"])
	assert.Equal(t, bareURL, team["url"])
	assert.Equal(t, filepath.ToSlash(filepath.Join("services", "order-service", "api")), team["subdir"])
}
