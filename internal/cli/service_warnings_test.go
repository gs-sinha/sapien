package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// textPlainOpenAPIForCLI declares one operation with no summary
// (MISSING_SUMMARY) whose 200 response is text/plain
// (UNSUPPORTED_MEDIA_TYPE), so a real `service add`/`sync` against it
// always emits exactly those two warnings. textPlainServiceYAML accepts
// only UNSUPPORTED_MEDIA_TYPE, with a reason, so the CLI's warning
// acceptance rendering ("K warnings accepted", the unaccepted-only
// `warning [...]` lines, the ACCEPTED column, and --show-accepted's
// `accepted [CODE] ... (reason)` lines) can be exercised against the real
// engine -- enginetest.Fake never populates Service.Warnings/AcceptedWarnings.
const textPlainOpenAPIForCLI = `
openapi: 3.1.0
info:
  title: Text Plain Service
  version: "1.0.0"
paths:
  /v1/status:
    get:
      operationId: getStatus
      responses:
        "200":
          description: ok
          content:
            text/plain:
              schema: { type: string }
`

const textPlainServiceYAML = `
version: 1
name: text-service
accepted_warnings:
  - code: UNSUPPORTED_MEDIA_TYPE
    reason: "Returns text/plain by design; the contract describes the wire faithfully."
`

// writeServiceWarningsFixture writes a tiny real api/ package (see
// textPlainOpenAPIForCLI/textPlainServiceYAML) to a fresh temp dir and
// returns its path, ready to be passed to `service add`.
func writeServiceWarningsFixture(t *testing.T) (svcDir string) {
	t.Helper()
	svcDir = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(svcDir, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "api", "openapi.yaml"), []byte(textPlainOpenAPIForCLI), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "api", "service.yaml"), []byte(textPlainServiceYAML), 0o644))
	return svcDir
}

func TestServiceAdd_Warnings_DefaultHidesAcceptedDetail(t *testing.T) {
	wsDir := t.TempDir()
	svcDir := writeServiceWarningsFixture(t)

	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", wsDir, "service", "add", svcDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	assert.Contains(t, stdout, "text-service: 1 operations, status ok, 1 warnings accepted")
	assert.Contains(t, stdout, "warning [MISSING_SUMMARY]")
	// UNSUPPORTED_MEDIA_TYPE was accepted; --show-accepted was not passed,
	// so no accepted-warning detail line (and hence its code) appears.
	assert.NotContains(t, stdout, "accepted [")
	assert.NotContains(t, stdout, "UNSUPPORTED_MEDIA_TYPE")
}

func TestServiceAdd_ShowAccepted(t *testing.T) {
	wsDir := t.TempDir()
	svcDir := writeServiceWarningsFixture(t)

	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", wsDir, "service", "add", svcDir, "--show-accepted")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "accepted [UNSUPPORTED_MEDIA_TYPE]")
	assert.Contains(t, stdout, "Returns text/plain by design; the contract describes the wire faithfully.")
}

func TestServiceSync_ShowAccepted(t *testing.T) {
	wsDir := t.TempDir()
	svcDir := writeServiceWarningsFixture(t)

	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	_, stderr, code = run(t, "--workspace", wsDir, "service", "add", svcDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", wsDir, "service", "sync", "text-service", "--show-accepted")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "text-service: 1 operations, status ok, 1 warnings accepted")
	assert.Contains(t, stdout, "accepted [UNSUPPORTED_MEDIA_TYPE]")
	assert.Contains(t, stdout, "Returns text/plain by design; the contract describes the wire faithfully.")

	// Without the flag, the same sync omits the accepted-warning detail line.
	stdoutNoFlag, stderr, code := run(t, "--workspace", wsDir, "service", "sync", "text-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdoutNoFlag, "accepted [")
}

func TestServiceList_AcceptedColumn(t *testing.T) {
	wsDir := t.TempDir()
	svcDir := writeServiceWarningsFixture(t)

	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	_, stderr, code = run(t, "--workspace", wsDir, "service", "add", svcDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", wsDir, "service", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "ACCEPTED")

	stdoutJSON, stderr, code := run(t, "--workspace", wsDir, "service", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var svcs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdoutJSON), &svcs))
	require.Len(t, svcs, 1)
	acceptedWarnings, ok := svcs[0]["accepted_warnings"].([]any)
	require.True(t, ok, "expected an accepted_warnings key in %+v", svcs[0])
	assert.Len(t, acceptedWarnings, 1)
}

// TestServiceAdd_ConflictHint checks the hint newServiceAddCmd attaches to
// an E_CONFLICT error: unlike add_service over MCP (which re-syncs
// automatically), the CLI still fails the command, but points at the fix.
func TestServiceAdd_ConflictHint(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "./order-service", "--name", "order-service")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "hint: already registered; run `sapien service sync order-service`")

	_, stderrJSON, code2 := run(t, "--workspace", dir, "service", "add", "./order-service", "--name", "order-service", "--json")
	assert.Equal(t, 2, code2)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderrJSON), &got))
	assert.Equal(t, "already registered; run `sapien service sync order-service`", got["hint"])
}
