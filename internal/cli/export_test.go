package cli

import (
	"context"
	"time"

	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
)

// Test-only exports for package cli_test.
var ReplaceStaleDaemon = replaceStaleDaemon

// WriteError exposes the human/JSON error formatter for tests.
var WriteError = writeError

// AcquireWorkspaceLock exposes serve's lock acquisition for tests.
var AcquireWorkspaceLock = acquireWorkspaceLock

// StopDaemon exposes the SIGTERM-then-SIGKILL stopper for tests.
var StopDaemon = stopDaemon

// FindDaemon exposes daemon discovery with its bounded retry for tests.
var FindDaemon = findDaemon

// SetStopWindows shortens stopDaemon's SIGTERM grace period, its
// post-SIGKILL window and its poll interval, returning a func that restores
// them. A test that proves the escalation should not have to sit out the
// real five seconds to do it.
func SetStopWindows(grace, kill, poll time.Duration) func() {
	oldGrace, oldKill, oldPoll := stopGraceWindow, stopKillWindow, stopPollInterval
	stopGraceWindow, stopKillWindow, stopPollInterval = grace, kill, poll
	return func() {
		stopGraceWindow, stopKillWindow, stopPollInterval = oldGrace, oldKill, oldPoll
	}
}

// SetFindDaemonRetry overrides findDaemon's attempt count and the pause
// between attempts, returning a func that restores them, so a test can
// exercise the whole retry budget in milliseconds.
func SetFindDaemonRetry(attempts int, delay time.Duration) func() {
	oldAttempts, oldDelay := findDaemonAttempts, findDaemonRetryDelay
	findDaemonAttempts, findDaemonRetryDelay = attempts, delay
	return func() {
		findDaemonAttempts, findDaemonRetryDelay = oldAttempts, oldDelay
	}
}

// ListenLoopback exposes serve's binder, so a test can prove the
// stable-port fallback without starting a daemon.
var ListenLoopback = listenLoopback

// Test-only exports for the macOS launcher bundle (ui_installapp.go).
var (
	InstallAppBundle     = installAppBundle
	LauncherScript       = launcherScript
	PlistVersion         = plistVersion
	ShellQuote           = shellQuote
	RemoveExistingBundle = removeExistingBundle
)

// RestartDaemon exposes `sapien daemon restart`'s stop-then-start helper
// for tests (daemonctl.go).
var RestartDaemon = restartDaemon

// KickBackgroundUpdateCheck exposes the daemon's background update check
// for tests (update_check.go).
var KickBackgroundUpdateCheck = kickBackgroundUpdateCheck

// SetBackgroundUpdateCheckDelay overrides kickBackgroundUpdateCheck's
// startup delay, returning a func that restores it, so a test doesn't have
// to sit out the real delay (or, given 0, doesn't have to race it at all).
func SetBackgroundUpdateCheckDelay(d time.Duration) func() {
	old := backgroundUpdateCheckDelay
	backgroundUpdateCheckDelay = d
	return func() { backgroundUpdateCheckDelay = old }
}

// SetReleasesBaseURL overrides `sapien upgrade`'s GitHub Releases root
// (upgrade.go), returning a func that restores it, so a test can point it
// at an httptest.Server rather than the real github.com.
func SetReleasesBaseURL(url string) func() {
	old := releasesBaseURL
	releasesBaseURL = url
	return func() { releasesBaseURL = old }
}

// SetUpgradeExecutablePath overrides the file `sapien upgrade` replaces
// (the running binary, by default), returning a func that restores it, so
// a test can prove a full download-verify-replace cycle against a
// throwaway file instead of the go-test binary that is running the test.
func SetUpgradeExecutablePath(path string) func() {
	old := upgradeExecutablePath
	upgradeExecutablePath = path
	return func() { upgradeExecutablePath = old }
}

// SetSpawnDaemonFunc overrides spawnDaemonFunc (used by findOrStartDaemon
// and `daemon restart`/`upgrade`'s restartDaemon) with a fake, returning a
// func that restores it. spawnDaemon itself execs os.Executable() as
// "serve"; under `go test` that is the compiled test binary, not a built
// `sapien`, so a test that needs to observe "and then a daemon is started"
// substitutes a fake here rather than ever calling the real one.
func SetSpawnDaemonFunc(f func(context.Context, *domain.Workspace, string) (*daemon.Info, error)) func() {
	old := spawnDaemonFunc
	spawnDaemonFunc = f
	return func() { spawnDaemonFunc = old }
}
