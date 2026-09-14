package friction

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/memory"
	"github.com/gs-sinha/sapien/internal/store"
)

// frictionDirEnv lets a test (or an operator) point the store at a
// directory other than the default without threading a flag through every
// caller. Both `sapien friction ...` and the report_friction MCP tool read
// it the same way, via New(""), so the two surfaces agree by construction
// instead of by convention.
const frictionDirEnv = "SAPIEN_FRICTION_DIR"

// Store persists Reports as one "<dir>/<id>.md" file each: YAML front
// matter plus a Markdown body, the same shape internal/memory/format.go
// uses for memory files.
type Store struct {
	dir string
}

// New builds a Store rooted at dir. An empty dir resolves, in order: the
// SAPIEN_FRICTION_DIR environment variable (tests and operators), else
// "~/.sapien/friction", else "<TempDir>/.sapien/friction" if the home
// directory cannot be determined -- mirroring internal/gitsrc.New's default
// resolution for its own cache directory.
func New(dir string) *Store {
	if dir == "" {
		if v := os.Getenv(frictionDirEnv); v != "" {
			dir = v
		} else if home, err := os.UserHomeDir(); err == nil && home != "" {
			dir = filepath.Join(home, ".sapien", "friction")
		} else {
			dir = filepath.Join(os.TempDir(), ".sapien", "friction")
		}
	}
	return &Store{dir: dir}
}

// Dir returns the directory this Store persists reports under.
func (s *Store) Dir() string { return s.dir }

// Create validates r, defaults Category to bug and Status to pending,
// refuses it if any text field looks like it carries a secret (reports are
// posted publicly on send), assigns ID and Created, and writes the file.
func (s *Store) Create(ctx context.Context, r Report) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.Cancelled, err, "friction: create cancelled")
	}
	if strings.TrimSpace(r.Title) == "" {
		return nil, errs.New(errs.Invalid, "friction report needs a title").
			WithHint("pass a one-line title: what was hard")
	}
	if strings.TrimSpace(r.Happened) == "" {
		return nil, errs.New(errs.Invalid, "friction report needs \"what happened\"").
			WithHint("describe what happened instead of what you expected")
	}
	if r.Category == "" {
		r.Category = CategoryBug
	}
	if !validCategory(r.Category) {
		return nil, errs.New(errs.Invalid, "friction: unknown category %q", r.Category).
			WithHint("use one of: " + categoryNames())
	}
	if r.Status == "" {
		r.Status = StatusPending
	}

	if kinds := scanSecrets(r); len(kinds) > 0 {
		return nil, errs.New(errs.Invalid,
			"report looks like it contains a secret (%s); remove it, this report is posted publicly",
			strings.Join(kinds, ", ")).
			WithDetail("kinds", kinds)
	}

	r.ID = store.NewID("fr")
	r.Created = time.Now().UTC()
	r.Path = s.path(r.ID)

	if err := writeReportFile(r); err != nil {
		return nil, err
	}
	out := r
	return &out, nil
}

// scanSecrets runs memory.ScanSecrets over every user-supplied text field of
// r (not Workspace/Client/Version/ID, which the caller sets, not the
// agent), returning the deduplicated kinds found across all of them.
func scanSecrets(r Report) []string {
	joined := strings.Join([]string{r.Title, r.Tried, r.Happened, r.WouldHelp, r.Tool}, "\n")
	return memory.ScanSecrets(joined)
}

// List returns every report in the store, newest (by Created) first. A
// missing store directory is not an error: it just means no report has
// ever been filed, so List returns an empty slice.
func (s *Store) List(ctx context.Context) ([]Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.Cancelled, err, "friction: list cancelled")
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.Wrap(errs.Internal, err, "friction: reading %s", s.dir)
	}

	var reports []Report
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		r, err := readReportFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Created.After(reports[j].Created) })
	return reports, nil
}

// Get reads one report by id. There is no dedicated "friction not found"
// code in internal/errs (see the package's Code list), so a missing report
// is reported as errs.Invalid with a hint, the closest fit the existing
// codes offer.
func (s *Store) Get(ctx context.Context, id string) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.Cancelled, err, "friction: get cancelled")
	}
	path := s.path(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errs.New(errs.FrictionNotFound, "no friction report %q", id).
				WithHint("check `sapien friction list`")
		}
		return nil, errs.Wrap(errs.Internal, err, "friction: reading %s", path)
	}
	r, err := parseReport(data)
	if err != nil {
		return nil, err
	}
	r.Path = path
	return &r, nil
}

// MarkSent records that r has been posted: status "sent", the Discussion
// url, and the time it happened.
func (s *Store) MarkSent(ctx context.Context, id, url string) (*Report, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	r.Status = StatusSent
	r.SentURL = url
	r.SentAt = time.Now().UTC()
	if err := writeReportFile(*r); err != nil {
		return nil, err
	}
	return r, nil
}

// Drop deletes a report's file.
func (s *Store) Drop(ctx context.Context, id string) error {
	r, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := os.Remove(r.Path); err != nil {
		return errs.Wrap(errs.Internal, err, "friction: removing %s", r.Path)
	}
	return nil
}

// path returns the file path a report with the given id is stored under.
func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".md")
}

// --- file format: YAML front matter + Markdown body, mirroring
// internal/memory/format.go's Parse/Format --------------------------------

// frontMatter mirrors Report's persisted fields in a fixed, deterministic
// field order, independent of Report's own Go struct field order (which
// groups by "what the agent said" vs. "what send recorded" for readability
// rather than for serialization).
type frontMatter struct {
	ID        string   `yaml:"id"`
	Title     string   `yaml:"title"`
	Category  Category `yaml:"category"`
	Tool      string   `yaml:"tool,omitempty"`
	Workspace string   `yaml:"workspace,omitempty"`
	Client    string   `yaml:"client,omitempty"`
	Version   string   `yaml:"version,omitempty"`
	Created   string   `yaml:"created"`
	Status    string   `yaml:"status"`
	SentURL   string   `yaml:"sent_url,omitempty"`
	SentAt    string   `yaml:"sent_at,omitempty"`
}

// The three body headings PLAN's friction-report format uses (also the
// headings Render's public body reuses, so a human sees exactly the same
// section titles in `friction show` and in what gets posted).
const (
	headingTried     = "## What I was trying to do"
	headingHappened  = "## What happened"
	headingWouldHelp = "## What would have helped"
)

// formatReport renders r as a full "*.md" friction file. Timestamps are
// RFC3339 in UTC; an empty section (Tried/WouldHelp) is omitted entirely
// rather than written as an empty heading.
func formatReport(r Report) []byte {
	fm := frontMatter{
		ID: r.ID, Title: r.Title, Category: r.Category, Tool: r.Tool,
		Workspace: r.Workspace, Client: r.Client, Version: r.Version,
		Status: r.Status, SentURL: r.SentURL,
	}
	if !r.Created.IsZero() {
		fm.Created = r.Created.UTC().Format(time.RFC3339)
	}
	if !r.SentAt.IsZero() {
		fm.SentAt = r.SentAt.UTC().Format(time.RFC3339)
	}

	var fmBuf bytes.Buffer
	enc := yaml.NewEncoder(&fmBuf)
	enc.SetIndent(2)
	_ = enc.Encode(fm)
	_ = enc.Close()

	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(fmBuf.Bytes())
	out.WriteString("---\n\n")
	out.WriteString(reportBody(r))
	return out.Bytes()
}

// reportBody renders just the three-heading Markdown body (no front
// matter), shared by formatReport and Render (render.go) so the stored file
// and the posted Discussion carry the same section text.
func reportBody(r Report) string {
	var b strings.Builder
	if strings.TrimSpace(r.Tried) != "" {
		b.WriteString(headingTried)
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(r.Tried))
		b.WriteString("\n\n")
	}
	b.WriteString(headingHappened)
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(r.Happened))
	b.WriteString("\n\n")
	if strings.TrimSpace(r.WouldHelp) != "" {
		b.WriteString(headingWouldHelp)
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(r.WouldHelp))
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

// parseReport parses src (front matter + body) into a Report. Path is left
// unset; callers that read from disk set it themselves (Get, readReportFile).
func parseReport(src []byte) (Report, error) {
	fmBytes, body, err := splitFrontMatter(src)
	if err != nil {
		return Report{}, err
	}

	var fm frontMatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return Report{}, errs.Wrap(errs.Invalid, err, "friction: parsing front matter")
	}

	r := Report{
		ID: fm.ID, Title: fm.Title, Category: fm.Category, Tool: fm.Tool,
		Workspace: fm.Workspace, Client: fm.Client, Version: fm.Version,
		Status: fm.Status, SentURL: fm.SentURL,
	}
	if fm.Created != "" {
		t, err := time.Parse(time.RFC3339, fm.Created)
		if err != nil {
			return Report{}, errs.Wrap(errs.Invalid, err, "friction: parsing created timestamp %q", fm.Created)
		}
		r.Created = t
	}
	if fm.SentAt != "" {
		t, err := time.Parse(time.RFC3339, fm.SentAt)
		if err != nil {
			return Report{}, errs.Wrap(errs.Invalid, err, "friction: parsing sent_at timestamp %q", fm.SentAt)
		}
		r.SentAt = t
	}

	r.Tried, r.Happened, r.WouldHelp = parseReportBody(string(body))
	return r, nil
}

// parseReportBody recovers Tried/Happened/WouldHelp from a rendered body by
// splitting on the three known headings; text before the first heading (or
// under an unrecognized one) is discarded, matching how reportBody never
// writes anything outside them.
func parseReportBody(body string) (tried, happened, wouldHelp string) {
	sections := map[string]*strings.Builder{}
	var current *strings.Builder
	for _, line := range strings.Split(body, "\n") {
		switch strings.TrimSpace(line) {
		case headingTried:
			b := &strings.Builder{}
			sections["tried"] = b
			current = b
			continue
		case headingHappened:
			b := &strings.Builder{}
			sections["happened"] = b
			current = b
			continue
		case headingWouldHelp:
			b := &strings.Builder{}
			sections["would_help"] = b
			current = b
			continue
		}
		if current != nil {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	get := func(key string) string {
		if b, ok := sections[key]; ok {
			return strings.TrimSpace(b.String())
		}
		return ""
	}
	return get("tried"), get("happened"), get("would_help")
}

// splitFrontMatter locates the "---" delimited front-matter block at the
// start of src and returns it (without the delimiters) alongside the
// remaining body bytes. Copied from internal/memory/format.go's
// splitFrontMatter (unexported there, and this package intentionally has no
// dependency on internal/memory beyond ScanSecrets) rather than shared.
func splitFrontMatter(src []byte) (frontMatterBytes, body []byte, err error) {
	lines := strings.Split(string(src), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, nil, errs.New(errs.Invalid, "friction file must start with a '---' front-matter delimiter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, nil, errs.New(errs.Invalid, "friction file front matter is missing its closing '---' delimiter")
	}
	frontMatterBytes = []byte(strings.Join(lines[1:end], "\n"))
	body = []byte(strings.Join(lines[end+1:], "\n"))
	return frontMatterBytes, body, nil
}

// writeReportFile atomically writes formatReport(r) to r.Path: a temp file
// in the same directory, then a rename, so a reader never observes a
// partially written file (mirrors internal/memory/format.go's WriteFile).
func writeReportFile(r Report) error {
	if r.Path == "" {
		return errs.New(errs.Invalid, "friction report has no file path to write to")
	}
	dir := filepath.Dir(r.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "friction: creating directory %s", dir)
	}

	tmp, err := os.CreateTemp(dir, ".fr-*.tmp")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "friction: creating temp file in %s", dir)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(formatReport(r)); err != nil {
		tmp.Close()
		return errs.Wrap(errs.Internal, err, "friction: writing temp file")
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "friction: closing temp file")
	}
	if err := os.Rename(tmpPath, r.Path); err != nil {
		return errs.Wrap(errs.Internal, err, "friction: renaming temp file to %s", r.Path)
	}
	return nil
}

// readReportFile reads and parses the report file at path, setting Path on
// the result.
func readReportFile(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, errs.Wrap(errs.Internal, err, "friction: reading %s", path)
	}
	r, err := parseReport(data)
	if err != nil {
		return Report{}, err
	}
	r.Path = path
	return r, nil
}
