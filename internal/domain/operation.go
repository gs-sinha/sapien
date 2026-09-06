package domain

// Protocol is the wire protocol of an operation.
type Protocol string

const (
	ProtocolHTTP Protocol = "http"
)

// SourceLoc points at where something was defined.
type SourceLoc struct {
	File    string `json:"file"`
	Pointer string `json:"pointer,omitempty"` // JSON pointer inside the file
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// HTTPBinding is the protocol-specific part of an HTTP operation.
type HTTPBinding struct {
	Method string `json:"method"` // upper-case
	Path   string `json:"path"`   // as written in the contract, e.g. /v1/riders/{riderId}
}

// ParamLocation is where a parameter travels.
type ParamLocation string

const (
	InPath   ParamLocation = "path"
	InQuery  ParamLocation = "query"
	InHeader ParamLocation = "header"
	InCookie ParamLocation = "cookie"
)

// Param is a request parameter.
type Param struct {
	Name        string        `json:"name"`
	In          ParamLocation `json:"in"`
	Required    bool          `json:"required"`
	Description string        `json:"description,omitempty"`
	Schema      *Schema       `json:"schema,omitempty"`
	Example     any           `json:"example,omitempty"`
	Deprecated  bool          `json:"deprecated,omitempty"`
}

// Body is a request body.
type Body struct {
	ContentType string    `json:"content_type"`
	Required    bool      `json:"required"`
	Description string    `json:"description,omitempty"`
	Schema      *Schema   `json:"schema,omitempty"`
	Examples    []Example `json:"examples,omitempty"`
}

// Response is one documented response.
type Response struct {
	Status      string             `json:"status"` // "200", "2XX", "default"
	Description string             `json:"description,omitempty"`
	ContentType string             `json:"content_type,omitempty"`
	Schema      *Schema            `json:"schema,omitempty"`
	Headers     map[string]*Schema `json:"headers,omitempty"`
	Examples    []Example          `json:"examples,omitempty"`
}

// Example is a named example value.
type Example struct {
	Name    string `json:"name,omitempty"`
	Summary string `json:"summary,omitempty"`
	Value   any    `json:"value"`
}

// SecurityRequirement is one alternative set of security schemes.
type SecurityRequirement struct {
	Scheme string   `json:"scheme"` // name in components/securitySchemes
	Type   string   `json:"type"`   // http, apiKey, oauth2, openIdConnect
	Scopes []string `json:"scopes,omitempty"`
}

// Operation is a normalized, protocol-independent API operation.
type Operation struct {
	ID          string                `json:"id"` // "<service>.<operationId>"
	ServiceID   string                `json:"service_id"`
	Protocol    Protocol              `json:"protocol"`
	HTTP        *HTTPBinding          `json:"http,omitempty"`
	RawOpID     string                `json:"raw_operation_id,omitempty"` // contract operationId if present
	Synthesized bool                  `json:"synthesized,omitempty"`      // ID was derived from method+path
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Concepts    []string              `json:"concepts,omitempty"`
	Params      []Param               `json:"params,omitempty"`
	RequestBody *Body                 `json:"request_body,omitempty"`
	Responses   []Response            `json:"responses,omitempty"`
	Security    []SecurityRequirement `json:"security,omitempty"`
	Deprecated  bool                  `json:"deprecated,omitempty"`
	Source      SourceLoc             `json:"source"`
	Hash        string                `json:"hash"`
}

// Field is one flattened schema field of an operation, addressed by a field path such as
// request.body.customer.id, request.query.limit, response.200.body.rider.qcomSkill,
// response.200.body.items[].riderId.
type Field struct {
	OperationID string `json:"operation_id"`
	Path        string `json:"path"`
	Type        string `json:"type"` // string, integer, number, boolean, object, array, null, or a union like "string|null"
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
	Format      string `json:"format,omitempty"`
}

// Alias maps METHOD + path to an operation ID for lookup and fallback resolution.
type Alias struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	OperationID string `json:"operation_id"`
}
