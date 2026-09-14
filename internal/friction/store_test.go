package friction_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/friction"
)

func newTestStore(t *testing.T) *friction.Store {
	t.Helper()
	return friction.New(t.TempDir())
}

func TestNew_EmptyDir_UsesEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAPIEN_FRICTION_DIR", dir)
	s := friction.New("")
	assert.Equal(t, dir, s.Dir())
}

func TestNew_ExplicitDirWinsOverEnv(t *testing.T) {
	envDir := t.TempDir()
	explicitDir := t.TempDir()
	t.Setenv("SAPIEN_FRICTION_DIR", envDir)
	s := friction.New(explicitDir)
	assert.Equal(t, explicitDir, s.Dir())
}

func TestNew_NoOverride_DefaultsUnderHome(t *testing.T) {
	t.Setenv("SAPIEN_FRICTION_DIR", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	s := friction.New("")
	assert.Equal(t, filepath.Join(home, ".sapien", "friction"), s.Dir())
}

// --- Create -----------------------------------------------------------

func TestCreate_RequiresTitle(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(context.Background(), friction.Report{Happened: "it broke"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestCreate_RequiresHappened(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(context.Background(), friction.Report{Title: "a tool misbehaved"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestCreate_DefaultsCategoryAndStatus(t *testing.T) {
	s := newTestStore(t)
	r, err := s.Create(context.Background(), friction.Report{
		Title: "a tool misbehaved", Happened: "it returned the wrong shape",
	})
	require.NoError(t, err)
	assert.Equal(t, friction.CategoryBug, r.Category)
	assert.Equal(t, friction.StatusPending, r.Status)
	assert.NotEmpty(t, r.ID)
	assert.True(t, strings.HasPrefix(r.ID, "fr_"))
}

func TestCreate_RejectsUnknownCategory(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(context.Background(), friction.Report{
		Title: "x", Happened: "y", Category: friction.Category("nonsense"),
	})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.NotEmpty(t, errs.As(err).Hint)
}

// TestCreate_RefusesSecret uses an AWS-shaped access key
// ("AKIAIOSFODNN7EXAMPLE", the same fake AWS docs use in examples and
// internal/memory/secrets_test.go's own fixture), which
// internal/memory.ScanSecrets recognizes by its AKIA prefix -- a real key
// is never needed to exercise the refusal.
func TestCreate_RefusesSecret(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(context.Background(), friction.Report{
		Title: "leaked something", Happened: "the response included AKIAIOSFODNN7EXAMPLE in a header",
	})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "aws access key")
	assert.Contains(t, err.Error(), "posted publicly")
}

func TestCreate_ScansAllTextFields(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(context.Background(), friction.Report{
		Title: "x", Happened: "y", Tried: "AKIAIOSFODNN7EXAMPLE",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aws access key")
}

// --- round trip ---------------------------------------------------------

func TestCreate_Get_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create(context.Background(), friction.Report{
		Title: "get_api returned the wrong shape", Category: friction.CategoryBug,
		Tool: "get_api", Tried: "fetching an operation's contract",
		Happened:  "the response had no request_example field at all",
		WouldHelp: "document the fallback when no example exists",
		Workspace: "logistics", Client: "claude-code", Version: "1.2.0",
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.Path)
	assert.FileExists(t, created.Path)

	got, err := s.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "get_api returned the wrong shape", got.Title)
	assert.Equal(t, friction.CategoryBug, got.Category)
	assert.Equal(t, "get_api", got.Tool)
	assert.Equal(t, "fetching an operation's contract", got.Tried)
	assert.Equal(t, "the response had no request_example field at all", got.Happened)
	assert.Equal(t, "document the fallback when no example exists", got.WouldHelp)
	assert.Equal(t, "logistics", got.Workspace)
	assert.Equal(t, "claude-code", got.Client)
	assert.Equal(t, "1.2.0", got.Version)
	assert.Equal(t, friction.StatusPending, got.Status)
	assert.WithinDuration(t, created.Created, got.Created, time.Second)
	assert.Equal(t, created.Path, got.Path)
}

// A report with no Tried/WouldHelp omits those headings entirely rather
// than writing them empty, and Get must not fabricate a heading from an
// empty section either.
func TestCreate_Get_OmitsEmptySections(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create(context.Background(), friction.Report{
		Title: "minimal report", Happened: "the tool errored with no message",
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(created.Path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "What I was trying to do")
	assert.NotContains(t, string(raw), "What would have helped")

	got, err := s.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Empty(t, got.Tried)
	assert.Empty(t, got.WouldHelp)
}

func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get(context.Background(), "fr_nope")
	require.Error(t, err)
	assert.Equal(t, errs.FrictionNotFound, errs.CodeOf(err))
	assert.NotEmpty(t, errs.As(err).Hint)
}

// --- List: ordering and empty dir ---------------------------------------

func TestList_EmptyDir(t *testing.T) {
	s := friction.New(filepath.Join(t.TempDir(), "does-not-exist-yet"))
	reports, err := s.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, reports)
}

func TestList_OrderedNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.Create(ctx, friction.Report{Title: "first", Happened: "h1"})
	require.NoError(t, err)
	second, err := s.Create(ctx, friction.Report{Title: "second", Happened: "h2"})
	require.NoError(t, err)
	third, err := s.Create(ctx, friction.Report{Title: "third", Happened: "h3"})
	require.NoError(t, err)

	// Pin well-separated Created timestamps by rewriting each file's front
	// matter directly, so the ordering assertion below is independent of
	// how fast (or slow) three back-to-back Create calls actually ran --
	// Store exposes no way to set Created except through Create itself.
	setCreated(t, first.Path, time.Now().Add(-3*time.Hour))
	setCreated(t, second.Path, time.Now().Add(-2*time.Hour))
	setCreated(t, third.Path, time.Now().Add(-1*time.Hour))

	reports, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, reports, 3)
	ids := []string{reports[0].ID, reports[1].ID, reports[2].ID}
	assert.Equal(t, []string{third.ID, second.ID, first.ID}, ids)
}

// setCreated rewrites a report file's "created:" front-matter line in
// place, the only way this test package has to pin a specific Created
// value (Store's public API always stamps Created from time.Now() itself).
func setCreated(t *testing.T, path string, ts time.Time) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "created:") {
			lines[i] = "created: " + ts.UTC().Format(time.RFC3339)
		}
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))
}

// --- MarkSent / Drop ------------------------------------------------------

func TestMarkSent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, err := s.Create(ctx, friction.Report{Title: "x", Happened: "y"})
	require.NoError(t, err)

	sent, err := s.MarkSent(ctx, created.ID, "https://github.com/gs-sinha/sapien/discussions/1")
	require.NoError(t, err)
	assert.Equal(t, friction.StatusSent, sent.Status)
	assert.Equal(t, "https://github.com/gs-sinha/sapien/discussions/1", sent.SentURL)
	assert.False(t, sent.SentAt.IsZero())

	got, err := s.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, friction.StatusSent, got.Status)
	assert.Equal(t, "https://github.com/gs-sinha/sapien/discussions/1", got.SentURL)
}

func TestMarkSent_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.MarkSent(context.Background(), "fr_nope", "https://example.com")
	require.Error(t, err)
	assert.Equal(t, errs.FrictionNotFound, errs.CodeOf(err))
}

func TestDrop(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, err := s.Create(ctx, friction.Report{Title: "x", Happened: "y"})
	require.NoError(t, err)

	require.NoError(t, s.Drop(ctx, created.ID))
	_, err = s.Get(ctx, created.ID)
	require.Error(t, err)
	assert.Equal(t, errs.FrictionNotFound, errs.CodeOf(err))
}

func TestDrop_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.Drop(context.Background(), "fr_nope")
	require.Error(t, err)
	assert.Equal(t, errs.FrictionNotFound, errs.CodeOf(err))
}
