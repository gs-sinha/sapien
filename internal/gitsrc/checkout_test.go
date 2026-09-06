package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func gitSource(url string) domain.Source {
	return domain.Source{Kind: domain.SourceGit, URL: url}
}

func TestEnsure_ClonesWithSparseCheckout(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	sha := commitAndPush(t, bareDir, env, map[string]string{
		"api/openapi.yaml": "openapi: 3.1.0\n",
		"api/service.yaml": "version: 1\nname: allocation-service\n",
		"docs/other.md":    "not part of the api package\n",
		"README.md":        "top-level readme, outside api/\n",
	}, "init")

	m := testManager(t, env)
	co, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)
	require.NotNil(t, co)

	assert.Equal(t, m.Dir(url), co.Dir)
	assert.Equal(t, filepath.Join(co.Dir, "api"), co.PackageDir)
	assert.Equal(t, sha, co.Commit)
	assert.Equal(t, "main", co.Ref)
	assert.Equal(t, url, co.URL)
	assert.Equal(t, "api", co.Subdir)

	// Sparse-checkout (cone mode) must have materialized api/ but not the
	// docs/ subdirectory. Cone mode always keeps loose files at the
	// repository root regardless of the sparse pattern, so README.md (a
	// root-level file, not inside a directory) is not a useful exclusion
	// check here -- docs/, a real subdirectory, is.
	assert.FileExists(t, filepath.Join(co.PackageDir, "openapi.yaml"))
	assert.FileExists(t, filepath.Join(co.PackageDir, "service.yaml"))
	assert.NoDirExists(t, filepath.Join(co.Dir, "docs"))

	data, err := os.ReadFile(filepath.Join(co.PackageDir, "openapi.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "openapi: 3.1.0\n", string(data))
}

func TestEnsure_CustomSubdir(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{
		"contract/openapi.yaml": "openapi: 3.1.0\n",
		"api/decoy.yaml":        "should not be checked out\n",
	}, "init")

	m := testManager(t, env)
	src := gitSource(url)
	src.Subdir = "contract"

	co, err := m.Ensure(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, "contract", co.Subdir)
	assert.Equal(t, filepath.Join(co.Dir, "contract"), co.PackageDir)
	assert.FileExists(t, filepath.Join(co.PackageDir, "openapi.yaml"))
	assert.NoFileExists(t, filepath.Join(co.Dir, "api", "decoy.yaml"))
}

func TestEnsure_AlreadyCloned_JustResolvesWithoutFetching(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	firstSHA := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	co1, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)
	assert.Equal(t, firstSHA, co1.Commit)

	// Push a second commit directly to the bare repo. Ensure must not fetch,
	// so a second Ensure call still reports the original commit.
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v2\n"}, "second")

	co2, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)
	assert.Equal(t, firstSHA, co2.Commit, "Ensure on an existing clone must not fetch")
	assert.Equal(t, co1.Dir, co2.Dir)
}

func TestEnsure_ExplicitBranchRef(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "main\n"}, "init")
	featureSHA := createBranch(t, bareDir, env, "feature", "main", map[string]string{"api/openapi.yaml": "feature\n"}, "feature work")

	m := testManager(t, env)
	src := gitSource(url)
	src.Ref = "feature"

	co, err := m.Ensure(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, "feature", co.Ref)
	assert.Equal(t, featureSHA, co.Commit)

	data, err := os.ReadFile(filepath.Join(co.PackageDir, "openapi.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "feature\n", string(data))
}

func TestEnsure_ExplicitTagRef(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")
	tagSHA := createTag(t, bareDir, env, "v1.0.0", "main")

	m := testManager(t, env)
	src := gitSource(url)
	src.Ref = "v1.0.0"

	co, err := m.Ensure(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", co.Ref)
	assert.Equal(t, tagSHA, co.Commit)
}

func TestEnsure_ExplicitCommitRef(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	firstSHA := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v2\n"}, "second")

	m := testManager(t, env)
	src := gitSource(url)
	src.Ref = firstSHA

	co, err := m.Ensure(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, firstSHA, co.Ref)
	assert.Equal(t, firstSHA, co.Commit)

	data, err := os.ReadFile(filepath.Join(co.PackageDir, "openapi.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "v1\n", string(data))
}

func TestEnsure_DefaultRefFallsBackToLsRemote(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	sha := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	dir := m.Dir(url)
	require.NoError(t, m.clone(context.Background(), url, dir, "api"))

	// Simulate a clone predating Sapien (or one that otherwise lost its
	// origin/HEAD tracking ref): resolveDefaultRef must fall back to
	// `ls-remote --symref`.
	require.NoError(t, os.Remove(filepath.Join(dir, ".git", "refs", "remotes", "origin", "HEAD")))

	ref, err := m.resolveDefaultRef(context.Background(), dir, url)
	require.NoError(t, err)
	assert.Equal(t, "main", ref)

	_, err = m.run(context.Background(), dir, "checkout", ref)
	require.NoError(t, err)
	commit, err := m.currentCommit(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, sha, commit)
}

func TestEnsure_EmptyRepo_NoRefResolvable(t *testing.T) {
	env := hermeticGitEnv(t)
	_, url := newBareRepo(t, env) // zero commits, no refs at all

	m := testManager(t, env)
	_, err := m.Ensure(context.Background(), gitSource(url))
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
	e := errs.As(err)
	assert.NotEmpty(t, e.Hint)
}

func TestSync_DetectsNewCommitAndUpdatesFiles(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	firstSHA := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	co, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)
	assert.Equal(t, firstSHA, co.Commit)

	secondSHA := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v2\n"}, "second")

	updated, changed, err := m.Sync(context.Background(), gitSource(url))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, secondSHA, updated.Commit)

	data, err := os.ReadFile(filepath.Join(updated.PackageDir, "openapi.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "v2\n", string(data))
}

func TestSync_NoChange_ChangedFalse(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	_, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)

	updated, changed, err := m.Sync(context.Background(), gitSource(url))
	require.NoError(t, err)
	assert.False(t, changed)
	require.NotNil(t, updated)
}

func TestSync_ClonesWhenMissing(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	sha := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	co, changed, err := m.Sync(context.Background(), gitSource(url))
	require.NoError(t, err)
	assert.True(t, changed, "a first-time sync (clone) always reports changed=true")
	assert.Equal(t, sha, co.Commit)
}

func TestSync_BranchDiscardsLocalModifications(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	co, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)

	// Simulate local drift in the working tree.
	target := filepath.Join(co.PackageDir, "openapi.yaml")
	require.NoError(t, os.WriteFile(target, []byte("locally modified\n"), 0o644))

	secondSHA := commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v2\n"}, "second")

	updated, changed, err := m.Sync(context.Background(), gitSource(url))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, secondSHA, updated.Commit)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "v2\n", string(data), "reset --hard must discard local drift")
}

func TestSync_TagRef_UsesCheckoutNotReset(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")
	tagSHA := createTag(t, bareDir, env, "v1.0.0", "main")

	// A tag never moves, so a second Sync at the same tag must report
	// changed=false via the checkout (non-branch) path.
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v2\n"}, "second, after the tag")

	m := testManager(t, env)
	src := gitSource(url)
	src.Ref = "v1.0.0"

	co, err := m.Ensure(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, tagSHA, co.Commit)

	updated, changed, err := m.Sync(context.Background(), src)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, tagSHA, updated.Commit)

	data, err := os.ReadFile(filepath.Join(updated.PackageDir, "openapi.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "v1\n", string(data))
}

func TestSync_SubdirChangeUpdatesSparseCheckout(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{
		"api/openapi.yaml":      "old subdir\n",
		"contract/openapi.yaml": "new subdir\n",
	}, "init")

	m := testManager(t, env)
	co, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(co.Dir, "api", "openapi.yaml"))
	assert.NoFileExists(t, filepath.Join(co.Dir, "contract", "openapi.yaml"))

	src := gitSource(url)
	src.Subdir = "contract"
	updated, _, err := m.Sync(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(co.Dir, "contract"), updated.PackageDir)
	assert.FileExists(t, filepath.Join(co.Dir, "contract", "openapi.yaml"))
}

func TestRemove(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	co, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)
	require.DirExists(t, co.Dir)

	require.NoError(t, m.Remove(url))
	assert.NoDirExists(t, co.Dir)

	// Removing an already-absent (or never-cloned) URL is not an error.
	assert.NoError(t, m.Remove(url))
	assert.NoError(t, m.Remove("git@example.com:nope/nope.git"))
}

func TestList(t *testing.T) {
	env := hermeticGitEnv(t)

	bareA, urlA := newBareRepo(t, env)
	shaA := commitAndPush(t, bareA, env, map[string]string{"api/openapi.yaml": "a\n"}, "init a")

	bareB, urlB := newBareRepo(t, env)
	shaB := commitAndPush(t, bareB, env, map[string]string{"api/openapi.yaml": "b\n"}, "init b")

	m := testManager(t, env)

	// An empty (never-used) cache directory lists as empty, not an error.
	empty, err := m.List()
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = m.Ensure(context.Background(), gitSource(urlA))
	require.NoError(t, err)
	_, err = m.Ensure(context.Background(), gitSource(urlB))
	require.NoError(t, err)

	// A stray directory with no sapien.json must be skipped, not fail List.
	require.NoError(t, os.MkdirAll(filepath.Join(m.cacheDir, "not-a-managed-clone"), 0o755))

	list, err := m.List()
	require.NoError(t, err)
	require.Len(t, list, 2)

	byURL := map[string]Checkout{}
	for _, co := range list {
		byURL[co.URL] = co
	}
	require.Contains(t, byURL, urlA)
	require.Contains(t, byURL, urlB)
	assert.Equal(t, shaA, byURL[urlA].Commit)
	assert.Equal(t, "api", byURL[urlA].Subdir)
	assert.Equal(t, shaB, byURL[urlB].Commit)

	// Sorted by URL.
	assert.True(t, list[0].URL < list[1].URL)
}

func TestEnsure_Errors_BadURL(t *testing.T) {
	env := hermeticGitEnv(t)
	m := testManager(t, env)

	_, err := m.Ensure(context.Background(), gitSource("file:///no/such/repository/here.git"))
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
	e := errs.As(err)
	assert.NotEmpty(t, e.Hint)
	assert.NotEmpty(t, e.Details["stderr"])
	assert.Equal(t, "file:///no/such/repository/here.git", e.Details["url"])
}

func TestEnsure_Errors_UnknownRef(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	src := gitSource(url)
	src.Ref = "does-not-exist-branch"

	_, err := m.Ensure(context.Background(), src)
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
	e := errs.As(err)
	assert.Contains(t, e.Hint, "unknown ref")
}

func TestEnsure_Errors_NotAGitURL(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))

	_, err := m.Ensure(context.Background(), gitSource("/local/path/not/a/url"))
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
}

func TestEnsure_Errors_EmptyURL(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))

	_, err := m.Ensure(context.Background(), gitSource(""))
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
}

func TestEnsure_Errors_WrongSourceKind(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))

	_, err := m.Ensure(context.Background(), domain.Source{Kind: domain.SourceLocal, Path: "."})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestSync_Errors_WrongSourceKind(t *testing.T) {
	m := testManager(t, hermeticGitEnv(t))

	_, _, err := m.Sync(context.Background(), domain.Source{Kind: domain.SourceLocal, Path: "."})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestEnsure_ContextAlreadyCancelled(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := m.Ensure(ctx, gitSource(url))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Equal(t, errs.Cancelled, errs.CodeOf(err))
	assert.Less(t, elapsed, 2*time.Second, "cancellation must be prompt")
}

func TestSync_ContextAlreadyCancelled(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, map[string]string{"api/openapi.yaml": "v1\n"}, "init")

	m := testManager(t, env)
	_, err := m.Ensure(context.Background(), gitSource(url))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = m.Sync(ctx, gitSource(url))
	require.Error(t, err)
	assert.Equal(t, errs.Cancelled, errs.CodeOf(err))
}
