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
