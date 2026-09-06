package enginetest

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// subjectMatch scores how many non-empty fields of a match the same
// non-empty fields of b, returning the count and human-readable reasons.
func subjectMatch(a, b domain.Subject) (int, []string) {
	var reasons []string
	add := func(cond bool, reason string) {
		if cond {
			reasons = append(reasons, reason)
		}
	}
	add(a.Service != "" && a.Service == b.Service, "service match")
	add(a.Operation != "" && a.Operation == b.Operation, "operation match")
	add(a.Field != "" && a.Field == b.Field, "field match")
	add(a.Schema != "" && a.Schema == b.Schema, "schema match")
	add(a.Flow != "" && a.Flow == b.Flow, "flow match")
	add(a.Step != "" && a.Step == b.Step, "step match")
	add(a.Run != "" && a.Run == b.Run, "run match")
	add(a.Environment != "" && a.Environment == b.Environment, "environment match")
	add(a.Concept != "" && a.Concept == b.Concept, "concept match")
	add(a.Error != nil && b.Error != nil && *a.Error == *b.Error, "error match")
	return len(reasons), reasons
}

// memoryMatchesQuery applies the non-text filters shared by List and Search.
func memoryMatchesQuery(mem domain.Memory, q domain.MemoryQuery) bool {
	if q.Scope != "" && mem.Scope != q.Scope {
		return false
	}
	if q.Type != "" && mem.Type != q.Type {
		return false
	}
	if q.Service != "" && mem.Subject.Service != q.Service {
		return false
	}
	if q.Operation != "" && mem.Subject.Operation != q.Operation {
		return false
	}
	if q.Flow != "" && mem.Subject.Flow != q.Flow {
		return false
	}
	return true
}

func (m *memoryAPI) Create(ctx context.Context, mem domain.Memory) (*domain.Memory, error) {
	f := m.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Memories.Create", mem)

	if mem.ID == "" {
		mem.ID = "mem_" + ulid.Make().String()
	}
	now := time.Now().UTC()
	mem.Created = now
	mem.Updated = now
	mem.Hash = hashOf(mem.Text)
	f.memories[mem.ID] = mem
	f.events.publish(domain.Event{Type: domain.EventMemoryCreated, Time: now, Payload: mem.ID})

	cp := mem
	return &cp, nil
}

func (m *memoryAPI) Get(ctx context.Context, id string) (*domain.Memory, error) {
	f := m.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Memories.Get", id)

	mem, ok := f.memories[id]
	if !ok {
		return nil, errs.New(errs.MemoryNotFound, "memory %q not found", id).WithDetail("id", id)
	}
	cp := mem
	return &cp, nil
}

func (m *memoryAPI) Update(ctx context.Context, mem domain.Memory) (*domain.Memory, error) {
	f := m.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Memories.Update", mem)

	existing, ok := f.memories[mem.ID]
	if !ok {
		return nil, errs.New(errs.MemoryNotFound, "memory %q not found", mem.ID).WithDetail("id", mem.ID)
	}
	mem.Created = existing.Created
	mem.Updated = time.Now().UTC()
	mem.Hash = hashOf(mem.Text)
	f.memories[mem.ID] = mem
	f.events.publish(domain.Event{Type: domain.EventMemoryChanged, Time: mem.Updated, Payload: mem.ID})

	cp := mem
	return &cp, nil
}

func (m *memoryAPI) Delete(ctx context.Context, id string) error {
	f := m.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Memories.Delete", id)

	if _, ok := f.memories[id]; !ok {
		return errs.New(errs.MemoryNotFound, "memory %q not found", id).WithDetail("id", id)
	}
	delete(f.memories, id)
	f.events.publish(domain.Event{Type: domain.EventMemoryChanged, Time: time.Now().UTC(), Payload: id})
	return nil
}

func (m *memoryAPI) List(ctx context.Context, q domain.MemoryQuery) ([]domain.Memory, error) {
	f := m.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Memories.List", q)

	text := strings.ToLower(strings.TrimSpace(q.Text))
	var out []domain.Memory
	for _, mem := range f.memories {
		if !memoryMatchesQuery(mem, q) {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(mem.Text), text) {
			continue
		}
		out = append(out, mem)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (m *memoryAPI) Search(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
	f := m.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Memories.Search", q)

	text := strings.ToLower(strings.TrimSpace(q.Text))
	var out []domain.ScoredMemory
	for _, mem := range f.memories {
		if !memoryMatchesQuery(mem, q) {
			continue
		}

		score := 1.0
		var reasons []string
		if text != "" {
			if !strings.Contains(strings.ToLower(mem.Text), text) {
				continue
			}
			reasons = append(reasons, "lexical")
			score = 2
		}
		if len(q.Subjects) > 0 {
			best := 0
			var bestReasons []string
			for _, subj := range q.Subjects {
				if s, r := subjectMatch(mem.Subject, subj); s > best {
					best, bestReasons = s, r
				}
			}
			if best == 0 && text == "" {
				continue
			}
			score += float64(best)
			reasons = append(reasons, bestReasons...)
		}
		if q.MinScore > 0 && score < q.MinScore {
			continue
		}
		out = append(out, domain.ScoredMemory{Memory: mem, Score: score, Reasons: reasons})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Memory.ID < out[j].Memory.ID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (m *memoryAPI) Relevant(ctx context.Context, subjects []domain.Subject, limit int) ([]domain.ScoredMemory, error) {
	f := m.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Memories.Relevant", map[string]any{"subjects": subjects, "limit": limit})

	var out []domain.ScoredMemory
	for _, mem := range f.memories {
		best := 0
		var bestReasons []string
		for _, subj := range subjects {
			if s, r := subjectMatch(mem.Subject, subj); s > best {
				best, bestReasons = s, r
			}
		}
		if best == 0 {
			continue
		}
		out = append(out, domain.ScoredMemory{Memory: mem, Score: float64(best), Reasons: bestReasons})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Memory.ID < out[j].Memory.ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memoryAPI) PromotionTarget(ctx context.Context, id string) (*engine.PromotionTarget, error) {
	f := m.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Memories.PromotionTarget", id)

	mem, ok := f.memories[id]
	if !ok {
		return nil, errs.New(errs.MemoryNotFound, "memory %q not found", id).WithDetail("id", id)
	}
	file := "api/openapi.yaml"
	if mem.Subject.Service != "" {
		file = mem.Subject.Service + "/api/openapi.yaml"
	}
	return &engine.PromotionTarget{
		Kind:      "openapi",
		File:      file,
		Pointer:   "/paths",
		Memory:    mem,
		Suggested: mem.Text,
	}, nil
}

func (m *memoryAPI) Reindex(ctx context.Context) error {
	f := m.f()
	f.mu.Lock()
	f.reindexedMemories = true
	f.recordLocked("Memories.Reindex", nil)
	f.mu.Unlock()

	f.Publish(domain.Event{Type: domain.EventCatalogChanged, Time: time.Now().UTC()})
	return nil
}

var _ engine.MemoryAPI = (*memoryAPI)(nil)
