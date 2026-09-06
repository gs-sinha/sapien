package domain

// ServiceRef is one entry in sapien.workspace.yaml.
type ServiceRef struct {
	Name   string `yaml:"name" json:"name"`
	Source Source `yaml:"source" json:"source"`
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
