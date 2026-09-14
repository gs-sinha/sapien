package domain

// ServiceRef is one entry in sapien.workspace.yaml.
type ServiceRef struct {
	Name   string `yaml:"name" json:"name"`
	Source Source `yaml:"source" json:"source"`
	// Team is the source committed in sapien.workspace.yaml when a local
	// override (sapien.workspace.local.yaml) replaced Source on this
	// machine; nil when Source is the committed one. Save writes Team back,
	// so an override never leaks into the committed file.
	Team *Source `yaml:"-" json:"team,omitempty"`
}

// Workspace is the parsed contents of sapien.workspace.yaml plus its resolved location.
type Workspace struct {
	Version            int          `yaml:"version" json:"version"`
	Name               string       `yaml:"name" json:"name"`
	Services           []ServiceRef `yaml:"services,omitempty" json:"services,omitempty"`
	DefaultEnvironment string       `yaml:"default_environment,omitempty" json:"default_environment,omitempty"`

	Dir  string `yaml:"-" json:"dir"`  // absolute directory containing sapien.workspace.yaml
	File string `yaml:"-" json:"file"` // absolute path of sapien.workspace.yaml
}

// Well-known workspace layout.
const (
	WorkspaceFileName = "sapien.workspace.yaml"
	WorkspaceStateDir = ".sapien"
	WorkspaceDBFile   = "sapien.db"
	FlowsDir          = "flows"
	MemoriesDir       = "memories"
	EnvironmentsDir   = "environments"
	ServicePackageDir = "api"
	FlowFileSuffix    = ".flow.yaml"
)

// Per-machine layout beside the committed workspace (PLAN §7b).
const (
	// WorkspaceLocalFileName is the gitignored per-machine override file:
	// it binds a service name to a local checkout that this machine reads
	// instead of the committed (git) source.
	WorkspaceLocalFileName = "sapien.workspace.local.yaml"
	// LocalDir holds this machine's tier of flows and memories
	// (<workspace>/local/flows, <workspace>/local/memories). It is
	// self-ignoring: local/.gitignore contains "*", like .sapien.
	LocalDir = "local"
)

// Flow owner kinds (FlowSummary.OwnerKind): the tier a flow file lives in.
const (
	// FlowOwnerLocal is this machine only: <workspace>/local/flows.
	FlowOwnerLocal = "local"
	// FlowOwnerWorkspace is the team's workspace repo: <workspace>/flows.
	FlowOwnerWorkspace = "workspace"
	// FlowOwnerService is the owning service's package: <service>/api/flows.
	FlowOwnerService = "service"
)
