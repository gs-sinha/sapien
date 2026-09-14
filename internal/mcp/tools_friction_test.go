package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// report_friction queues a Markdown file on this machine and sends nothing:
// the human review step is the CLI's, so the tool's whole contract is "the
// file exists, carries what I said and who I am, and the text tells the
// agent that nothing was posted".
func TestReportFriction_QueuesLocallyAndSendsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAPIEN_FRICTION_DIR", dir)
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	res := callTool(t, cs, "report_friction", map[string]any{
		"title":           "get_api field list has no example for arrays",
		"what_i_tried":    "build a request body for logistics.createShipment",
		"what_happened":   "request_example omitted the items array entirely",
		"what_would_help": "synthesize one element from the item schema",
		"tool":            "get_api",
		"category":        "bug",
	})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[ReportFrictionOutput](t, res.StructuredContent)
	assert.True(t, strings.HasPrefix(out.ID, "fr_"), out.ID)
	assert.Equal(t, "pending", out.Status)
	assert.Contains(t, firstText(res), "nothing was sent")
	assert.Contains(t, firstText(res), "sapien friction send "+out.ID)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	text := string(raw)
	assert.Contains(t, text, "client: claude-code", "the MCP client is attributed")
	assert.Contains(t, text, "version: test")
	assert.Contains(t, text, "status: pending")
	assert.Contains(t, text, "items array")
}

// A report is posted publicly after review, so a secret in it is refused at
// the door rather than left for the human to notice.
func TestReportFriction_RefusesSecrets(t *testing.T) {
	t.Setenv("SAPIEN_FRICTION_DIR", t.TempDir())
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	res := callTool(t, cs, "report_friction", map[string]any{
		"title":         "auth header rejected",
		"what_happened": "sent Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD and got 401",
	})
	require.True(t, res.IsError)
	assert.Contains(t, strings.ToLower(firstText(res)), "secret")
}

// The title and what happened are the minimum a human can act on. The SDK's
// schema check refuses a missing required field before the handler runs;
// a blank one reaches the store and is refused there.
func TestReportFriction_RequiresTitleAndHappened(t *testing.T) {
	t.Setenv("SAPIEN_FRICTION_DIR", t.TempDir())
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	res := callTool(t, cs, "report_friction", map[string]any{"title": "something"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "what_happened")

	res = callTool(t, cs, "report_friction", map[string]any{"title": "something", "what_happened": "   "})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_INVALID")
}
