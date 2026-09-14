package friction_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/friction"
)

// writeStubGH writes an executable shell script standing in for the `gh`
// binary: it inspects "$@" (joined into one string, since the query text is
// itself one argument) to tell the repository-lookup call apart from the
// createDiscussion mutation, the same way a real `gh api graphql` caller's
// two calls in GitHubDiscussions.Publish differ only in which GraphQL
// document they send.
func writeStubGH(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

const stubGHSuccess = `#!/bin/sh
args="$*"
case "$args" in
  *hasDiscussionsEnabled*)
    echo '{"data":{"repository":{"id":"R_repo123","hasDiscussionsEnabled":true,"discussionCategories":{"nodes":[{"id":"DIC_cat123","name":"General","slug":"general"}]}}}}'
    ;;
  *createDiscussion*)
    echo '{"data":{"createDiscussion":{"discussion":{"url":"https://github.com/gs-sinha/sapien/discussions/42"}}}}'
    ;;
  *)
    echo "unexpected args: $args" >&2
    exit 1
    ;;
esac
`

const stubGHDiscussionsDisabled = `#!/bin/sh
echo '{"data":{"repository":{"id":"R_repo123","hasDiscussionsEnabled":false,"discussionCategories":{"nodes":[]}}}}'
`

const stubGHCategoryMissing = `#!/bin/sh
echo '{"data":{"repository":{"id":"R_repo123","hasDiscussionsEnabled":true,"discussionCategories":{"nodes":[{"id":"DIC_cat1","name":"General","slug":"general"},{"id":"DIC_cat2","name":"Announcements","slug":"announcements"}]}}}}'
`

const stubGHAuthFailure = `#!/bin/sh
echo "error connecting to api.github.com" >&2
echo "To authenticate, please run: gh auth login" >&2
exit 1
`

func TestGitHubDiscussions_Publish_Success(t *testing.T) {
	gh := writeStubGH(t, stubGHSuccess)
	g := friction.GitHubDiscussions{Repo: "gs-sinha/sapien", Category: "General", GH: gh}

	url, err := g.Publish(context.Background(), "[agent friction] x", "body text")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/gs-sinha/sapien/discussions/42", url)
}

// Category matching is case-insensitive on name or slug.
func TestGitHubDiscussions_Publish_CategoryMatchesSlugCaseInsensitively(t *testing.T) {
	gh := writeStubGH(t, stubGHSuccess)
	g := friction.GitHubDiscussions{Repo: "gs-sinha/sapien", Category: "general", GH: gh}

	url, err := g.Publish(context.Background(), "title", "body")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/gs-sinha/sapien/discussions/42", url)
}

// An empty Category defaults to "General".
func TestGitHubDiscussions_Publish_DefaultCategory(t *testing.T) {
	gh := writeStubGH(t, stubGHSuccess)
	g := friction.GitHubDiscussions{Repo: "gs-sinha/sapien", GH: gh}

	url, err := g.Publish(context.Background(), "title", "body")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/gs-sinha/sapien/discussions/42", url)
}

func TestGitHubDiscussions_Publish_DiscussionsDisabled(t *testing.T) {
	gh := writeStubGH(t, stubGHDiscussionsDisabled)
	g := friction.GitHubDiscussions{Repo: "gs-sinha/sapien", GH: gh}

	_, err := g.Publish(context.Background(), "title", "body")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "not enabled")
	assert.Contains(t, errs.As(err).Hint, "Settings > General > Features")
}

func TestGitHubDiscussions_Publish_CategoryMissing(t *testing.T) {
	gh := writeStubGH(t, stubGHCategoryMissing)
	g := friction.GitHubDiscussions{Repo: "gs-sinha/sapien", Category: "NoSuchCategory", GH: gh}

	_, err := g.Publish(context.Background(), "title", "body")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	hint := errs.As(err).Hint
	assert.Contains(t, hint, "General")
	assert.Contains(t, hint, "Announcements")
}

func TestGitHubDiscussions_Publish_AuthFailure(t *testing.T) {
	gh := writeStubGH(t, stubGHAuthFailure)
	g := friction.GitHubDiscussions{Repo: "gs-sinha/sapien", GH: gh}

	_, err := g.Publish(context.Background(), "title", "body")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Hint, "gh auth login")
}

func TestGitHubDiscussions_Publish_BinaryMissing(t *testing.T) {
	g := friction.GitHubDiscussions{Repo: "gs-sinha/sapien", GH: filepath.Join(t.TempDir(), "no-such-gh-binary")}

	_, err := g.Publish(context.Background(), "title", "body")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Hint, "https://cli.github.com")
	assert.Contains(t, errs.As(err).Hint, "gh auth login")
}

func TestGitHubDiscussions_Publish_InvalidRepoFormat(t *testing.T) {
	g := friction.GitHubDiscussions{Repo: "not-owner-slash-name"}
	_, err := g.Publish(context.Background(), "title", "body")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// A cancelled context is rejected before a process is even started.
func TestGitHubDiscussions_Publish_CancelledContext(t *testing.T) {
	gh := writeStubGH(t, stubGHSuccess)
	g := friction.GitHubDiscussions{Repo: "gs-sinha/sapien", GH: gh}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := g.Publish(ctx, "title", "body")
	require.Error(t, err)
	assert.Equal(t, errs.Cancelled, errs.CodeOf(err))
}

var _ friction.Publisher = friction.GitHubDiscussions{}
