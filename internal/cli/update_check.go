package cli

import (
	"context"
	"log/slog"
	"time"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/selfupdate"
)

// backgroundUpdateCheckDelay is how long the daemon waits after it starts
// serving before its first update check (PLAN §34f item 4), so the check
// never competes with startup for CPU or network. A var, not a const, so a
// test can shrink it rather than sitting out the real delay.
var backgroundUpdateCheckDelay = 5 * time.Second

// kickBackgroundUpdateCheck runs selfupdate's "is there a newer release"
// check once, after backgroundUpdateCheckDelay, if it is both enabled
// (updates.Enabled: cfg's `updates: check:` and $SAPIEN_NO_UPDATE_CHECK)
// and due (selfupdate.Due: never checked, or CheckInterval has elapsed).
// baseURL overrides selfupdate's default GitHub Releases root; serve.go
// passes "" (the real one) and a test passes an httptest.Server's URL.
//
// Call it with `go`: it never blocks the caller, and on failure never logs
// louder than Debug -- a laptop with no wifi failing to reach GitHub once
// a day is routine, not something daemon.log should warn about.
func kickBackgroundUpdateCheck(ctx context.Context, updates config.Updates, baseURL string) {
	if !updates.Enabled() {
		return
	}
	select {
	case <-time.After(backgroundUpdateCheckDelay):
	case <-ctx.Done():
		return
	}

	cache, err := selfupdate.LoadCacheFile(selfupdate.CachePath())
	if err != nil {
		slog.Default().Debug("background update check: could not read the cache", "error", err)
		// Fall through and check anyway: an unreadable cache is exactly the
		// case Due can't help with, and checking is cheap.
	}
	if !selfupdate.Due(cache, time.Now()) {
		return
	}

	if _, err := selfupdate.Refresh(ctx, selfupdate.RefreshOptions{BaseURL: baseURL}); err != nil {
		slog.Default().Debug("background update check failed", "error", err)
	}
}
