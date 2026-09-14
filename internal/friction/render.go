package friction

import (
	"fmt"
	"strings"
	"time"
)

// Host is the (deliberately small) environment detail Render's footer
// carries: only what says "this came from a real run of Sapien", never
// anything identifying the machine (no file paths, no hostnames -- see the
// package doc comment on why the report is safe to post publicly).
type Host struct {
	OS string // typically runtime.GOOS
}

// Render is what both `sapien friction show` and `sapien friction send`
// use, and what send actually posts: the two must never diverge, or a human
// approving a report in `show` would be approving different text than what
// gets published.
//
// title is "[agent friction] <Title>". body is the three sections
// (reportBody, store.go -- identical to what's on disk) plus a "---"
// footer naming who filed it, with what Sapien build, on what OS, in which
// workspace, when, and its category/tool -- attribution for a human triaging
// the queue, with nothing that identifies the machine or its environment.
func Render(r Report, host Host) (title, body string) {
	title = "[agent friction] " + r.Title

	var b strings.Builder
	b.WriteString(reportBody(r))
	b.WriteString("---\n")
	created := ""
	if !r.Created.IsZero() {
		created = r.Created.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "Filed by %s via Sapien %s (%s), workspace %q, %s, category %s, tool %s",
		r.Client, r.Version, host.OS, r.Workspace, created, r.Category, r.Tool)

	return title, strings.TrimRight(b.String(), "\n") + "\n"
}
