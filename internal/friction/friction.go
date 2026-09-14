// Package friction implements the friction-report loop described in
// PLAN.md's "an agent that hits friction should be able to say so" thread:
// an MCP client (an agent) files a report with report_friction, it is
// queued on disk under ~/.sapien/friction as a Markdown-with-front-matter
// file -- never sent anywhere by the agent -- a human reviews it with
// `sapien friction list/show`, and `sapien friction send` posts it as a
// GitHub Discussion through the `gh` CLI, which already holds the user's
// auth (Sapien stores no credentials, exactly as it does for git in
// internal/gitsrc). Reports are posted publicly, so Store.Create refuses
// anything that looks like a secret and the human always sees the rendered
// title/body before send asks to post it.
package friction

import (
	"strings"
	"time"
)

// Category classifies a friction report. The zero value is not valid on a
// stored report; Store.Create defaults an empty Category to CategoryBug.
type Category string

const (
	CategoryBug     Category = "bug"
	CategoryIdea    Category = "idea"
	CategoryDocs    Category = "docs"
	CategoryMissing Category = "missing"
)

// categories lists every valid Category, in the order Create's error
// message and the CLI's --category help text present them.
var categories = []Category{CategoryBug, CategoryIdea, CategoryDocs, CategoryMissing}

// validCategory reports whether c is one of the known categories.
func validCategory(c Category) bool {
	for _, want := range categories {
		if c == want {
			return true
		}
	}
	return false
}

// categoryNames renders the known categories as "bug, idea, docs, missing",
// for an "unknown category" error's hint.
func categoryNames() string {
	names := make([]string, len(categories))
	for i, c := range categories {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// Report statuses: pending is what Create always produces; sent is what
// MarkSent moves a report to once `sapien friction send` has posted it.
const (
	StatusPending = "pending"
	StatusSent    = "sent"
)

// Report is one queued friction report (PLAN.md's friction-report loop).
// Every field except Path round-trips through the on-disk Markdown file
// (Format/Parse in store.go); Path is where Get/List found it, and is
// never itself written into the file.
type Report struct {
	ID        string    `json:"id"` // "fr_<ULID>", assigned by Store.Create
	Title     string    `json:"title"`
	Category  Category  `json:"category"`
	Tool      string    `json:"tool,omitempty"`       // optional: the Sapien tool or command involved
	Tried     string    `json:"tried,omitempty"`      // what the agent was trying to do
	Happened  string    `json:"happened"`             // what happened instead (required)
	WouldHelp string    `json:"would_help,omitempty"` // what would have helped
	Workspace string    `json:"workspace,omitempty"`  // workspace name (not path)
	Client    string    `json:"client,omitempty"`     // MCP client name, or "cli"
	Version   string    `json:"version,omitempty"`    // Sapien version at report time, "" if unknown
	Created   time.Time `json:"created"`
	Status    string    `json:"status"` // "pending" | "sent"
	SentURL   string    `json:"sent_url,omitempty"`
	SentAt    time.Time `json:"sent_at,omitempty"`
	Path      string    `json:"path,omitempty"` // file path; not persisted in the front matter itself
}
