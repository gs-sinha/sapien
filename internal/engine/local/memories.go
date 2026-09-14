package local

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/memory"
)

// memoryAPI implements engine.MemoryAPI over a Local (PLAN §10-13, §26).
type memoryAPI struct{ l *Local }

var _ engine.MemoryAPI = (*memoryAPI)(nil)

func (m *memoryAPI) Create(ctx context.Context, mem domain.Memory) (*domain.Memory, error) {
	m.warnOnSecrets(mem.Text)
	created, err := m.l.memStore.Create(ctx, mem)
	if err != nil {
		return nil, err
	}
	m.l.emit(domain.EventMemoryCreated, *created)
	m.l.enqueueSemanticMemoryIndex([]domain.Memory{*created})
	m.l.refreshOperationKnowledge(ctx, operationSubjectIDs(created.Subject))
	return created, nil
}

func (m *memoryAPI) Get(ctx context.Context, id string) (*domain.Memory, error) {
	return m.l.memStore.Get(ctx, id)
}

func (m *memoryAPI) Update(ctx context.Context, mem domain.Memory) (*domain.Memory, error) {
	m.warnOnSecrets(mem.Text)
	// Fetched before Update so a subject.operation change (or a rescope,
	// which is an Update under the hood — see cli/mcp's rescope verbs) also
	// refreshes the operation the memory is moving *away* from, not just
	// the one it's moving to; best effort, nil on any lookup failure (the
	// Update call below will itself surface a real error if mem.ID is bad).
	existing, _ := m.l.memStore.Get(ctx, mem.ID)

	updated, err := m.l.memStore.Update(ctx, mem)
	if err != nil {
		return nil, err
	}
	m.l.emit(domain.EventMemoryChanged, *updated)
	m.l.enqueueSemanticMemoryIndex([]domain.Memory{*updated})

	ids := operationSubjectIDs(updated.Subject)
	if existing != nil {
		ids = append(ids, operationSubjectIDs(existing.Subject)...)
	}
	m.l.refreshOperationKnowledge(ctx, dedupStrings(ids))
	return updated, nil
}

func (m *memoryAPI) Delete(ctx context.Context, id string) error {
	// Fetched before Delete purely to learn which operation's memory_text
	// needs refreshing afterward; best effort, nil on lookup failure.
	existing, _ := m.l.memStore.Get(ctx, id)

	if err := m.l.memStore.Delete(ctx, id); err != nil {
		return err
	}
	m.l.emit(domain.EventMemoryChanged, map[string]string{"id": id, "action": "deleted"})
	if existing != nil {
		m.l.refreshOperationKnowledge(ctx, operationSubjectIDs(existing.Subject))
	}
	return nil
}

func (m *memoryAPI) List(ctx context.Context, q domain.MemoryQuery) ([]domain.Memory, error) {
	return m.l.memStore.List(ctx, q)
}

func (m *memoryAPI) Search(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
	return m.l.memStore.Search(ctx, q)
}

func (m *memoryAPI) Relevant(ctx context.Context, subjects []domain.Subject, limit int) ([]domain.ScoredMemory, error) {
	return m.l.memStore.Relevant(ctx, subjects, limit)
}

func (m *memoryAPI) Reindex(ctx context.Context) error {
	_, err := m.l.memStore.Reindex(ctx)
	if err == nil {
		m.l.enqueueSemanticMemoryIndex(nil)
		// A bulk reindex can touch memories for any operation; recompute
		// memory_text for all of them rather than tracking exactly which
		// changed.
		m.l.refreshOperationKnowledge(ctx, nil)
	}
	return err
}

// refreshOperationKnowledge best-effort recomputes operations_fts'
// doc_text/memory_text for opIDs (nil/empty = every operation) after a
// memory write. Errors are logged and swallowed: a stale knowledge column
// only ever costs search ranking precision, never correctness, and the
// next memory write or Apply retries it anyway.
func (l *Local) refreshOperationKnowledge(ctx context.Context, opIDs []string) {
	if err := l.cat.RefreshOperationKnowledge(ctx, opIDs); err != nil {
		l.logger.Warn("refreshing operation knowledge (docs/memories) failed", "error", err, "operations", len(opIDs))
	}
}

// operationSubjectIDs returns subj's operation id as a single-element
// slice, or nil if subj has no operation subject at all. A field subject
// alongside an operation subject is already covered by the operation id
// alone: internal/memory's subjectRows keys a field-only memory_subjects
// row "<operation>#<field-path>", which RefreshOperationKnowledge's
// memory_text query already matches via that same operation id.
func operationSubjectIDs(subj domain.Subject) []string {
	if subj.Operation == "" {
		return nil
	}
	return []string{subj.Operation}
}

// dedupStrings returns in with empty strings dropped and duplicates
// removed, preserving first-occurrence order.
func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// warnOnSecrets scans text for secret-shaped content (PLAN §20, §28: "runs
// and memories are scanned for secret values on write"). domain.Memory has
// no field to carry a warning back to the caller, so this logs at warn
// level and otherwise lets the write proceed -- ScanSecrets is deliberately
// warn-only, never blocking.
func (m *memoryAPI) warnOnSecrets(text string) {
	if kinds := memory.ScanSecrets(text); len(kinds) > 0 {
		m.l.logger.Warn("memory text looks like it may contain a secret", "kinds", kinds)
	}
}

// PromotionTarget locates where a memory's knowledge belongs canonically
// (PLAN §26).
func (m *memoryAPI) PromotionTarget(ctx context.Context, id string) (*engine.PromotionTarget, error) {
	l := m.l
	mem, err := l.memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	subj := mem.Subject
	target := &engine.PromotionTarget{Memory: *mem}

	switch {
	case subj.Field != "" && subj.Operation != "":
		return openAPIFieldTarget(ctx, l, target, subj.Operation, subj.Field)
	case subj.Operation != "":
		return openAPIOperationTarget(ctx, l, target, subj.Operation)
	default:
		return docTarget(ctx, l, target, subj, mem.Type)
	}
}

// openAPIFieldTarget builds a PromotionTarget pointing at operationID's
// field fieldPath in its OpenAPI contract file.
func openAPIFieldTarget(ctx context.Context, l *Local, target *engine.PromotionTarget, operationID, fieldPath string) (*engine.PromotionTarget, error) {
	op, err := l.cat.GetOperation(ctx, operationID)
	if err != nil {
		return nil, err
	}
	svc, err := l.cat.GetService(ctx, op.ServiceID)
	if err != nil {
		return nil, err
	}
	target.Kind = "openapi"
	target.File = filepath.Join(svc.PackageDir, op.Source.File)
	target.Line = op.Source.Line
	target.Pointer = fieldPointerSuffix(op.Source.Pointer, fieldPath)
	if fields, err := l.cat.Fields(ctx, operationID); err == nil {
		for _, f := range fields {
			if f.Path == fieldPath {
				target.Current = f.Description
				break
			}
		}
	}
	target.Suggested = descriptionBlock(target.Memory.Text)
	return target, nil
}

// openAPIOperationTarget builds a PromotionTarget pointing at operationID
// itself in its OpenAPI contract file.
func openAPIOperationTarget(ctx context.Context, l *Local, target *engine.PromotionTarget, operationID string) (*engine.PromotionTarget, error) {
	op, err := l.cat.GetOperation(ctx, operationID)
	if err != nil {
		return nil, err
	}
	svc, err := l.cat.GetService(ctx, op.ServiceID)
	if err != nil {
		return nil, err
	}
	target.Kind = "openapi"
	target.File = filepath.Join(svc.PackageDir, op.Source.File)
	target.Line = op.Source.Line
	target.Pointer = op.Source.Pointer
	target.Current = op.Description
	target.Suggested = descriptionBlock(target.Memory.Text)
	return target, nil
}

// docTarget builds a PromotionTarget pointing at the best-matching doc
// section for subj's service (directly named, or derived from its schema
// or flow), or -- if none references it yet -- a proposed new doc file.
func docTarget(ctx context.Context, l *Local, target *engine.PromotionTarget, subj domain.Subject, memType domain.MemoryType) (*engine.PromotionTarget, error) {
	service := resolveSubjectService(ctx, l, subj)
	if service == "" {
		return nil, errs.New(errs.Invalid, "memory has no service-, operation-, or schema-resolvable subject to promote")
	}
	svc, err := l.cat.GetService(ctx, service)
	if err != nil {
		return nil, err
	}

	target.Kind = "doc"
	target.Suggested = target.Memory.Text

	var sectionID string
	if r, err := l.cat.DocsReferencing(ctx, domain.RefService, service, 1); err == nil && len(r) > 0 {
		sectionID = r[0].Section.ID
	}
	if sectionID != "" {
		if sec, doc, err := l.cat.GetDocSection(ctx, sectionID); err == nil && sec != nil && doc != nil {
			target.File = filepath.Join(svc.PackageDir, doc.Path)
			target.Section = sec.Heading
			target.Current = sec.Body
			return target, nil
		}
	}

	target.File = filepath.Join(svc.PackageDir, "docs", slugFirstWords(target.Memory.Text, 5)+".md")
	target.Section = memoryTypeHeading(memType)
	return target, nil
}

// resolveSubjectService derives a service name from subj: its own Service
// field, else the service prefix of its Schema ("<service>.Component"),
// else the owning service of its Flow (when the flow is service-owned).
func resolveSubjectService(ctx context.Context, l *Local, subj domain.Subject) string {
	if subj.Service != "" {
		return subj.Service
	}
	if subj.Schema != "" {
		if i := strings.Index(subj.Schema, "."); i > 0 {
			return subj.Schema[:i]
		}
	}
	if subj.Flow != "" {
		if fs, err := l.cat.GetFlowSummary(ctx, subj.Flow); err == nil && fs != nil && fs.OwnerKind == "service" {
			return fs.OwnerID
		}
	}
	return ""
}

// fieldPointerSuffix best-effort extends base (an operation's JSON pointer)
// with a "/properties/<name>" suffix per path segment for a
// "request.body.…" or "response.<status>.body.…" field path. It returns
// base unchanged for any field path shape it isn't confident about (PLAN
// §26: "leave the operation pointer if unsure").
func fieldPointerSuffix(base, fieldPath string) string {
	segs := strings.Split(fieldPath, ".")
	var propSegs []string
	switch {
	case len(segs) >= 4 && segs[0] == "response" && segs[2] == "body":
		propSegs = segs[3:]
	case len(segs) >= 3 && segs[0] == "request" && segs[1] == "body":
		propSegs = segs[2:]
	default:
		return base
	}
	if len(propSegs) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	for _, seg := range propSegs {
		seg = strings.TrimSuffix(seg, "[]")
		if seg == "" {
			continue
		}
		b.WriteString("/properties/")
		b.WriteString(seg)
	}
	return b.String()
}

// descriptionBlock renders text as a YAML `description:` block scalar, for
// pasting into an OpenAPI operation/field.
func descriptionBlock(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "description: \"\"\n"
	}
	var b strings.Builder
	b.WriteString("description: >\n")
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// memoryTypeHeading renders a memory type as a doc section heading, e.g.
// domain.MemoryBehavioral -> "Behavioral".
func memoryTypeHeading(t domain.MemoryType) string {
	s := string(t)
	if s == "" {
		return "Notes"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// slugFirstWords builds a filesystem-safe slug from text's first n words:
// lower-cased, non-alphanumeric runs collapsed to a single "-".
func slugFirstWords(text string, n int) string {
	words := strings.Fields(text)
	if len(words) > n {
		words = words[:n]
	}
	joined := strings.ToLower(strings.Join(words, " "))

	var b strings.Builder
	prevDash := true // avoid a leading '-'
	for _, r := range joined {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "memory"
	}
	return slug
}

// Move and Commit: Phase 0 stubs, filled in by the tier work.
func (m *memoryAPI) Move(ctx context.Context, id, tier string) (*domain.Memory, error) {
	return nil, errs.New(errs.NotImplemented, "moving a memory between tiers is not available yet")
}

func (m *memoryAPI) Commit(ctx context.Context, id, message string) (*domain.Memory, error) {
	return nil, errs.New(errs.NotImplemented, "committing a memory is not available yet")
}
