package cli_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/selfupdate"
)

// fakeGitHubReleases mirrors internal/selfupdate's own test helper: a
// releases/latest redirect to releases/tag/<tag>.
func fakeGitHubReleases(t *testing.T, latestTag string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+latestTag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>release page</html>"))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestKickBackgroundUpdateCheck_Disabled_NeverHitsNetwork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defer cli.SetBackgroundUpdateCheckDelay(0)()

	called := false
	rel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer rel.Close()

	f := false
	cli.KickBackgroundUpdateCheck(context.Background(), config.Updates{Check: &f}, rel.URL)
	assert.False(t, called)
}

func TestKickBackgroundUpdateCheck_Enabled_WritesCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defer cli.SetBackgroundUpdateCheckDelay(0)()

	rel := fakeGitHubReleases(t, "v9.9.9")

	cli.KickBackgroundUpdateCheck(context.Background(), config.Updates{}, rel.URL)

	cache, err := selfupdate.LoadCacheFile(selfupdate.CachePath())
	require.NoError(t, err)
	require.NotNil(t, cache)
	assert.Equal(t, "v9.9.9", cache.Latest)
}

func TestKickBackgroundUpdateCheck_NotDueYet_SkipsNetwork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defer cli.SetBackgroundUpdateCheckDelay(0)()

	// A cache checked moments ago is not due again for another 24h.
	require.NoError(t, writeFreshCache(t))

	called := false
	rel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer rel.Close()

	cli.KickBackgroundUpdateCheck(context.Background(), config.Updates{}, rel.URL)
	assert.False(t, called)
}

func TestKickBackgroundUpdateCheck_ContextCancelledBeforeDelaySkips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defer cli.SetBackgroundUpdateCheckDelay(time.Hour)() // long enough that only cancellation stops it

	called := false
	rel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer rel.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		cli.KickBackgroundUpdateCheck(ctx, config.Updates{}, rel.URL)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("kickBackgroundUpdateCheck did not return promptly after ctx cancellation")
	}
	assert.False(t, called)
}

// writeFreshCache populates the cache with a CheckedAt of just now, via a
// fake release server (never the real one), so selfupdate.Due reports
// "not due" for the next 24h.
func writeFreshCache(t *testing.T) error {
	t.Helper()
	rel := fakeGitHubReleases(t, "v1.0.0")
	defer rel.Close()
	_, err := selfupdate.Refresh(context.Background(), selfupdate.RefreshOptions{BaseURL: rel.URL})
	return err
}
