package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

// createdID extracts the id printed by `memory add`'s first line
// ("created mem_...") from stdout.
func createdID(t *testing.T, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "created ") {
			return strings.TrimPrefix(line, "created ")
		}
	}
	t.Fatalf("no \"created <id>\" line in stdout: %q", stdout)
	return ""
}

// --- memory add: not-a-git-repo note (feedback item 7) ---

func TestMemoryAdd_WorkspaceScope_NotGitRepo_Hint(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "workspace scope, plain tmp dir")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "note:")
	assert.Contains(t, stdout, "is not a git repo, so workspace memories live only on this machine")
}

func TestMemoryAdd_WorkspaceScope_GitRepo_NoHint(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "workspace scope, dir is a git repo")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "is not a git repo")
}

// Personal-scope memories never get the not-a-git-repo note: it only
// applies to workspace scope, which is the one PLAN §12 ties to the
// workspace directory's own git history.
func TestMemoryAdd_PersonalScope_NoGitHint(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "personal scratch note", "--scope", "personal")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "stored: SQLite only")
	assert.NotContains(t, stdout, "is not a git repo")
	assert.NotContains(t, stdout, "concerns")
}

// --- memory add: service subject on workspace scope (feedback items 2-3) ---

func TestMemoryAdd_ServiceSubjectOnWorkspaceScope_Hint(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "allocate retries internally",
		"--service", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	id := createdID(t, stdout)

	assert.Contains(t, stdout, "concerns order-service")
	assert.Contains(t, stdout, "sapien memory rescope "+id+" --scope service")
}

// A service subject on a *non*-workspace scope (already service, or
// personal) is not a candidate for the rescope nudge: it's either already
// there, or scope isn't about subject at all for personal notes.
func TestMemoryAdd_ServiceSubjectOnServiceScope_NoRescopeHint(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "already service scoped",
		"--service", "order-service", "--scope", "service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "concerns order-service; if it would hold")
}

// --- memory add: similar-memory nudge (feedback item 5) ---

func TestMemoryAdd_SimilarMemory_Hint(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	existing, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryGotcha, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "duplicate detection sentinel text",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "duplicate detection sentinel text")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	assert.Contains(t, stdout, "similar: "+existing.ID)
	assert.Contains(t, stdout, "consider updating that one instead, or promoting it: `sapien memory promote "+existing.ID+"`")
}

// A similar match with a long, single-line body exercises firstLine's
// truncate-to-60-bytes branch (short bodies never hit it).
func TestMemoryAdd_SimilarMemory_LongFirstLine_Truncated(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	longText := "duplicate detection sentinel text that goes on for a while past the sixty character cutoff so truncation definitely triggers"
	existing, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryGotcha, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: longText,
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "duplicate detection sentinel text")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	assert.Contains(t, stdout, "similar: "+existing.ID)
	assert.Contains(t, stdout, longText[:60])
	assert.NotContains(t, stdout, longText[:61])
}

// A similar match with a multi-line body exercises firstLine's
// split-on-newline branch (a single-line body never hits it).
func TestMemoryAdd_SimilarMemory_MultiLine_FirstLineOnly(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	existing, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryGotcha, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "short first line\nsecond line irrelevant to the match",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "short first line")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	assert.Contains(t, stdout, "similar: "+existing.ID+" (")
	assert.Contains(t, stdout, "short first line")
	assert.NotContains(t, stdout, "second line irrelevant")
}

func TestMemoryAdd_NoSimilarMemory_NoHint(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "totally unrelated brand new text xyz123")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "similar:")
}

// --- memory add: the promotion reminder always appears, human mode only ---

func TestMemoryAdd_AlwaysEndsWithPromoteReminder(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "some memory text")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	id := createdID(t, stdout)
	assert.Contains(t, stdout, "when this stabilises, `sapien memory promote "+id+"` locates where it belongs")
}

func TestMemoryAdd_JSON_NoHints(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "some memory text", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "stored:")
	assert.NotContains(t, stdout, "similar:")
	assert.NotContains(t, stdout, "promote")
}

// --- memory rescope ---

func TestMemoryRescope_ToService_Success(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "some knowledge to rescope",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "rescope", mem.ID,
		"--scope", "service", "--service", "order-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, mem.ID, got["id"])

	updated, err := fake.Memories().Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ScopeService, updated.Scope)
	assert.Equal(t, "order-service", updated.Subject.Service)
}

func TestMemoryRescope_ToService_Success_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "some knowledge to rescope, human mode",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "rescope", mem.ID,
		"--scope", "service", "--service", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "->")
}

// A memory with an existing service subject can be rescoped to service
// scope without --service.
func TestMemoryRescope_ToService_ExistingSubject(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "order-service"},
		Source:  domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "already names its service",
	})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "memory", "rescope", mem.ID, "--scope", "service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	updated, err := fake.Memories().Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ScopeService, updated.Scope)
}

func TestMemoryRescope_ServiceScopeRequiresSubject(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "no service subject at all",
	})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "memory", "rescope", mem.ID, "--scope", "service", "--json")
	assert.Equal(t, 2, code)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
	assert.NotEmpty(t, got["hint"])
}

func TestMemoryRescope_MissingScope(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "missing --scope flag",
	})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "memory", "rescope", mem.ID, "--json")
	assert.Equal(t, 2, code)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
	assert.NotEmpty(t, got["hint"])
}

func TestMemoryRescope_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "memory", "rescope", "mem_nope", "--scope", "personal", "--json")
	assert.Equal(t, 2, code)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_MEMORY_NOT_FOUND", got["code"])
}

func TestMemoryRescope_ToPersonal(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "going personal",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "rescope", mem.ID, "--scope", "personal")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SQLite only")

	updated, err := fake.Memories().Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ScopePersonal, updated.Scope)
}
