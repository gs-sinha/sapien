package domain

// Task is a caller-vocabulary retrieval contract for one or more operations.
// Phrase/Operation are concise service.yaml aliases; registry normalization
// expands them into Phrases/Targets before a task enters the catalog.
type Task struct {
	ID        string       `yaml:"id,omitempty" json:"id"`
	Phrase    string       `yaml:"phrase,omitempty" json:"-"`
	Phrases   []string     `yaml:"phrases,omitempty" json:"phrases"`
	Operation string       `yaml:"operation,omitempty" json:"-"`
	Targets   []TaskTarget `yaml:"targets,omitempty" json:"targets"`
	Tests     []TaskTest   `yaml:"tests,omitempty" json:"tests,omitempty"`
}

// TaskTarget points at an operation that can perform a task. When explains
// the condition under which this target is the right choice.
type TaskTarget struct {
	Operation string `yaml:"operation" json:"operation"`
	When      string `yaml:"when,omitempty" json:"when,omitempty"`
}

// TaskTest is a held-out retrieval assertion. Query is deliberately separate
// from Phrases: tests are checked, never indexed.
type TaskTest struct {
	Query     string   `yaml:"query" json:"query"`
	ExpectAny []string `yaml:"expect_any" json:"expect_any"`
	TopK      int      `yaml:"top_k,omitempty" json:"top_k,omitempty"`
}

// TaskMatch explains which authored task caused an operation search hit.
type TaskMatch struct {
	ID     string `json:"id"`
	Phrase string `json:"phrase,omitempty"`
	When   string `json:"when,omitempty"`
}

// TaskCoverage summarizes executable intent-retrieval assertions.
type TaskCoverage struct {
	Tasks        int `json:"tasks"`
	Assertions   int `json:"assertions"`
	Discoverable int `json:"discoverable"`
}
