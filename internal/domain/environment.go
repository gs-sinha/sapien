package domain

// AuthType is a supported authentication style.
type AuthType string

const (
	AuthNone   AuthType = "none"
	AuthBearer AuthType = "bearer"
	AuthHeader AuthType = "header"
	AuthBasic  AuthType = "basic"
	AuthQuery  AuthType = "query"
)

// Auth is an authentication rule. Values may contain ${secret.NAME} references.
type Auth struct {
	Type     AuthType `yaml:"type" json:"type"`
	Token    string   `yaml:"token,omitempty" json:"token,omitempty"`       // bearer
	Name     string   `yaml:"name,omitempty" json:"name,omitempty"`         // header/query: header or param name
	Value    string   `yaml:"value,omitempty" json:"value,omitempty"`       // header/query
	Username string   `yaml:"username,omitempty" json:"username,omitempty"` // basic
	Password string   `yaml:"password,omitempty" json:"password,omitempty"` // basic
}

// ServiceEnv is a service's settings inside one environment.
type ServiceEnv struct {
	BaseURL string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	Auth    *Auth  `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// Transport holds per-environment HTTP settings.
type Transport struct {
	TimeoutMs        int    `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	ConnectTimeoutMs int    `yaml:"connect_timeout_ms,omitempty" json:"connect_timeout_ms,omitempty"`
	InsecureTLS      bool   `yaml:"insecure_tls,omitempty" json:"insecure_tls,omitempty"` // only honored when Production is false
	Proxy            string `yaml:"proxy,omitempty" json:"proxy,omitempty"`
	MaxRedirects     int    `yaml:"max_redirects,omitempty" json:"max_redirects,omitempty"`
	MaxBodyBytes     int64  `yaml:"max_body_bytes,omitempty" json:"max_body_bytes,omitempty"`
}

// Redaction configures what is scrubbed before persistence.
type Redaction struct {
	Headers   []string `yaml:"headers,omitempty" json:"headers,omitempty"`       // extra header names (defaults always apply)
	JSONPaths []string `yaml:"json_paths,omitempty" json:"json_paths,omitempty"` // e.g. body.customer.phone
}

// Environment is the parsed contents of environments/<name>.yaml.
type Environment struct {
	Version    int                   `yaml:"version" json:"version"`
	Name       string                `yaml:"name" json:"name"`
	Production bool                  `yaml:"production,omitempty" json:"production"`
	Services   map[string]ServiceEnv `yaml:"services,omitempty" json:"services,omitempty"`
	Vars       map[string]string     `yaml:"vars,omitempty" json:"vars,omitempty"`
	Auth       map[string]Auth       `yaml:"auth,omitempty" json:"auth,omitempty"` // "default" or a service name
	Transport  *Transport            `yaml:"transport,omitempty" json:"transport,omitempty"`
	Redaction  *Redaction            `yaml:"redaction,omitempty" json:"redaction,omitempty"`
	Path       string                `yaml:"-" json:"path,omitempty"`
}
