package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// These tests cover PLAN §34f item 2 (service ref from the UI): SetRef,
// ClearRef and Branches, driven against a real git remote (hermeticGitEnv,
// newBareRepo, commitAndPush -- from git_test.go) so a ref switch actually
// moves a managed clone and reindexes different content, not a mock of it.

// widgetOpenAPI is a minimal, valid OpenAPI contract: "main" gets one
// operation; passing extraPath adds a second, so a ref switch is observable
// through ListOperations without needing a whole fixture service.
func widgetOpenAPI(extraPath string) string {
	base := `openapi: 3.1.0
info: { title: Widget Service, version: "1.0.0" }
paths:
  /v1/widgets:
    get:
      operationId: listWidgets
      summary: List widgets
      responses: { "200": { description: ok } }
`
	if extraPath == "" {
		return base
	}
	return base + `  ` + extraPath + `:
    get:
      operationId: getWidget
      summary: Get a widget
      responses: { "200": { description: ok } }
`
}

// createBranch creates a new branch in bareDir from fromRef, writes files on
// top, commits and pushes it. A same-named helper exists in
// internal/gitsrc's own tests; this package needs its own copy since test
// helpers do not cross package boundaries.
func createBranch(t *testing.T, bareDir string, env []string, branch, fromRef string, files map[string]string, msg string) {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	runGit(t, work, env, "checkout", "-b", branch, "origin/"+fromRef)
	for rel, content := range files {
		p := filepath.Join(work, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", msg)
	runGit(t, work, env, "push", "origin", branch)
}

// setupWidgetService registers widget-service (git-sourced, ref "main")
// against a bare repo with a "feature" branch carrying an extra operation,
// returning the opened Local and the repo's URL.
func setupWidgetService(t *testing.T) (*Local, string) {
	t.Helper()
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": widgetOpenAPI("")}, "init")
	createBranch(t, bareDir, env, "feature", "main",
		map[string]string{"api/openapi.yaml": widgetOpenAPI("/v1/widgets/{id}")}, "feature branch")

	ws := newGitTestWorkspace(t)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	svc, err := l.Services().Add(context.Background(), "widget-service", domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"})
	require.NoError(t, err)
	require.Equal(t, domain.SyncOK, svc.Status)

	return l, bareURL
}

// TestServiceSetRef_LocalScope_SwitchesCloneAndOperations_NoWorkspaceFileChange
// is item 2's required acceptance test: a local-scope ref switch moves the
// managed clone to a separate cache directory (so it reindexes the feature
// branch's extra operation) and never touches sapien.workspace.yaml -- while
// the team's own clone at "main" is left completely alone.
func TestServiceSetRef_LocalScope_SwitchesCloneAndOperations_NoWorkspaceFileChange(t *testing.T) {
	l, bareURL := setupWidgetService(t)
	ctx := context.Background()

	ops, err := l.Catalog().ListOperations(ctx, "widget-service")
	require.NoError(t, err)
	assert.Len(t, ops, 1)

	before, err := os.ReadFile(l.ws.File)
	require.NoError(t, err)

	updated, err := l.Services().SetRef(ctx, "widget-service", "feature", "")
	require.NoError(t, err)
	assert.Equal(t, domain.SyncOK, updated.Status)
	assert.Equal(t, "feature", updated.Source.Ref)

	ops, err = l.Catalog().ListOperations(ctx, "widget-service")
	require.NoError(t, err)
	assert.Len(t, ops, 2, "the feature branch's extra operation must now be indexed")

	after, err := os.ReadFile(l.ws.File)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a local ref override must never touch sapien.workspace.yaml")

	// The team's default clone (ref "main") must be untouched: a fresh
	// service pointed at "main" against the same URL still resolves to the
	// original, one-operation content -- proof the override used its own
	// cache directory rather than resetting the shared one.
	svc2, err := l.Services().Add(ctx, "widget-service-main", domain.Source{Kind: domain.SourceGit, URL: bareURL, Ref: "main"})
	require.NoError(t, err)
	require.Equal(t, domain.SyncOK, svc2.Status)
	ops2, err := l.Catalog().ListOperations(ctx, "widget-service-main")
	require.NoError(t, err)
	assert.Len(t, ops2, 1, "the team's main clone must not have been reset onto feature")
}

// TestServiceSetRef_TeamScope_UpdatesCommittedFile is item 2's other half:
// team scope rewrites source.ref in the committed sapien.workspace.yaml.
func TestServiceSetRef_TeamScope_UpdatesCommittedFile(t *testing.T) {
	l, _ := setupWidgetService(t)
	ctx := context.Background()

	updated, err := l.Services().SetRef(ctx, "widget-service", "feature", domain.RefScopeTeam)
	require.NoError(t, err)
	assert.Equal(t, "feature", updated.Source.Ref)

	raw, err := os.ReadFile(l.ws.File)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "ref: feature")

	ops, err := l.Catalog().ListOperations(ctx, "widget-service")
	require.NoError(t, err)
	assert.Len(t, ops, 2)
}

func TestServiceSetRef_UnknownRefIsInvalid(t *testing.T) {
	l, _ := setupWidgetService(t)
	_, err := l.Services().SetRef(context.Background(), "widget-service", "no-such-branch", "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestServiceSetRef_NonGitServiceIsInvalid(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	_, err = l.Services().SetRef(context.Background(), "order-service", "main", "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestServiceBranches_ListsBranchesAndTags(t *testing.T) {
	l, _ := setupWidgetService(t)

	out, err := l.Services().Branches(context.Background(), "widget-service")
	require.NoError(t, err)
	assert.Equal(t, "main", out.Default)
	assert.Equal(t, "main", out.Current)
	assert.ElementsMatch(t, []string{"main", "feature"}, out.Branches)
	assert.Equal(t, "main", out.Branches[0], "the default branch sorts first")
}

func TestClearRef_RestoresCommittedRefAndClone(t *testing.T) {
	l, _ := setupWidgetService(t)
	ctx := context.Background()

	_, err := l.Services().SetRef(ctx, "widget-service", "feature", "")
	require.NoError(t, err)

	updated, err := l.Services().ClearRef(ctx, "widget-service")
	require.NoError(t, err)
	assert.Equal(t, "main", updated.Source.Ref)

	ops, err := l.Catalog().ListOperations(ctx, "widget-service")
	require.NoError(t, err)
	assert.Len(t, ops, 1)
}

func TestClearRef_NoneToClearIsInvalid(t *testing.T) {
	l, _ := setupWidgetService(t)
	_, err := l.Services().ClearRef(context.Background(), "widget-service")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}
