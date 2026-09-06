package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Printer is the single place commands write output through, so that human
// and --json output stay consistent and centralized. When IsJSON is true,
// commands are expected to call JSON exactly once and never Line or Table.
type Printer struct {
	Stdout   io.Writer
	Stderr   io.Writer
	jsonMode bool // set from --json; see IsJSON and the JSON method below
	Color    bool // true unless --no-color or $NO_COLOR is set
}

// NewPrinter builds a Printer. jsonMode mirrors the --json flag; color
// mirrors "colored output is allowed" (i.e. --no-color was not given and
// NO_COLOR is unset).
func NewPrinter(stdout, stderr io.Writer, jsonMode, color bool) *Printer {
	return &Printer{Stdout: stdout, Stderr: stderr, jsonMode: jsonMode, Color: color}
}

// ColorEnabled implements Sapien's one colour-output rule, applied uniformly
// to every ANSI sequence this package ever emits (currently just the dimmed
// response-header lines in `sapien call`'s human output; see Printer.Dim).
// Colour is enabled only when ALL of the following hold:
//
//   - noColorFlag is false (i.e. --no-color was not passed);
//   - $NO_COLOR is unset (an explicitly-empty value counts as unset, so
//     tests can simulate "unset" with t.Setenv("NO_COLOR", "") without
//     disturbing other tests); and
//   - either stdout is a terminal (a character device), or one of
//     $CLICOLOR_FORCE / $SAPIEN_FORCE_COLOR is set to a non-empty value
//     other than "0".
//
// This is what makes `sapien call ... > file.json` or `| less` come out
// plain even though NO_COLOR was never set: redirected/piped stdout is not
// a character device, so colour is off by default there, and only the
// force env vars (or an actual terminal) turn it back on.
func ColorEnabled(stdout io.Writer, noColorFlag bool) bool {
	if noColorFlag {
		return false
	}
	// Mirrors the rest of this package's existing convention (and the
	// tests below): an explicitly-empty NO_COLOR, as set by
	// t.Setenv("NO_COLOR", "") to simulate "unset" without disturbing
	// other tests, counts as unset. Any non-empty value disables colour.
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(stdout) || forceColorEnv()
}

// isTerminal reports whether w is a character device (an interactive
// terminal), as opposed to a regular file, pipe, or in-memory buffer. There
// is no x/term dependency here: os.File.Stat's mode bits are enough for the
// common case on macOS, Linux, and Windows.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// forceColorEnv reports whether either of the conventional "force colour
// even though stdout isn't a terminal" environment variables is set to a
// non-empty value other than "0" (so CLICOLOR_FORCE= and CLICOLOR_FORCE=0
// both leave colour off, matching the CLICOLOR convention).
func forceColorEnv() bool {
	for _, name := range []string{"CLICOLOR_FORCE", "SAPIEN_FORCE_COLOR"} {
		if v := os.Getenv(name); v != "" && v != "0" {
			return true
		}
	}
	return false
}

// IsJSON reports whether --json was set for this invocation.
func (p *Printer) IsJSON() bool { return p.jsonMode }

// Dim renders s the way `sapien call`'s human output dims response-header
// lines: wrapped in the ANSI "faint" escape when p.Color is true, returned
// unchanged otherwise. This (and Bold/Red/Green below) is the one place in
// the package that ever emits an ANSI escape sequence, so every caller's
// colour output automatically follows the ColorEnabled rule.
func (p *Printer) Dim(s string) string { return p.ansi(s, "2") }

// Bold renders s in bold when p.Color is true, unchanged otherwise.
func (p *Printer) Bold(s string) string { return p.ansi(s, "1") }

// Red renders s in red when p.Color is true, unchanged otherwise.
func (p *Printer) Red(s string) string { return p.ansi(s, "31") }

// Green renders s in green when p.Color is true, unchanged otherwise.
func (p *Printer) Green(s string) string { return p.ansi(s, "32") }

// ansi wraps s in the given SGR code, or returns it unchanged when colour
// output is disabled for this Printer. Human-readable glyphs (✓ ✗ ▸) never
// go through here: they are plain text regardless of colour.
func (p *Printer) ansi(s, code string) string {
	if !p.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// JSON writes v to Stdout as a single, pretty-printed JSON document. Commands
// running under --json call this exactly once with their result.
func (p *Printer) JSON(v any) error {
	enc := json.NewEncoder(p.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Line writes one formatted, newline-terminated line to Stdout. format
// should not itself end in "\n".
func (p *Printer) Line(format string, args ...any) {
	fmt.Fprintf(p.Stdout, format+"\n", args...)
}

// Errorf writes one formatted, newline-terminated line to Stderr, for
// non-fatal warnings a command wants to surface directly (a command's own
// returned error is what Execute formats as the final failure).
func (p *Printer) Errorf(format string, args ...any) {
	fmt.Fprintf(p.Stderr, format+"\n", args...)
}

// Table prints a simple, aligned, whitespace-separated table to Stdout: a
// header row followed by one row per entry in rows. Columns are padded to
// the widest cell (including the header) in that column; no external
// dependency, no color.
func (p *Printer) Table(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	p.writeRow(headers, widths)
	for _, row := range rows {
		p.writeRow(row, widths)
	}
}

func (p *Printer) writeRow(cells []string, widths []int) {
	parts := make([]string, len(cells))
	for i, cell := range cells {
		w := 0
		if i < len(widths) {
			w = widths[i]
		}
		parts[i] = fmt.Sprintf("%-*s", w, cell)
	}
	fmt.Fprintln(p.Stdout, strings.TrimRight(strings.Join(parts, "  "), " "))
}
