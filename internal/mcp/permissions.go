package mcp

import (
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Permissions is what one MCP client is allowed to do (PLAN §23.2).
type Permissions struct {
	ReadContracts   bool
	ReadMemories    bool
	WriteMemories   bool
	ReadFlows       bool
	WriteFlows      bool
	WriteServices   bool
	WriteExamples   bool
	ReadRuns        bool
	ExecuteRead     bool
	ExecuteMutation bool

	// Environments lists the allowed environment names. Empty means all
	// non-production environments are allowed.
	Environments []string
	// AllowProduction allows execution against environments with
	// Environment.Production == true. Default false.
	AllowProduction bool
}

// DefaultPermissions is the default profile granted to a client with no
// explicit configuration: read everything, execute_read (non-production
// only), write_memories, write_flows, write_services, write_examples; no
// execute_mutation, no production.
func DefaultPermissions() Permissions {
	return Permissions{
		ReadContracts:   true,
		ReadMemories:    true,
		WriteMemories:   true,
		ReadFlows:       true,
		WriteFlows:      true,
		WriteServices:   true,
		WriteExamples:   true,
		ReadRuns:        true,
		ExecuteRead:     true,
		ExecuteMutation: false,
		Environments:    nil,
		AllowProduction: false,
	}
}

// Config is the merged MCP permission configuration for a workspace: a
// default profile plus per-client overrides, keyed by the client name
// reported in the MCP initialize request's clientInfo.name.
type Config struct {
	Default Permissions
	Clients map[string]Permissions
}

// For returns the permissions granted to client, falling back to the
// configured default profile when the client has no specific entry.
func (c Config) For(client string) Permissions {
	if p, ok := c.Clients[client]; ok {
		return p
	}
	return c.Default
}

// permClass names one permission field for error messages and YAML keys.
// The string value is both the class name used in E_PERMISSION_DENIED
// messages and the YAML field name under clients.<name>.<field>.
type permClass string

const (
	classReadContracts   permClass = "read_contracts"
	classReadMemories    permClass = "read_memories"
	classWriteMemories   permClass = "write_memories"
	classReadFlows       permClass = "read_flows"
	classWriteFlows      permClass = "write_flows"
	classWriteServices   permClass = "write_services"
	classWriteExamples   permClass = "write_examples"
	classReadRuns        permClass = "read_runs"
	classExecuteRead     permClass = "execute_read"
	classExecuteMutation permClass = "execute_mutation"
)

// allows reports whether p grants class c.
func (p Permissions) allows(c permClass) bool {
	switch c {
	case classReadContracts:
		return p.ReadContracts
	case classReadMemories:
		return p.ReadMemories
	case classWriteMemories:
		return p.WriteMemories
	case classReadFlows:
		return p.ReadFlows
	case classWriteFlows:
		return p.WriteFlows
	case classWriteServices:
		return p.WriteServices
	case classWriteExamples:
		return p.WriteExamples
	case classReadRuns:
		return p.ReadRuns
	case classExecuteRead:
		return p.ExecuteRead
	case classExecuteMutation:
		return p.ExecuteMutation
	default:
		return false
	}
}

// --- YAML loading -----------------------------------------------------

// rawPermissions mirrors Permissions but with optional (pointer) fields so a
// partial YAML document only overrides the fields it actually mentions.
type rawPermissions struct {
	ReadContracts   *bool     `yaml:"read_contracts"`
	ReadMemories    *bool     `yaml:"read_memories"`
	WriteMemories   *bool     `yaml:"write_memories"`
	ReadFlows       *bool     `yaml:"read_flows"`
	WriteFlows      *bool     `yaml:"write_flows"`
	WriteServices   *bool     `yaml:"write_services"`
	WriteExamples   *bool     `yaml:"write_examples"`
	ReadRuns        *bool     `yaml:"read_runs"`
	ExecuteRead     *bool     `yaml:"execute_read"`
	ExecuteMutation *bool     `yaml:"execute_mutation"`
	Environments    *[]string `yaml:"environments"`
	AllowProduction *bool     `yaml:"allow_production"`
}

// rawConfig is the shape of one mcp.yaml (or `mcp:` section) document.
type rawConfig struct {
	Default rawPermissions            `yaml:"default"`
	Clients map[string]rawPermissions `yaml:"clients"`
}

// rawDoc accepts either a top-level Config document (default:/clients: at
// the root, as in <workspace>/.sapien/mcp.yaml) or a document with an `mcp:`
// key holding the same shape (as in ~/.sapien/config.yaml, which has other
// top-level settings too).
type rawDoc struct {
	MCP     *rawConfig                `yaml:"mcp"`
	Default rawPermissions            `yaml:"default"`
	Clients map[string]rawPermissions `yaml:"clients"`
}

func (d rawDoc) effective() rawConfig {
	if d.MCP != nil {
		return *d.MCP
	}
	return rawConfig{Default: d.Default, Clients: d.Clients}
}

// mergeRawPermissions copies every non-nil field of src into dst, in place.
func mergeRawPermissions(dst *rawPermissions, src rawPermissions) {
	if src.ReadContracts != nil {
		dst.ReadContracts = src.ReadContracts
	}
	if src.ReadMemories != nil {
		dst.ReadMemories = src.ReadMemories
	}
	if src.WriteMemories != nil {
		dst.WriteMemories = src.WriteMemories
	}
	if src.ReadFlows != nil {
		dst.ReadFlows = src.ReadFlows
	}
	if src.WriteFlows != nil {
		dst.WriteFlows = src.WriteFlows
	}
	if src.WriteServices != nil {
		dst.WriteServices = src.WriteServices
	}
	if src.WriteExamples != nil {
		dst.WriteExamples = src.WriteExamples
	}
	if src.ReadRuns != nil {
		dst.ReadRuns = src.ReadRuns
	}
	if src.ExecuteRead != nil {
		dst.ExecuteRead = src.ExecuteRead
	}
	if src.ExecuteMutation != nil {
		dst.ExecuteMutation = src.ExecuteMutation
	}
	if src.Environments != nil {
		dst.Environments = src.Environments
	}
	if src.AllowProduction != nil {
		dst.AllowProduction = src.AllowProduction
	}
}

// applyRaw overlays the non-nil fields of src onto dst.
func applyRaw(dst *Permissions, src rawPermissions) {
	if src.ReadContracts != nil {
		dst.ReadContracts = *src.ReadContracts
	}
	if src.ReadMemories != nil {
		dst.ReadMemories = *src.ReadMemories
	}
	if src.WriteMemories != nil {
		dst.WriteMemories = *src.WriteMemories
	}
	if src.ReadFlows != nil {
		dst.ReadFlows = *src.ReadFlows
	}
	if src.WriteFlows != nil {
		dst.WriteFlows = *src.WriteFlows
	}
	if src.WriteServices != nil {
		dst.WriteServices = *src.WriteServices
	}
	if src.WriteExamples != nil {
		dst.WriteExamples = *src.WriteExamples
	}
	if src.ReadRuns != nil {
		dst.ReadRuns = *src.ReadRuns
	}
	if src.ExecuteRead != nil {
		dst.ExecuteRead = *src.ExecuteRead
	}
	if src.ExecuteMutation != nil {
		dst.ExecuteMutation = *src.ExecuteMutation
	}
	if src.Environments != nil {
		dst.Environments = *src.Environments
	}
	if src.AllowProduction != nil {
		dst.AllowProduction = *src.AllowProduction
	}
}

// LoadConfig reads and merges the given YAML files, in order: each
// subsequent file's explicitly-set fields override the accumulated result of
// the files before it. A typical call is
//
//	LoadConfig(filepath.Join(wsDir, ".sapien", "mcp.yaml"), filepath.Join(home, ".sapien", "config.yaml"))
//
// Each file may be either a top-level Config document (default:/clients: at
// the root) or a document with an `mcp:` key holding the same shape (so the
// user's global ~/.sapien/config.yaml can carry MCP settings alongside
// unrelated ones). Missing files are ignored, not an error.
func LoadConfig(paths ...string) (Config, error) {
	var defaultAgg rawPermissions
	clientAgg := map[string]*rawPermissions{}
	var clientOrder []string

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Config{}, fmt.Errorf("mcp: reading config %s: %w", path, err)
		}
		var doc rawDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return Config{}, fmt.Errorf("mcp: parsing config %s: %w", path, err)
		}
		rc := doc.effective()
		mergeRawPermissions(&defaultAgg, rc.Default)
		for name, p := range rc.Clients {
			existing, ok := clientAgg[name]
			if !ok {
				existing = &rawPermissions{}
				clientAgg[name] = existing
				clientOrder = append(clientOrder, name)
			}
			mergeRawPermissions(existing, p)
		}
	}

	cfg := Config{Default: DefaultPermissions()}
	applyRaw(&cfg.Default, defaultAgg)

	if len(clientOrder) > 0 {
		cfg.Clients = make(map[string]Permissions, len(clientOrder))
		for _, name := range clientOrder {
			p := cfg.Default
			applyRaw(&p, *clientAgg[name])
			cfg.Clients[name] = p
		}
	}
	return cfg, nil
}

// --- hot reload ---------------------------------------------------------

// pathStamp is a cheap fingerprint of one config file: whether it exists,
// its size, and its mtime. Comparing stamps lets configSource notice an
// edit (or a file appearing/disappearing) without rereading and
// reparsing every file on every permissionsFor call.
type pathStamp struct {
	exists bool
	size   int64
	mtime  time.Time
}

func statPath(path string) pathStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return pathStamp{}
	}
	return pathStamp{exists: true, size: fi.Size(), mtime: fi.ModTime()}
}

func statPaths(paths []string) []pathStamp {
	out := make([]pathStamp, len(paths))
	for i, p := range paths {
		out[i] = statPath(p)
	}
	return out
}

func stampsEqual(a, b []pathStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// configSource resolves the Config permissionsFor should use on each call.
// With no paths configured, it is a trivial wrapper around a static
// Config (Options.Config, kept working as-is for existing tests). With
// paths configured, Load rereads and reparses them via LoadConfig only
// when a stamp (statPaths) shows one of them changed since the last
// successful load -- including a file appearing or disappearing -- and
// otherwise reuses the cached Config. This is how a client's grant (e.g.
// execute_mutation) takes effect on the very next tool call after editing
// <workspace>/.sapien/mcp.yaml, with no daemon or client restart.
//
// Safe for concurrent use: the server calls Load from every tool call,
// which may run concurrently.
type configSource struct {
	paths  []string
	static Config

	mu     sync.Mutex
	loaded bool
	cached Config
	stamps []pathStamp
}

// newConfigSource builds a configSource. static is served as-is when
// paths is empty (the existing Options.Config behaviour); otherwise it is
// only a fallback for the rare case where the very first LoadConfig(paths...)
// fails (e.g. a syntax error in mcp.yaml before it's ever loaded
// successfully).
func newConfigSource(static Config, paths []string) *configSource {
	return &configSource{static: static, paths: paths}
}

// Load returns the Config to use right now, reloading from disk first if
// any configured path changed since the last load.
func (c *configSource) Load() Config {
	if len(c.paths) == 0 {
		return c.static
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	stamps := statPaths(c.paths)
	if c.loaded && stampsEqual(c.stamps, stamps) {
		return c.cached
	}

	cfg, err := LoadConfig(c.paths...)
	if err != nil {
		// Best-effort hot reload: a transient or malformed edit must not
		// deny every call for every client. Keep serving the last good
		// config (or the static fallback, before any load has ever
		// succeeded) and try again on the next call.
		if c.loaded {
			return c.cached
		}
		return c.static
	}

	c.cached = cfg
	c.stamps = stamps
	c.loaded = true
	return c.cached
}
