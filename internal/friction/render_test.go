package friction_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/friction"
)

func TestRender_TitlePrefixed(t *testing.T) {
	title, _ := friction.Render(friction.Report{Title: "get_api returned the wrong shape"}, friction.Host{OS: "darwin"})
	assert.Equal(t, "[agent friction] get_api returned the wrong shape", title)
}

func TestRender_BodySections(t *testing.T) {
	created := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	r := friction.Report{
		Title: "x", Category: friction.CategoryBug, Tool: "get_api",
		Tried: "fetching an operation's contract", Happened: "no request_example field",
		WouldHelp: "document the fallback", Workspace: "logistics", Client: "claude-code",
		Version: "1.2.0", Created: created,
	}
	_, body := friction.Render(r, friction.Host{OS: "darwin"})

	assert.Contains(t, body, "## What I was trying to do")
	assert.Contains(t, body, "fetching an operation's contract")
	assert.Contains(t, body, "## What happened")
	assert.Contains(t, body, "no request_example field")
	assert.Contains(t, body, "## What would have helped")
	assert.Contains(t, body, "document the fallback")
	assert.Contains(t, body, "---")
	assert.Contains(t, body, "Filed by claude-code via Sapien 1.2.0 (darwin), workspace \"logistics\", 2026-09-14T12:00:00Z, category bug, tool get_api")
}

// A report with no Tried/WouldHelp omits those headings.
func TestRender_OmitsEmptySections(t *testing.T) {
	r := friction.Report{Title: "x", Happened: "the tool errored", Category: friction.CategoryBug}
	_, body := friction.Render(r, friction.Host{OS: "linux"})
	assert.NotContains(t, body, "What I was trying to do")
	assert.NotContains(t, body, "What would have helped")
	assert.Contains(t, body, "## What happened")
}

// Render carries no file paths or hostnames: the footer only names the
// client, version, OS family, workspace *name*, timestamp, category, tool.
func TestRender_NoFilePathsOrHostnames(t *testing.T) {
	r := friction.Report{
		Title: "x", Happened: "y", Path: "/Users/alice/.sapien/friction/fr_123.md",
		Workspace: "logistics",
	}
	_, body := friction.Render(r, friction.Host{OS: "darwin"})
	assert.NotContains(t, body, "/Users/alice")
	assert.NotContains(t, body, ".sapien/friction")
}
