package local

import (
	"context"
	"fmt"
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
	mem, err := m.l.memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	list := []domain.Memory{*mem}
	m.l.fillMemoryShipped(ctx, list)
	out := list[0]
	return &out, nil
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

// List returns memories matching q. Workspace-tier results carry Shipped
// (PLAN §7b), from one read-only look at the workspace's git repository;
// other tiers, and personal (no file), never do. See fillMemoryShipped.
func (m *memoryAPI) List(ctx context.Context, q domain.MemoryQuery) ([]domain.Memory, error) {
	out, err := m.l.memStore.List(ctx, q)
	if err != nil {
		return nil, err
	}
	m.l.fillMemoryShipped(ctx, out)
	return out, nil
}

// Search is List's counterpart for retrieval: the same Shipped fill,
// applied to the memory embedded in each result.
func (m *memoryAPI) Search(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
	out, err := m.l.memStore.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	mems := make([]domain.Memory, len(out))
	for i := range out {
		mems[i] = out[i].Memory
	}
	m.l.fillMemoryShipped(ctx, mems)
	for i := range out {
		out[i].Memory = mems[i]
	}
	return out, nil
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

// fillMemoryShipped fills Shipped on every workspace-tier memory in mems,
// in place, from one read-only look at the workspace's git repository
// (PLAN §7b) -- mirrors flowAPI's fillShipped for flows. Local-, service-,
// and (for a flow-scoped memory) any other tier are left alone, and so is
// personal scope, which has no file at all. Any git failure -- the
// workspace is not a git repository, git itself is unavailable, a
// transient error -- leaves every Shipped empty and is logged at Debug: a
// memory listing must never fail because git did. Only runs FileStates at
// all when at least one result is workspace tier, so a plain personal- or
// service-scope lookup never pays for a git call.
func (l *Local) fillMemoryShipped(ctx context.Context, mems []domain.Memory) {
	var idx []int
	var paths []string
	for i := range mems {
		if mems[i].Tier != domain.TierWorkspace {
			continue
		}
		idx = append(idx, i)
		paths = append(paths, mems[i].FilePath)
	}
	if len(idx) == 0 {
		return
	}
	if _, ok := l.gitMgr.RepoRoot(ctx, l.ws.Dir); !ok {
		return
	}
	states, err := l.gitMgr.FileStates(ctx, l.ws.Dir, paths)
	if err != nil {
		l.logger.Debug("computing memory ship states failed; leaving them empty", "error", err)
		return
	}
	for _, i := range idx {
		mems[i].Shipped = states[mems[i].FilePath]
	}
}

// Move places a workspace-scope memory's file in another tier (PLAN §7b):
// workspace scope only (personal has no file; service and flow scope each
// have exactly one tier already, so "move" doesn't apply to them -- a
// flow-scoped memory follows its flow instead). tier must be
// domain.TierLocal or domain.TierWorkspace.
//
// It delegates the actual move to Update (setting Tier on a copy of the
// existing memory): that is the one place a memory's file is written
// (writeFileForScope), so Move gets the same rewrite-in-the-new-place,
// remove-the-old-one behavior, the same memory.changed event and semantic
// reindex, for free, exactly as the tier work asks ("emit memory.changed
// ... as Update does"). Moving to the tier a memory is already in is a
// no-op that still returns the current item (with Shipped).
func (m *memoryAPI) Move(ctx context.Context, id, tier string) (*domain.Memory, error) {
	existing, err := m.l.memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.Scope != domain.ScopeWorkspace {
		return nil, errs.New(errs.Invalid, "only a workspace-scope memory can move between tiers; %q is %s scope", id, existing.Scope).
			WithHint("a personal memory has no file, a service memory belongs to its service, and a flow-scoped memory moves with its flow")
	}
	if tier != domain.TierLocal && tier != domain.TierWorkspace {
		return nil, errs.New(errs.Invalid, "unknown tier %q for Move; want %s or %s", tier, domain.TierLocal, domain.TierWorkspace)
	}
	if existing.Tier == tier {
		return m.Get(ctx, id)
	}

	toMove := *existing
	toMove.Tier = tier
	if _, err := m.Update(ctx, toMove); err != nil {
		return nil, err
	}
	return m.Get(ctx, id)
}

// MoveFolder places memory id's file at newFolder within its current
// directory, keeping scope and tier (PLAN §34f item 6): delegated straight
// to the store's own MoveFolder, which does the actual validation, file
// move, and reindex; this wrapper's only job is to fill Shipped on the
// result and emit memory.changed, the same finishing touches Move (tier)
// gets for free by riding through Update.
func (m *memoryAPI) MoveFolder(ctx context.Context, id, newFolder string) (*domain.Memory, error) {
	moved, err := m.l.memStore.MoveFolder(ctx, id, newFolder)
	if err != nil {
		return nil, err
	}
	list := []domain.Memory{*moved}
	m.l.fillMemoryShipped(ctx, list)
	out := list[0]
	m.l.emit(domain.EventMemoryChanged, out)
	return &out, nil
}

// Commit records a workspace-tier memory's file in the workspace
// repository with one commit of that file (PLAN §7b), mirroring
// flowAPI.Commit (see its doc comment for the full rationale) and reusing
// its shipStateNothingToCommit/default-message logic. Applies to any
// memory whose file happens to sit in the workspace tier, regardless of
// Scope -- a flow-scoped memory whose flow was promoted is exactly as
// committable standalone as the flow itself is. Refused with errs.Invalid
// for every other tier (including personal, which has no file at all), a
// workspace not inside a git repository, and a file with nothing to commit
// (FileStates already says unpushed or shipped -- Sapien never pushes, so
// there is nothing left for a commit to do).
func (m *memoryAPI) Commit(ctx context.Context, id, message string) (*domain.Memory, error) {
	l := m.l
	existing, err := l.memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.Tier != domain.TierWorkspace {
		tier := existing.Tier
		if tier == "" {
			tier = "personal (no file)"
		}
		return nil, errs.New(errs.Invalid, "only a workspace-tier memory can be committed; %q is %s", id, tier).
			WithHint("move it to the workspace tier first (`sapien memory move " + id + " workspace`), or promote its flow if it is flow-scoped")
	}
	if _, ok := l.gitMgr.RepoRoot(ctx, l.ws.Dir); !ok {
		return nil, errs.New(errs.Invalid, "workspace %s is not in a git repository", l.ws.Dir)
	}

	states, err := l.gitMgr.FileStates(ctx, l.ws.Dir, []string{existing.FilePath})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "checking the git state of %s", existing.FilePath)
	}
	state := states[existing.FilePath]
	if sentence := shipStateNothingToCommit(state); sentence != "" {
		return nil, errs.New(errs.Invalid, "memory %q has nothing to commit: %s", id, sentence)
	}

	if message == "" {
		if state == domain.ShipUntracked {
			message = fmt.Sprintf("Add memory %s to the team workspace", id)
		} else {
			message = fmt.Sprintf("Update memory %s", id)
		}
	}
	if _, err := l.gitMgr.CommitPaths(ctx, l.ws.Dir, []string{existing.FilePath}, message); err != nil {
		return nil, err
	}

	updated, err := l.memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	updated.Shipped = domain.ShipUnpushed
	l.emit(domain.EventMemoryChanged, *updated)
	return updated, nil
}
