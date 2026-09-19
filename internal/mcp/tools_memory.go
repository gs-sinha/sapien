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
	Query     string `json:"query" jsonschema:"full-text query over memory text, tags, subject, and folder"`
	Operation string `json:"operation,omitempty" jsonschema:"restrict to memories about this operation"`
	Service   string `json:"service,omitempty" jsonschema:"restrict to memories about this service"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum number of results; default 10"`
	// Folder restricts to that folder and everything below it (PLAN §34f
	// item 4/7): "" (the default) applies no folder filter.
	Folder string `json:"folder,omitempty" jsonschema:"restrict to this folder and everything below it"`
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
		Text: in.Query, Operation: in.Operation, Service: in.Service, Limit: limit, Folder: in.Folder,
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
		folderNote := ""
		if r.Memory.Folder != "" {
			folderNote = " folder:" + r.Memory.Folder
		}
		fmt.Fprintf(&b, "- [%s/%s]%s %s (score %.2f): %s%s\n",
			r.Memory.Type, r.Memory.Scope, folderNote, r.Memory.ID, r.Score, r.Memory.Text, tierShipSuffix(r.Memory.Tier, r.Memory.Shipped))
	}
	if len(results) == 0 {
		b.WriteString("no matches\n")
	}
	return b.String()
}

// tierShipSuffix renders the workspace tier's ship state (PLAN §7b) as a
// short bracketed suffix on a memory or example's list/search text line:
// "" unless the item is at the workspace tier with a ship state to
// report -- the structured Tier/Shipped fields already ride on the
// domain type itself, so this only adds the same information where a
// human or agent reads the plain-text line.
func tierShipSuffix(tier, shipped string) string {
	if tier != domain.TierWorkspace {
		return ""
	}
	if s := shipStateText(shipped); s != "" {
		return fmt.Sprintf(" [%s]", s)
	}
	return ""
}

// tierLandedLine is create_memory/create_example's one-line note on which
// tier the new file landed in (PLAN §7b), read from the created item's own
// Tier: local is spelled out, since it is the default and the reader needs
// to know both where the file went and how to move it up; workspace and
// service tiers just name themselves. toolName is the rescope tool to
// point at in the local-tier hint ("rescope_memory" or "rescope_example").
func tierLandedLine(toolName, tier string) string {
	switch tier {
	case domain.TierLocal:
		return fmt.Sprintf("local tier: this machine only; move it to the workspace tier with %s(tier=workspace) when it should reach the team", toolName)
	case domain.TierWorkspace:
		return "workspace tier: the team's repo"
	case domain.TierService:
		return "service tier"
	default:
		return ""
	}
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
	// Folder places the memory in a subfolder of the scope's memories
	// directory (PLAN §34f item 6); "" (the default) is the root.
	// Meaningless for personal scope, which has no file.
	Folder string `json:"folder,omitempty" jsonschema:"subfolder of the scope's memories directory to save into; default the root"`
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
		Folder: in.Folder,
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
	if line := tierLandedLine("rescope_memory", created.Tier); line != "" {
		fmt.Fprintf(b, "%s\n", line)
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

// RescopeMemoryInput is rescope_memory's arguments. Scope used to be
// required; it is optional now (PLAN §34f item 6) so a call can change only
// Folder (or Tier) and leave the memory's scope exactly where it is --
// existing callers that always pass scope see no change in behavior.
type RescopeMemoryInput struct {
	ID      string `json:"id" jsonschema:"memory id"`
	Scope   string `json:"scope,omitempty" jsonschema:"personal|workspace|service|flow; omit to keep the current scope and only change tier and/or folder"`
	Service string `json:"service,omitempty" jsonschema:"service name; required for service scope when the memory has no service subject"`
	// Tier is applied after the scope change, via Memories().Move, so one
	// call can both rescope and place the file in a tier (PLAN §7b).
	// Meaningful only when the memory ends up at workspace scope.
	Tier string `json:"tier,omitempty" jsonschema:"local|workspace; optional: also move the file to this tier, after the scope change"`
	// Folder is applied last, via Memories().MoveFolder, so one call can
	// rescope, retier, and change folder together (PLAN §34f item 6). A
	// pointer so "move to the root" (an explicit "") can be told apart from
	// "leave the folder alone" (the key absent).
	Folder *string `json:"folder,omitempty" jsonschema:"also move the file to this folder within its (new or current) directory, after the scope/tier change; \"\" moves it to the root"`
}

// RescopeMemoryOutput is rescope_memory's structured output.
type RescopeMemoryOutput struct {
	Memory domain.Memory `json:"memory"`
}

func (s *server) rescopeMemory(ctx context.Context, req *sdkmcp.CallToolRequest, in RescopeMemoryInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteMemories); denied != nil {
		return denied, nil, nil
	}
	if strings.TrimSpace(in.Scope) == "" && in.Tier == "" && in.Folder == nil {
		return errResult(errs.New(errs.Invalid, "rescope_memory requires scope, tier, or folder")), nil, nil
	}

	mem, err := s.engine().Memories().Get(ctx, in.ID)
	if err != nil {
		return errResult(err), nil, nil
	}

	oldPath := mem.FilePath
	moved := mem
	text := ""
	if strings.TrimSpace(in.Scope) != "" {
		updated := *mem
		updated.Scope = domain.MemoryScope(in.Scope)
		if in.Service != "" {
			updated.Subject.Service = in.Service
		}
		if updated.Scope == domain.ScopeService && updated.Subject.Service == "" {
			err := errs.New(errs.Invalid, "rescope_memory: service scope requires a service subject").
				WithHint("pass service, or give the memory a service subject first")
			return errResult(err), nil, nil
		}
		moved, err = s.engine().Memories().Update(ctx, updated)
		if err != nil {
			return errResult(err), nil, nil
		}
		text += fmt.Sprintf("rescoped memory %s to %s scope (%s -> %s)\n",
			moved.ID, moved.Scope, memoryPathOrSQLite(oldPath), memoryPathOrSQLite(moved.FilePath))
	}

	if in.Tier != "" {
		moved, err = s.engine().Memories().Move(ctx, moved.ID, in.Tier)
		if err != nil {
			return errResult(err), nil, nil
		}
		text += fmt.Sprintf("moved to the %s tier: %s\n", in.Tier, memoryPathOrSQLite(moved.FilePath))
	}
	if in.Folder != nil {
		moved, err = s.engine().Memories().MoveFolder(ctx, moved.ID, *in.Folder)
		if err != nil {
			return errResult(err), nil, nil
		}
		text += fmt.Sprintf("moved to folder %q: %s\n", moved.Folder, memoryPathOrSQLite(moved.FilePath))
	}

	out := RescopeMemoryOutput{Memory: *moved}
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

// --- commit_memory -----------------------------------------------------
//
// commit_memory is the standalone form of what a promotion to the
// workspace tier leaves undone (PLAN §7b): moving a memory's file there
// never commits it, and rescope_memory carries no commit option of its
// own (unlike rescope_flow) -- this is the one way to record it in the
// workspace repository over MCP. It never pushes; there is no push tool
// over MCP at all, since pushing what an agent wrote is the human's call.

// CommitMemoryInput is commit_memory's arguments.
type CommitMemoryInput struct {
	ID string `json:"id" jsonschema:"memory id; must already be at the workspace tier"`
	// Message overrides the engine's own default.
	Message string `json:"message,omitempty" jsonschema:"commit message; default depends on whether the file was ever added to git"`
}

// CommitMemoryOutput is commit_memory's structured output.
type CommitMemoryOutput struct {
	Memory domain.Memory `json:"memory"`
}

// commitMemory commits a workspace-tier memory's file in the workspace
// repository: one commit of that file, never a push. Refused by the
// engine for any other tier, a workspace not in git, or a file with
// nothing to commit.
func (s *server) commitMemory(ctx context.Context, req *sdkmcp.CallToolRequest, in CommitMemoryInput) (*sdkmcp.CallToolResult, any, error) {
	if _, _, denied := s.checkPermission(req.Session, classWriteMemories); denied != nil {
		return denied, nil, nil
	}
	mem, err := s.engine().Memories().Commit(ctx, in.ID, in.Message)
	if err != nil {
		return errResult(err), nil, nil
	}
	out := CommitMemoryOutput{Memory: *mem}
	text := fmt.Sprintf("committed %s; not pushed\n", memoryPathOrSQLite(mem.FilePath))
	return result(text, out), nil, nil
}
