package cli

// Test-only exports for package cli_test.
var ReplaceStaleDaemon = replaceStaleDaemon

// WriteError exposes the human/JSON error formatter for tests.
var WriteError = writeError

// AcquireWorkspaceLock exposes serve's lock acquisition for tests.
var AcquireWorkspaceLock = acquireWorkspaceLock
