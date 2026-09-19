package remote

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// memoryQuery encodes the fields of a domain.MemoryQuery that the wire
// format supports. Subjects has no query-string encoding (see the package
// doc) and is always dropped; use Relevant for subject-based retrieval.
func memoryQuery(q domain.MemoryQuery) url.Values {
	v := url.Values{}
	if q.Text != "" {
		v.Set("q", q.Text)
	}
	if q.Scope != "" {
		v.Set("scope", string(q.Scope))
	}
	if q.Type != "" {
		v.Set("type", string(q.Type))
	}
	if q.Service != "" {
		v.Set("service", q.Service)
	}
	if q.Operation != "" {
		v.Set("op", q.Operation)
	}
	if q.Flow != "" {
		v.Set("flow", q.Flow)
	}
	if q.Folder != "" {
		v.Set("folder", q.Folder)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.MinScore > 0 {
		v.Set("min_score", strconv.FormatFloat(q.MinScore, 'f', -1, 64))
	}
	return v
}

// List maps to GET /v1/memories.
func (m *memoryAPI) List(ctx context.Context, q domain.MemoryQuery) ([]domain.Memory, error) {
	var out []domain.Memory
	if err := m.r().do(ctx, http.MethodGet, "/v1/memories", memoryQuery(q), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Create maps to POST /v1/memories.
func (m *memoryAPI) Create(ctx context.Context, mem domain.Memory) (*domain.Memory, error) {
	var out domain.Memory
	if err := m.r().do(ctx, http.MethodPost, "/v1/memories", nil, mem, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Search maps to GET /v1/memories/search.
func (m *memoryAPI) Search(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
	var out []domain.ScoredMemory
	if err := m.r().do(ctx, http.MethodGet, "/v1/memories/search", memoryQuery(q), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type relevantMemoriesRequest struct {
	Subjects []domain.Subject `json:"subjects"`
	Limit    int              `json:"limit,omitempty"`
}

// Relevant maps to POST /v1/memories/relevant.
func (m *memoryAPI) Relevant(ctx context.Context, subjects []domain.Subject, limit int) ([]domain.ScoredMemory, error) {
	var out []domain.ScoredMemory
	body := relevantMemoriesRequest{Subjects: subjects, Limit: limit}
	if err := m.r().do(ctx, http.MethodPost, "/v1/memories/relevant", nil, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Get maps to GET /v1/memories/{id}.
func (m *memoryAPI) Get(ctx context.Context, id string) (*domain.Memory, error) {
	var out domain.Memory
	if err := m.r().do(ctx, http.MethodGet, "/v1/memories/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update maps to PATCH /v1/memories/{id}, using mem.ID as the path id.
func (m *memoryAPI) Update(ctx context.Context, mem domain.Memory) (*domain.Memory, error) {
	var out domain.Memory
	if err := m.r().do(ctx, http.MethodPatch, "/v1/memories/"+mem.ID, nil, mem, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete maps to DELETE /v1/memories/{id}.
func (m *memoryAPI) Delete(ctx context.Context, id string) error {
	return m.r().do(ctx, http.MethodDelete, "/v1/memories/"+id, nil, nil, nil)
}

// PromotionTarget maps to GET /v1/memories/{id}/promotion.
func (m *memoryAPI) PromotionTarget(ctx context.Context, id string) (*engine.PromotionTarget, error) {
	var out engine.PromotionTarget
	if err := m.r().do(ctx, http.MethodGet, "/v1/memories/"+id+"/promotion", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Reindex maps to POST /v1/memories/reindex.
func (m *memoryAPI) Reindex(ctx context.Context) error {
	return m.r().do(ctx, http.MethodPost, "/v1/memories/reindex", nil, nil, nil)
}

var _ engine.MemoryAPI = (*memoryAPI)(nil)

// moveTierRequest is POST /v1/memories/{id}/move and POST
// /v1/examples/{id}/move's shared body: Tier (PLAN §7b) moves the file to
// another tier, Folder (PLAN §34f item 6) moves it to another folder within
// its current directory; each request sends exactly one of the two. Folder
// is a pointer so "move to the root folder" (an explicit "") can be told
// apart from "no folder change requested" (the key absent) on the wire. The
// server-side twin is internal/server/handlers_memories.go's own
// moveTierRequest.
type moveTierRequest struct {
	Tier   string  `json:"tier,omitempty"`
	Folder *string `json:"folder,omitempty"`
}

type commitMessageRequest struct {
	Message string `json:"message,omitempty"`
}

// Move maps to POST /v1/memories/{id}/move with tier.
func (m *memoryAPI) Move(ctx context.Context, id, tier string) (*domain.Memory, error) {
	var out domain.Memory
	if err := m.r().do(ctx, http.MethodPost, "/v1/memories/"+id+"/move", nil, moveTierRequest{Tier: tier}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MoveFolder maps to POST /v1/memories/{id}/move with folder.
func (m *memoryAPI) MoveFolder(ctx context.Context, id, newFolder string) (*domain.Memory, error) {
	var out domain.Memory
	if err := m.r().do(ctx, http.MethodPost, "/v1/memories/"+id+"/move", nil, moveTierRequest{Folder: &newFolder}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Commit maps to POST /v1/memories/{id}/commit.
func (m *memoryAPI) Commit(ctx context.Context, id, message string) (*domain.Memory, error) {
	var out domain.Memory
	if err := m.r().do(ctx, http.MethodPost, "/v1/memories/"+id+"/commit", nil, commitMessageRequest{Message: message}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
