package cli

import "time"

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
