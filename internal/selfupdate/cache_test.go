package selfupdate_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/selfupdate"
)

func TestCachePath_UnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	assert.Equal(t, filepath.Join(home, ".sapien", "update-check.json"), selfupdate.CachePath())
}

func TestLoadCacheFile_MissingIsNilNotError(t *testing.T) {
	c, err := selfupdate.LoadCacheFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	require.NoError(t, err)
	assert.Nil(t, c)
}

func TestDue_Table(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		cache *selfupdate.Cache
		want  bool
	}{
		{"nil cache", nil, true},
		{"zero CheckedAt", &selfupdate.Cache{}, true},
		{"just checked", &selfupdate.Cache{CheckedAt: now}, false},
		{"checked 23h ago", &selfupdate.Cache{CheckedAt: now.Add(-23 * time.Hour)}, false},
		{"checked 25h ago", &selfupdate.Cache{CheckedAt: now.Add(-25 * time.Hour)}, true},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, selfupdate.Due(tc.cache, now), tc.name)
	}
}

func TestRefresh_WritesLatestToCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts := fakeGitHubReleases(t, "v1.5.0")

	c, err := selfupdate.Refresh(context.Background(), selfupdate.RefreshOptions{BaseURL: ts.URL})
	require.NoError(t, err)
	assert.Equal(t, "v1.5.0", c.Latest)
	assert.Empty(t, c.Error)
	assert.False(t, c.CheckedAt.IsZero())

	onDisk, err := selfupdate.LoadCacheFile(selfupdate.CachePath())
	require.NoError(t, err)
	require.NotNil(t, onDisk)
	assert.Equal(t, "v1.5.0", onDisk.Latest)
}

func TestRefresh_NetworkErrorIsCachedNotJustReturned(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts := fakeGitHubReleases(t, "v1.5.0")
	ts.Close()

	c, err := selfupdate.Refresh(context.Background(), selfupdate.RefreshOptions{BaseURL: ts.URL})
	require.NoError(t, err, "a network failure is captured in the cache, not returned as Refresh's own error")
	assert.NotEmpty(t, c.Error)
	assert.Empty(t, c.Latest)

	onDisk, err := selfupdate.LoadCacheFile(selfupdate.CachePath())
	require.NoError(t, err)
	require.NotNil(t, onDisk)
	assert.NotEmpty(t, onDisk.Error)
}
