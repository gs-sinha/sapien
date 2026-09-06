// Package domain holds the protocol-independent types shared by every Sapien
// component: services, operations, schemas, docs, flows, runs, memories,
// environments, and workspaces. It has no dependencies on storage or transport.
package domain

// Doc is a documentation file (or a contract-embedded document) belonging to a service.
type Doc struct {
	ID        string       `json:"id"` // "<service>/docs/allocation.md" or "<service>/contract#info"
	ServiceID string       `json:"service_id"`
	Path      string       `json:"path"` // relative to the service package; "contract#info", "contract#tag:<name>" for embedded docs
	Title     string       `json:"title"`
	Source    DocSource    `json:"source"`
	Hash      string       `json:"hash"`
	Sections  []DocSection `json:"sections,omitempty"`
}

// DocSource says where a doc came from.
type DocSource string

const (
	DocSourceFile         DocSource = "file"
	DocSourceContractInfo DocSource = "contract_info"
	DocSourceContractTag  DocSource = "contract_tag"
)

// DocSection is one heading-delimited section of a Doc.
type DocSection struct {
	ID      string   `json:"id"` // Doc.ID + "#" + heading slug
	Ord     int      `json:"ord"`
	Heading string   `json:"heading"`
	Level   int      `json:"level"`
	Body    string   `json:"body"` // Markdown
	Refs    []DocRef `json:"refs,omitempty"`
}

// DocRef is a structural reference extracted from a section's text.
type DocRef struct {
	Kind  RefKind `json:"kind"`
	Value string  `json:"value"`
}

// RefKind enumerates the things a doc section or memory can reference.
type RefKind string

const (
	RefOperation RefKind = "operation"
	RefPath      RefKind = "path"
	RefSchema    RefKind = "schema"
	RefField     RefKind = "field"
	RefConcept   RefKind = "concept"
	RefService   RefKind = "service"
)
