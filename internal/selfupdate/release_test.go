package selfupdate_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/selfupdate"
)

// fakeGitHubReleases serves the two request/response shapes selfupdate
// depends on from the real github.com: releases/latest redirects to
// releases/tag/<tag>, and releases/download/<tag>/<name> serves file
// content directly (used by apply_test.go).
func fakeGitHubReleases(t *testing.T, latestTag string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+latestTag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>a release page</html>"))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestLatest_FollowsRedirectToTag(t *testing.T) {
	ts := fakeGitHubReleases(t, "v1.4.0")

	tag, err := selfupdate.Latest(context.Background(), selfupdate.LatestOptions{BaseURL: ts.URL})
	require.NoError(t, err)
	assert.Equal(t, "v1.4.0", tag)
}

func TestLatest_NoRedirectIsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a release page"))
	}))
	defer ts.Close()

	_, err := selfupdate.Latest(context.Background(), selfupdate.LatestOptions{BaseURL: ts.URL})
	assert.Error(t, err)
}

func TestLatest_UnreachableServerIsAnError(t *testing.T) {
	ts := fakeGitHubReleases(t, "v1.4.0")
	ts.Close() // now nothing is listening

	_, err := selfupdate.Latest(context.Background(), selfupdate.LatestOptions{BaseURL: ts.URL})
	assert.Error(t, err)
}

func TestReleaseURL(t *testing.T) {
	assert.Equal(t, "https://github.com/gs-sinha/sapien/releases/tag/v1.4.0", selfupdate.ReleaseURL("v1.4.0"))
}
