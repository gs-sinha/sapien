// Package logs turns an app's logcat output into what ui steps assert on:
// the raw lines, and the network calls the app logged, parsed into
// structured records by a per-app format (log_format in the environment's
// apps: entry).
package logs

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Formats.
const (
	FormatRiderBox           = "rider-box"            // rider_app_fe: ╔═ [HTTP]|[DIO] API REQUEST-RESPONSE LOG boxes
	FormatOverwatchAPILogger = "overwatch-api-logger" // overwatch: 🟡/🟢/🔴 API LOGGER → REQUEST|RESPONSE|ERROR
	FormatRaw                = "raw"                  // lines only
)

// Formats lists the known log formats.
func Formats() []string { return []string{FormatRiderBox, FormatOverwatchAPILogger, FormatRaw} }

// APICall is one network call reconstructed from the app's logs. It is
// what body.logs.api holds, one map per call (see Map).
type APICall struct {
	Method         string
	URL            string
	Path           string
	Status         int
	RequestHeaders any
	RequestBody    any
	ResponseBody   any
	Error          string
}

// Map renders c as the CEL-facing value:
// {method, url, path, status, request: {headers, body}, response: {body}, error}.
func (c APICall) Map() map[string]any {
	m := map[string]any{
		"method":   c.Method,
		"url":      c.URL,
		"path":     c.Path,
		"status":   c.Status,
		"request":  map[string]any{"headers": orEmpty(c.RequestHeaders), "body": c.RequestBody},
		"response": map[string]any{"body": c.ResponseBody},
	}
	if c.Error != "" {
		m["error"] = c.Error
	}
	return m
}

func orEmpty(v any) any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

// Buffer collects a device's log lines as they stream in. Mark/Since give a
// step the lines logged while it ran. Safe for concurrent use: the logcat
// reader appends while the runner reads.
type Buffer struct {
	mu    sync.Mutex
	lines []string
	max   int
	base  int // absolute index of lines[0], after trimming
}

// NewBuffer keeps at most max lines (oldest dropped first); max <= 0 means
// 200k.
func NewBuffer(max int) *Buffer {
	if max <= 0 {
		max = 200_000
	}
	return &Buffer{max: max}
}

// Append adds one raw logcat line.
func (b *Buffer) Append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if over := len(b.lines) - b.max; over > 0 {
		b.lines = append([]string(nil), b.lines[over:]...)
		b.base += over
	}
}

// Mark returns a position Since can later slice from.
func (b *Buffer) Mark() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.base + len(b.lines)
}

// Since returns a copy of the lines appended after mark.
func (b *Buffer) Since(mark int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := mark - b.base
	if i < 0 {
		i = 0
	}
	if i >= len(b.lines) {
		return nil
	}
	return append([]string(nil), b.lines[i:]...)
}

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// brief: `I/flutter ( 1234): msg`; threadtime: `10-09 12:00:00.000  1234  1240 I flutter : msg`;
	// epoch: `  1696841234.123  1234  1240 I flutter : msg`.
	briefRe      = regexp.MustCompile(`^[VDIWEF]/[^(]+\(\s*\d+\):\s?`)
	threadtimeRe = regexp.MustCompile(`^\s*[\d.:\- ]+\s+\d+\s+\d+\s+[VDIWEF]\s+[^:]+?\s*:\s?`)
)

// Clean strips a logcat line down to the app's own message: the logcat
// header, ANSI colour codes, and the `logger` package's PrettyPrinter
// decoration (the `│ ` border and the per-level emoji it prefixes to every
// line).
func Clean(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if loc := briefRe.FindStringIndex(line); loc != nil {
		line = line[loc[1]:]
	} else if loc := threadtimeRe.FindStringIndex(line); loc != nil {
		line = line[loc[1]:]
	}
	line = ansiRe.ReplaceAllString(line, "")
	if strings.HasPrefix(line, "│ ") {
		line = strings.TrimPrefix(line, "│ ")
		for _, e := range levelEmojis {
			if strings.HasPrefix(line, e) {
				line = strings.TrimPrefix(line, e)
				break
			}
		}
	}
	return line
}

// levelEmojis are logger's PrettyPrinter defaults (logger 1.x and 2.x).
var levelEmojis = []string{"💡 ", "⛔ ", "🐛 ", "⚠️ ", "👾 ", "🔍 ", "📃 "}

// isBorder reports a PrettyPrinter frame line (top/bottom/separator).
func isBorder(s string) bool {
	return strings.HasPrefix(s, "┌") || strings.HasPrefix(s, "└") || strings.HasPrefix(s, "├")
}

// Parse extracts API calls from raw logcat lines in the given format; an
// unknown or raw format yields none.
func Parse(format string, raw []string) []APICall {
	clean := make([]string, len(raw))
	for i, l := range raw {
		clean[i] = Clean(l)
	}
	switch format {
	case FormatRiderBox:
		return parseRiderBox(clean)
	case FormatOverwatchAPILogger:
		return parseOverwatch(clean)
	default:
		return nil
	}
}

// --- rider-box ---------------------------------------------------------------

// parseRiderBox reads rider_app_fe's boxes (lib/core/network/
// network_api_service.dart _logApiDebug and dio_api_service.dart
// _DioLogInterceptor):
//
//	╔═══ [HTTP] API REQUEST-RESPONSE LOG ═══
//	║ API POST /auth
//	╠═══
//	║ Request URL: https://host/v1/auth
//	║ Request headers:
//	║   { ...json... }
//	║ Request body:
//	║   { ...json... }
//	╠═══
//	║ Status: 200
//	║ Response:
//	║   { ...json... }
//	╚═══
//
// A `║` line with three spaces after it continues the current section; any
// other `║` line starts a new labelled one.
func parseRiderBox(lines []string) []APICall {
	var calls []APICall
	var cur *APICall
	var section string
	var buf []string

	flush := func() {
		if cur == nil || section == "" {
			buf = nil
			return
		}
		v := parseJSONish(strings.Join(buf, "\n"))
		switch section {
		case "headers":
			cur.RequestHeaders = v
		case "body":
			cur.RequestBody = v
		case "response":
			cur.ResponseBody = v
		}
		section, buf = "", nil
	}

	for _, l := range lines {
		i := strings.IndexAny(l, "╔║╠╚")
		if i < 0 {
			continue
		}
		l = l[i:]
		switch {
		case strings.HasPrefix(l, "╔"):
			if strings.Contains(l, "API REQUEST-RESPONSE LOG") {
				cur = &APICall{}
				section, buf = "", nil
			}
		case cur == nil:
		case strings.HasPrefix(l, "╠"):
			flush()
		case strings.HasPrefix(l, "╚"):
			flush()
			calls = append(calls, *cur)
			cur = nil
		case strings.HasPrefix(l, "║"):
			body := strings.TrimPrefix(l, "║")
			if strings.HasPrefix(body, "   ") && section != "" {
				buf = append(buf, body[3:])
				continue
			}
			flush()
			body = strings.TrimSpace(body)
			switch {
			case strings.HasPrefix(body, "API "):
				if f := strings.Fields(strings.TrimPrefix(body, "API ")); len(f) >= 1 {
					cur.Method = f[0]
					if len(f) >= 2 {
						cur.Path = f[1]
					}
				}
			case strings.HasPrefix(body, "Request URL:"):
				cur.URL = strings.TrimSpace(strings.TrimPrefix(body, "Request URL:"))
				if u, err := url.Parse(cur.URL); err == nil && u.Path != "" {
					cur.Path = u.Path
				}
			case strings.HasPrefix(body, "Request headers:"):
				section = "headers"
			case strings.HasPrefix(body, "Request body:"):
				section = "body"
			case strings.HasPrefix(body, "Status:"):
				cur.Status, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(body, "Status:")))
			case strings.HasPrefix(body, "Response:"):
				section = "response"
			}
		}
	}
	return calls
}

// --- overwatch-api-logger ----------------------------------------------------

// parseOverwatch reads overwatch's Dio interceptor (lib/services/
// baseservice.dart), which logs the request and the response (or error)
// as separate entries:
//
//	══════ 🟡 API LOGGER → REQUEST ══════=
//	→ URL: https://host/v1/x
//	→ METHOD: POST
//	→ HEADERS: { ...json... }
//	→ BODY: { ...json... }
//
//	══════ 🟢 API LOGGER → RESPONSE ══════=
//	→ URL: https://host/v1/x
//	→ STATUS CODE: 200
//	→ DATA: { ...json... }
//
// A response or error is paired with the oldest unanswered request for the
// same URL; an unanswered request is still reported (status 0).
func parseOverwatch(lines []string) []APICall {
	type entry struct {
		kind   string // REQUEST | RESPONSE | ERROR
		fields map[string]string
	}
	var entries []entry
	var cur *entry
	var key string
	var buf []string

	flushField := func() {
		if cur != nil && key != "" {
			cur.fields[key] = strings.TrimSpace(strings.Join(buf, "\n"))
		}
		key, buf = "", nil
	}
	flushEntry := func() {
		flushField()
		if cur != nil {
			entries = append(entries, *cur)
			cur = nil
		}
	}

	for _, l := range lines {
		if isBorder(l) {
			flushEntry()
			continue
		}
		if i := strings.Index(l, "API LOGGER → "); i >= 0 {
			flushEntry()
			rest := l[i+len("API LOGGER → "):]
			kind := strings.Fields(rest)
			if len(kind) > 0 {
				cur = &entry{kind: kind[0], fields: map[string]string{}}
			}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(l, "→ ") {
			flushField()
			kv := strings.TrimPrefix(l, "→ ")
			if j := strings.Index(kv, ":"); j > 0 {
				key = strings.TrimSpace(kv[:j])
				buf = []string{strings.TrimSpace(kv[j+1:])}
			}
			continue
		}
		if key != "" {
			buf = append(buf, l)
		}
	}
	flushEntry()

	var calls []APICall
	pending := map[string][]int{} // url -> indexes into calls awaiting an answer
	for _, e := range entries {
		u := e.fields["URL"]
		switch e.kind {
		case "REQUEST":
			c := APICall{
				Method:         e.fields["METHOD"],
				URL:            u,
				Path:           urlPath(u),
				RequestHeaders: parseJSONish(e.fields["HEADERS"]),
				RequestBody:    parseJSONish(e.fields["BODY"]),
			}
			calls = append(calls, c)
			pending[u] = append(pending[u], len(calls)-1)
		case "RESPONSE", "ERROR":
			idx := -1
			if q := pending[u]; len(q) > 0 {
				idx, pending[u] = q[0], q[1:]
			} else {
				calls = append(calls, APICall{URL: u, Path: urlPath(u)})
				idx = len(calls) - 1
			}
			c := &calls[idx]
			if e.kind == "RESPONSE" {
				c.Status, _ = strconv.Atoi(e.fields["STATUS CODE"])
				c.ResponseBody = parseJSONish(e.fields["DATA"])
			} else {
				c.Error = e.fields["MESSAGE"]
				c.ResponseBody = parseJSONish(e.fields["RESPONSE"])
			}
		}
	}
	return calls
}

func urlPath(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Path
	}
	return ""
}

// parseJSONish decodes s as JSON when it is JSON, else returns it as a
// string ("null" and "" become nil).
func parseJSONish(s string) any {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}

// CleanLines returns lines with Clean applied, dropping PrettyPrinter
// frame lines and empty lines: what body.logs.lines holds.
func CleanLines(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		c := Clean(l)
		if strings.TrimSpace(c) == "" || isBorder(c) {
			continue
		}
		out = append(out, c)
	}
	return out
}
