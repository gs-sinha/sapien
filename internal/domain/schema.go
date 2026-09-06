package domain

// SchemaKind is the normalized kind of a schema node.
type SchemaKind string

const (
	KindObject  SchemaKind = "object"
	KindArray   SchemaKind = "array"
	KindString  SchemaKind = "string"
	KindNumber  SchemaKind = "number"
	KindInteger SchemaKind = "integer"
	KindBoolean SchemaKind = "boolean"
	KindNull    SchemaKind = "null"
	KindOneOf   SchemaKind = "oneOf"
	KindAnyOf   SchemaKind = "anyOf"
	KindAllOf   SchemaKind = "allOf"
	KindRef     SchemaKind = "ref" // unresolved/cyclic reference to a named component
	KindAny     SchemaKind = "any" // no constraints
)

// Schema is a normalized subset of JSON Schema. References are resolved at ingest;
// component names are preserved in Name; cycles are kept as KindRef nodes.
type Schema struct {
	Kind                 SchemaKind         `json:"kind"`
	Name                 string             `json:"name,omitempty"` // component name when it came from components/schemas
	Ref                  string             `json:"ref,omitempty"`  // KindRef: "<service>.<ComponentName>"
	Description          string             `json:"description,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	PropertyOrder        []string           `json:"property_order,omitempty"` // stable order for rendering
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties *Schema            `json:"additional_properties,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Variants             []*Schema          `json:"variants,omitempty"` // oneOf / anyOf / allOf members
	Enum                 []any              `json:"enum,omitempty"`
	Format               string             `json:"format,omitempty"`
	Nullable             bool               `json:"nullable,omitempty"`
	Example              any                `json:"example,omitempty"`
	Default              any                `json:"default,omitempty"`
	ReadOnly             bool               `json:"read_only,omitempty"`
	WriteOnly            bool               `json:"write_only,omitempty"`
	Deprecated           bool               `json:"deprecated,omitempty"`
}

// NamedSchema is a component schema stored per service.
type NamedSchema struct {
	ServiceID string   `json:"service_id"`
	Name      string   `json:"name"`
	Hash      string   `json:"hash"`
	Schema    *Schema  `json:"schema"`
	UsedBy    []string `json:"used_by,omitempty"` // operation IDs
}
