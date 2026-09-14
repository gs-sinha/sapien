package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// --- search_memories -----------------------------------------------------

// SearchMemoriesInput is search_memories' arguments.
type SearchMemoriesInput struct {
	Query     string `json:"query" jsonschema:"full-text query over memory text, tags, and subject"`
	Operation string `json:"operation,omitempty" jsonschema:"restrict to memories about this operation"`
	Service   string `json:"service,omitempty" jsonschema:"restrict to memories about this service"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum number of results; default 10"`
}

// SearchMemoriesOutput is search_memories' structured output.
type SearchMemoriesOutput struct {
	Memories []domain.ScoredMemory `json:"memories"`
}

func (s *server) searchMemories(ctx context.Context, req *sdkmcp.CallToolRequest, in SearchMemoriesInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadMemories); denied != nil {
		return denied, nil, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	results, err := s.engine().Memories().Search(ctx, domain.MemoryQuery{
		Text: in.Query, Operation: in.Operation, Service: in.Service, Limit: limit,
	})
	if err != nil {
		return errResult(err), nil, nil
	}
	out := SearchMemoriesOutput{Memories: results}
	return result(renderMemories(results), out), nil, nil
}

func renderMemories(results []domain.ScoredMemory) string {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "- [%s/%s] %s (score %.2f): %s\n", r.Memory.Type, r.Memory.Scope, r.Memory.ID, r.Score, r.Memory.Text)
	}
	if len(results) == 0 {
		b.WriteString("no matches\n")
	}
	return b.String()
}

// --- get_relevant_memories -----------------------------------------------------

// GetRelevantMemoriesInput is get_relevant_memories' arguments.
type GetRelevantMemoriesInput struct {
	Subjects []domain.Subject `json:"subjects" jsonschema:"subjects to retrieve memories for"`
	Limit    int              `json:"limit,omitempty" jsonschema:"maximum number of results; default 15"`
}

// GetRelevantMemoriesOutput is get_relevant_memories' structured output.
type GetRelevantMemoriesOutput struct {
	Memories []domain.ScoredMemory `json:"memories"`
}

func (s *server) getRelevantMemories(ctx context.Context, req *sdkmcp.CallToolRequest, in GetRelevantMemoriesInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadMemories); denied != nil {
		return denied, nil, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 15
	}
	results, err := s.engine().Memories().Relevant(ctx, in.Subjects, limit)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := GetRelevantMemoriesOutput{Memories: results}
	return result(renderMemories(results), out), nil, nil
}

// --- create_memory -----------------------------------------------------

// CreateMemoryInput is create_memory's arguments.
type CreateMemoryInput struct {
	Text    string          `json:"text" jsonschema:"the memory's Markdown body"`
	Subject *domain.Subject `json:"subject,omitempty" jsonschema:"what the memory is about"`
	Type    string          `json:"type,omitempty" jsonschema:"note|semantic|behavioral|testing|invariant|environment|gotcha"`
	Scope   string          `json:"scope,omitempty" jsonschema:"personal|workspace|service|flow; default workspace. Scope decides storage and sharing, not subject: service is committed in the service repo and shared; workspace is local to this workspace. If it would still be true in a fresh environment with empty databases, use service."`
	Tags    []string        `json:"tags,omitempty" jsonschema:"free-form tags"`
}

// CreateMemoryOutput is create_memory's structured output.
type CreateMemoryOutput struct {
	Memory domain.Memory `json:"memory"`
}

// createMemory records source: {kind: agent, client: <name>} per PLAN §23/§24.
func (s *server) createMemory(ctx context.Context, req *sdkmcp.CallToolRequest, in CreateMemoryInput) (*sdkmcp.CallToolResult, any, error) {
	client, _, denied := s.checkPermission(req.Session, classWriteMemories)
	if denied != nil {
		return denied, nil, nil
	}
	scope := domain.MemoryScope(in.Scope)
	if scope == "" {
		scope = domain.ScopeWorkspace
	}
	m := domain.Memory{
		Type:   domain.MemoryType(in.Type),
		Scope:  scope,
		Tags:   in.Tags,
		Text:   in.Text,
		Source: domain.MemorySource{Kind: "agent", Client: client},
	}
	if in.Subject != nil {
		m.Subject = *in.Subject
	}
	created, err := s.engine().Memories().Create(ctx, m)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := CreateMemoryOutput{Memory: *created}
	var b strings.Builder
	fmt.Fprintf(&b, "created memory %s (%s, %s, source agent{%s}) in workspace %s\n%s\n", created.ID, created.Type, created.Scope, client, s.workspaceName(), created.Text)
	s.writeMemoryHints(ctx, &b, created)
	return result(b.String(), out), nil, nil
}

// writeMemoryHints appends the same situational nudges the CLI's
// `sapien memory add` prints to b: where the memory actually landed, a
// warning when workspace scope has no git history to carry it, a rescope
// suggestion when a workspace-scoped memory names a service, up to three
// similar existing memories, and a standing reminder that
// get_promotion_target is how a memory graduates out of the staging area
// (the feedback this addresses: scope decides storage and sharing, not
// subject, and nobody was discovering either fact on their own).
func (s *server) writeMemoryHints(ctx context.Context, b *strings.Builder, created *domain.Memory) {
	if created.Scope == domain.ScopePersonal {
		fmt.Fprintf(b, "stored: SQLite only\n")
	} else if created.FilePath != "" {
		fmt.Fprintf(b, "stored: %s\n", created.FilePath)
	}

	if created.Scope == domain.ScopeWorkspace {
		if dir := s.workspaceDir(); !isGitRepo(dir) {
			fmt.Fprintf(b, "note: %s is not a git repo, so workspace memories live only on this machine\n", dir)
		}
		if created.Subject.Service != "" {
			fmt.Fprintf(b, "concerns %s; if it would hold in a fresh environment, call rescope_memory(id=%q, scope=\"service\")\n",
				created.Subject.Service, created.ID)
		}
	}

	q := domain.MemoryQuery{Text: created.Text, Limit: 3}
	if created.Subject.Service != "" {
		q.Service = created.Subject.Service
	}
	if similar, err := s.engine().Memories().Search(ctx, q); err == nil {
		shown := 0
		for _, r := range similar {
			if r.Memory.ID == created.ID {
				continue
			}
			fmt.Fprintf(b, "similar: %s (%.2f) %s\n", r.Memory.ID, r.Score, firstLineOf(r.Memory.Text))
			fmt.Fprintf(b, "consider updating that one instead, or promoting it via get_promotion_target(memory_id=%q)\n", r.Memory.ID)
			shown++
			if shown == 3 {
				break
			}
		}
	}

	fmt.Fprintf(b, "when this stabilises, get_promotion_target(memory_id=%q) locates where it belongs\n", created.ID)
}

// isGitRepo reports whether dir looks like a git working tree: it has a
// .git entry, directory (a normal clone) or file (a worktree/submodule).
func isGitRepo(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// firstLineOf returns s's first line, trimmed and cut to 60 bytes, for a
// compact "similar:" preview.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// memoryPathOrSQLite renders a memory's FilePath for tool output, per PLAN
// §12: personal-scope memories have no file at all.
func memoryPathOrSQLite(path string) string {
	if path == "" {
		return "SQLite only"
	}
	return path
}

// --- get_promotion_target -----------------------------------------------------

// GetPromotionTargetInput is get_promotion_target's arguments.
type GetPromotionTargetInput struct {
	MemoryID string `json:"memory_id" jsonschema:"memory id"`
}

func (s *server) getPromotionTarget(ctx context.Context, req *sdkmcp.CallToolRequest, in GetPromotionTargetInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classReadContracts); denied != nil {
		return denied, nil, nil
	}
	if _, _, denied := s.checkPermission(req.Session, classReadMemories); denied != nil {
		return denied, nil, nil
	}
	target, err := s.engine().Memories().PromotionTarget(ctx, in.MemoryID)
	if err != nil {
		return errResult(err), nil, nil
	}
	text := fmt.Sprintf("promote memory %s to %s %s", in.MemoryID, target.Kind, target.File)
	if target.Line > 0 {
		text += fmt.Sprintf(":%d", target.Line)
	}
	if target.Pointer != "" {
		text += " " + target.Pointer
	}
	text += "\n"
	if target.Current != "" {
		text += "current: " + target.Current + "\n"
	}
	return result(text, target), nil, nil
}

// --- rescope_memory ---------------------------------------------------

// RescopeMemoryInput is rescope_memory's arguments.
type RescopeMemoryInput struct {
	ID      string `json:"id" jsonschema:"memory id"`
	Scope   string `json:"scope" jsonschema:"personal|workspace|service|flow"`
	Service string `json:"service,omitempty" jsonschema:"service name; required for service scope when the memory has no service subject"`
}

// RescopeMemoryOutput is rescope_memory's structured output.
type RescopeMemoryOutput struct {
	Memory domain.Memory `json:"memory"`
}

func (s *server) rescopeMemory(ctx context.Context, req *sdkmcp.CallToolRequest, in RescopeMemoryInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteMemories); denied != nil {
		return denied, nil, nil
	}

	mem, err := s.engine().Memories().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}

	updated := *mem
	oldPath := mem.FilePath
	updated.Scope = domain.MemoryScope(in.Scope)
	if in.Service != "" {
		updated.Subject.Service = in.Service
	}
	if updated.Scope == domain.ScopeService && updated.Subject.Service == "" {
		err := errs.New(errs.Invalid, "rescope_memory: service scope requires a service subject").
			WithHint("pass service, or give the memory a service subject first")
		return errResult(err), nil, nil
	}

	moved, err := s.engine().Memories().Update(ctx, updated)
	if err != nil {
		return errResult(err), nil, nil
	}

	out := RescopeMemoryOutput{Memory: *moved}
	text := fmt.Sprintf("rescoped memory %s to %s scope (%s -> %s)\n",
		moved.ID, moved.Scope, memoryPathOrSQLite(oldPath), memoryPathOrSQLite(moved.FilePath))
	return result(text, out), nil, nil
}

// --- delete_memory -----------------------------------------------------

// DeleteMemoryInput is delete_memory's arguments.
type DeleteMemoryInput struct {
	ID string `json:"id" jsonschema:"memory id"`
}

// DeleteMemoryOutput is delete_memory's structured output.
type DeleteMemoryOutput struct {
	ID string `json:"id"`
}

// delete_memory is `sapien memory rm` for agents. It was missing: the
// first friction report filed against Sapien had to park a memory that
// landed in the wrong workspace at personal scope because nothing over
// MCP could delete it.
func (s *server) deleteMemory(ctx context.Context, req *sdkmcp.CallToolRequest, in DeleteMemoryInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteMemories); denied != nil {
		return denied, nil, nil
	}
	if err := s.engine().Memories().Delete(ctx, in.ID); err != nil {
		return errResult(err), nil, nil
	}
	return result(fmt.Sprintf("deleted memory %s from workspace %s\n", in.ID, s.workspaceName()), DeleteMemoryOutput{ID: in.ID}), nil, nil
}
