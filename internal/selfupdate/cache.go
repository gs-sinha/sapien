package selfupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/errs"
)

// CheckInterval is how long a cached Latest result is trusted before a
// background check is due again (PLAN §34f item 4: "at most once a day").
const CheckInterval = 24 * time.Hour

// Cache is the persisted contents of CachePath: the last update check's
// result, success or failure.
type Cache struct {
	Latest    string    `json:"latest,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

// CachePath returns "~/.sapien/update-check.json" (config.UserDir).
func CachePath() string {
	return filepath.Join(config.UserDir(), "update-check.json")
}

// LoadCacheFile reads path as a Cache. A missing file is (nil, nil), not an
// error: "never checked yet" is the ordinary state for a fresh install.
func LoadCacheFile(path string) (*Cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.Wrap(errs.Internal, err, "reading %s", path)
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "parsing %s", path)
	}
	return &c, nil
}

// saveCacheFile writes c to path atomically (temp file, then rename), mode
// 0600, mirroring internal/daemon.Write's dance for daemon.json.
func saveCacheFile(path string, c *Cache) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errs.Wrap(errs.Internal, err, "creating %s", dir)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "encoding %s", path)
	}

	tmp, err := os.CreateTemp(dir, ".update-check.json.*.tmp")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "creating temp file in %s", dir)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errs.Wrap(errs.Internal, err, "writing %s", tmpName)
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "closing %s", tmpName)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return errs.Wrap(errs.Internal, err, "chmod %s", tmpName)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errs.Wrap(errs.Internal, err, "renaming %s to %s", tmpName, path)
	}
	return nil
}

// Due reports whether a check should run: cache is nil (never checked), its
// CheckedAt is zero, or CheckInterval has elapsed since. A previous error
// does not by itself force an earlier retry -- only staleness does, so a
// sustained outage checks once a day like anything else rather than on
// every request.
func Due(cache *Cache, now time.Time) bool {
	if cache == nil || cache.CheckedAt.IsZero() {
		return true
	}
	return now.Sub(cache.CheckedAt) >= CheckInterval
}

// RefreshOptions configures Refresh.
type RefreshOptions struct {
	// BaseURL and Client are forwarded to Latest; see LatestOptions.
	BaseURL string
	Client  *http.Client
	// Now overrides time.Now, for tests.
	Now func() time.Time
}

// Refresh always hits the network (via Latest) and writes the result --
// success or failure -- to CachePath, returning the fresh Cache. It does
// not consult Due or any config/env gate: those decide *whether* to call
// Refresh; this is the call itself, used both by the daemon's throttled
// background check and by POST /v1/update/check's explicit "check now",
// which bypasses the throttle on purpose.
//
// A network error is captured in the returned Cache's Error field and
// still written to disk (with the previous Latest, if any, discarded --
// there is no stale-latest-plus-fresh-error state to preserve here) rather
// than only returned as err, so a caller that only reads the cache later
// (GET /v1/update) still sees why the last check failed. err is non-nil
// only when the cache file itself could not be written.
func Refresh(ctx context.Context, opts RefreshOptions) (*Cache, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	c := &Cache{CheckedAt: now().UTC()}
	latest, err := Latest(ctx, LatestOptions{BaseURL: opts.BaseURL, Client: opts.Client})
	if err != nil {
		c.Error = err.Error()
	} else {
		c.Latest = latest
	}

	if err := saveCacheFile(CachePath(), c); err != nil {
		return c, err
	}
	return c, nil
}
