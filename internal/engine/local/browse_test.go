package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
)

// BrowseCheckouts tests (PLAN §7b) reuse the same hermetic bare-repo
// fixtures as binding_test.go.

func TestBrowseCheckouts_ListsAndClassifiesEntries(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	otherBare, otherURL := newBareRepo(t, env)
	commitAndPush(t, otherBare, env, readFixtureAPIFiles(t, "rider-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	home := t.TempDir()

	// A clone of the team repository: should be fully described and match.
	matchClone := filepath.Join(home, "alpha-clone")
	runGit(t, "", env, "clone", bareURL, matchClone)

	// A clone of an unrelated repository: a repo, but not a match.
	otherClone := filepath.Join(home, "beta-other")
	runGit(t, "", env, "clone", otherURL, otherClone)

	// A plain directory: not a git repository at all.
	plainDir := filepath.Join(home, "zeta-plain")
	require.NoError(t, os.MkdirAll(plainDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plainDir, "notes.txt"), []byte("hi\n"), 0o644))

	// Noise that must never appear in the listing.
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".hidden"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "node_modules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "a-file.txt"), []byte("x"), 0o644))

	listing, err := l.Services().BrowseCheckouts(ctx, "order-service", home)
	require.NoError(t, err)
	assert.Equal(t, home, listing.Path)
	assert.Equal(t, filepath.Dir(home), listing.Parent)

	require.Len(t, listing.Entries, 3, "dotfiles, node_modules and plain files are skipped")
	names := []string{listing.Entries[0].Name, listing.Entries[1].Name, listing.Entries[2].Name}
	assert.Equal(t, []string{"alpha-clone", "beta-other", "zeta-plain"}, names, "sorted case-insensitively")

	match := listing.Entries[0]
	assert.True(t, match.Matches)
	require.NotNil(t, match.Checkout)
	assert.Equal(t, matchClone, match.Checkout.Path)
	assert.Equal(t, "main", match.Checkout.Branch)
	assert.NotEmpty(t, match.Checkout.Commit)
	assert.Equal(t, filepath.Join(matchClone, "api"), match.Checkout.Package)
	assert.Empty(t, match.Reason)

	other := listing.Entries[1]
	assert.False(t, other.Matches)
	require.NotNil(t, other.Checkout)
	assert.Equal(t, otherURL, other.Checkout.Remote)
	assert.Equal(t, "clone of "+otherURL, other.Reason)

	plain := listing.Entries[2]
	assert.False(t, plain.Matches)
	assert.Nil(t, plain.Checkout)
	assert.Empty(t, plain.Reason)
}

func TestBrowseCheckouts_MatchWithoutPackage(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	home := t.TempDir()
	clone := filepath.Join(home, "clone")
	runGit(t, "", env, "clone", bareURL, clone)
	require.NoError(t, os.RemoveAll(filepath.Join(clone, "api")))

	listing, err := l.Services().BrowseCheckouts(ctx, "order-service", home)
	require.NoError(t, err)
	require.Len(t, listing.Entries, 1)

	entry := listing.Entries[0]
	assert.True(t, entry.Matches, "still a match; the UI decides what to do about the missing package")
	require.NotNil(t, entry.Checkout)
	assert.Empty(t, entry.Checkout.Package)
	assert.Equal(t, "no API package under this checkout", entry.Reason)
}

func TestBrowseCheckouts_UnknownService(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()

	_, err = l.Services().BrowseCheckouts(context.Background(), "no-such-service", t.TempDir())
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
}

func TestBrowseCheckouts_DirMustBeADirectory(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()

	file := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	_, err = l.Services().BrowseCheckouts(context.Background(), "order-service", file)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestBrowseCheckouts_EmptyDirDefaultsToHome(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, bareURL := newBareRepo(t, env)
	commitAndPush(t, bareDir, env, readFixtureAPIFiles(t, "order-service"), "initial import")

	ws := teamWorkspace(t, bareURL)
	l, err := Open(ws, Options{GitCacheDir: t.TempDir()})
	require.NoError(t, err)
	defer l.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)

	listing, err := l.Services().BrowseCheckouts(context.Background(), "order-service", "")
	require.NoError(t, err)
	wantHome, err := filepath.EvalSymlinks(home)
	require.NoError(t, err)
	gotHome, err := filepath.EvalSymlinks(listing.Path)
	require.NoError(t, err)
	assert.Equal(t, wantHome, gotHome)
}
