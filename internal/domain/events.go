package domain

import "time"

// EventType enumerates engine events (PLAN §4).
type EventType string

const (
	EventCatalogChanged    EventType = "catalog.changed"
	EventServiceSyncFailed EventType = "service.sync_failed"
	EventRunStarted        EventType = "run.started"
	EventRunStep           EventType = "run.step"
	EventRunFinished       EventType = "run.finished"
	EventMemoryCreated     EventType = "memory.created"
	EventMemoryChanged     EventType = "memory.changed"
	EventFlowChanged       EventType = "flow.changed"
	// EventWorkspaceRepo carries a domain.RepoStatus whenever the workspace's
	// own repository was fetched, pulled or synced (PLAN §7b), so a status
	// bar learns "team has N new commits" without polling.
	EventWorkspaceRepo EventType = "workspace.repo"
	// EventSemanticIndex carries a SemanticIndexEvent whenever the semantic
	// vector index starts, progresses through (throttled to ~1/s), or
	// finishes a round of (re)indexing (PLAN §34f item 5), so a Settings
	// page can show live progress without polling.
	EventSemanticIndex EventType = "semantic.index"
	// EventSemanticPull carries a SemanticPullEvent for each line of an
	// `ollama pull`'s NDJSON progress stream (PLAN §34f item 5).
	EventSemanticPull EventType = "semantic.pull"
)

// Event is one engine event.
type Event struct {
	Type    EventType `json:"type"`
	Time    time.Time `json:"time"`
	Payload any       `json:"payload,omitempty"`
}

// CatalogChange describes what a reindex changed for one service.
type CatalogChange struct {
	Service string   `json:"service"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
}
