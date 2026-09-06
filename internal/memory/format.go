package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/spec"
)

// FileName returns the file name (no directory) a memory with the given ID is
// stored under: the ID's ULID suffix (the "mem_" prefix stripped) plus ".md".
func FileName(id string) string {
	return strings.TrimPrefix(id, "mem_") + ".md"
}

// Parse parses src (a full "*.md" memory file: YAML front matter delimited by
// "---" lines, then a Markdown body) into a domain.Memory.
//
// The front matter is validated against spec.Memory (spec.ValidateYAML), so a
// missing/malformed field is reported as an *errs.Error with code
// errs.Invalid whose message embeds the line numbers spec.ValidateYAML
// resolved (relative to the front-matter block, i.e. line 1 is the first
// line after the opening "---"). type defaults to "note" and status defaults
// to "active" when omitted; scope has no default (spec.Memory requires it).
func Parse(src []byte) (domain.Memory, error) {
	frontMatter, body, err := splitFrontMatter(src)
	if err != nil {
		return domain.Memory{}, err
	}

	if problems := spec.ValidateYAML(spec.Memory, frontMatter); len(problems) > 0 {
		return domain.Memory{}, frontMatterError(problems)
	}

	var m domain.Memory
	if err := yaml.Unmarshal(frontMatter, &m); err != nil {
		return domain.Memory{}, errs.Wrap(errs.Invalid, err, "parse memory front matter")
	}

	if m.Type == "" {
		m.Type = domain.MemoryNote
	}
	if m.Status == "" {
		m.Status = domain.MemoryActive
	}
	m.Text = strings.TrimSpace(string(body))
	return m, nil
}

// splitFrontMatter locates the "---" delimited front-matter block at the
// start of src and returns it (without the delimiters) alongside the
// remaining body bytes.
func splitFrontMatter(src []byte) (frontMatter, body []byte, err error) {
	lines := strings.Split(string(src), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, nil, errs.New(errs.Invalid, "memory file must start with a '---' front-matter delimiter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, nil, errs.New(errs.Invalid, "memory file front matter is missing its closing '---' delimiter")
	}
	frontMatter = []byte(strings.Join(lines[1:end], "\n"))
	body = []byte(strings.Join(lines[end+1:], "\n"))
	return frontMatter, body, nil
}

// frontMatterError builds an errs.Invalid error from spec.ValidateYAML
// problems, embedding each problem's line number (relative to the front
// matter block) and path in the message, and attaching the raw problems as a
// detail for callers that want structured access.
func frontMatterError(problems []spec.Problem) error {
	msgs := make([]string, len(problems))
	for i, p := range problems {
		if p.Line > 0 {
			msgs[i] = fmt.Sprintf("line %d: %s", p.Line, p.String())
		} else {
			msgs[i] = p.String()
		}
	}
	e := errs.New(errs.Invalid, "invalid memory front matter: %s", strings.Join(msgs, "; "))
	return e.WithDetail("problems", problems)
}

// frontMatter mirrors domain.Memory's front-matter fields in the field order
// PLAN.md §10 uses, independent of domain.Memory's own Go struct field order
// (which groups Status before Created/Updated for a different reason: JSON
// serialization). Format uses this exclusively for deterministic output;
// Parse unmarshals straight into domain.Memory, which is order-independent.
type frontMatter struct {
	ID       string                  `yaml:"id"`
	Type     domain.MemoryType       `yaml:"type,omitempty"`
	Scope    domain.MemoryScope      `yaml:"scope"`
	Subject  *domain.Subject         `yaml:"subject,omitempty"`
	Tags     []string                `yaml:"tags,omitempty"`
	Source   *domain.MemorySource    `yaml:"source,omitempty"`
	Created  string                  `yaml:"created"`
	Updated  string                  `yaml:"updated,omitempty"`
	Status   domain.MemoryStatus     `yaml:"status,omitempty"`
	Resolved *domain.ResolvedSubject `yaml:"resolved,omitempty"`
}

// Format renders m as a full "*.md" memory file: YAML front matter (field
// order per PLAN.md §10, empty fields omitted) delimited by "---" lines,
// followed by m.Text trimmed with a single trailing newline. Timestamps are
// RFC3339 in UTC.
func Format(m domain.Memory) []byte {
	fm := frontMatter{
		ID:     m.ID,
		Type:   m.Type,
		Scope:  m.Scope,
		Tags:   m.Tags,
		Status: m.Status,
	}
	if !m.Subject.IsZero() {
		s := m.Subject
		fm.Subject = &s
	}
	if m.Source.Kind != "" {
		s := m.Source
		fm.Source = &s
	}
	if !m.Created.IsZero() {
		fm.Created = m.Created.UTC().Format(time.RFC3339)
	}
	if !m.Updated.IsZero() {
		fm.Updated = m.Updated.UTC().Format(time.RFC3339)
	}
	if m.Resolved != nil {
		r := *m.Resolved
		if !r.ResolvedAt.IsZero() {
			r.ResolvedAt = r.ResolvedAt.UTC()
		}
		fm.Resolved = &r
	}

	var fmBuf bytes.Buffer
	enc := yaml.NewEncoder(&fmBuf)
	enc.SetIndent(2)
	_ = enc.Encode(fm)
	_ = enc.Close()

	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(fmBuf.Bytes())
	out.WriteString("---\n")
	if text := strings.TrimSpace(m.Text); text != "" {
		out.WriteString(text)
		out.WriteString("\n")
	}
	return out.Bytes()
}

// ReadFile reads and parses the memory file at path, setting FilePath and
// Hash (the sha256 hex digest of the file's raw bytes) on the result.
func ReadFile(path string) (domain.Memory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Memory{}, errs.Wrap(errs.Internal, err, "read memory file %s", path)
	}
	m, err := Parse(data)
	if err != nil {
		return domain.Memory{}, err
	}
	m.FilePath = path
	m.Hash = hashBytes(data)
	return m, nil
}

// WriteFile atomically writes Format(m) to m.FilePath (create parent
// directories as needed, write to a temp file in the same directory, then
// rename over the destination).
func WriteFile(m domain.Memory) error {
	if m.FilePath == "" {
		return errs.New(errs.Invalid, "memory has no file path to write to")
	}
	dir := filepath.Dir(m.FilePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "create memory directory %s", dir)
	}

	tmp, err := os.CreateTemp(dir, ".mem-*.tmp")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "create temp file in %s", dir)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(Format(m)); err != nil {
		tmp.Close()
		return errs.Wrap(errs.Internal, err, "write memory temp file")
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "close memory temp file")
	}
	if err := os.Rename(tmpPath, m.FilePath); err != nil {
		return errs.Wrap(errs.Internal, err, "rename memory temp file to %s", m.FilePath)
	}
	return nil
}

// hashBytes returns the sha256 hex digest of data.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
