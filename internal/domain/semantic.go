package domain

// SemanticState is the workspace's semantic-search index state (PLAN §34f
// item 5): off by default, indexing while the background worker or an
// explicit reindex is embedding content, ready once it is caught up, error
// when the last attempt failed (search itself still degrades to
// lexical-only rather than surfacing that error to a caller -- see
// internal/engine/local's semanticAdapter -- but a Settings page wants to
// know).
type SemanticState string

const (
	SemanticOff      SemanticState = "off"
	SemanticReady    SemanticState = "ready"
	SemanticIndexing SemanticState = "indexing"
	SemanticError    SemanticState = "error"
)

// SemanticStatus is the live half of GET /v1/settings/semantic, and (as
// State/Embedded/Total alone) the semantic.index event's payload.
type SemanticStatus struct {
	State SemanticState `json:"state"`
	// Error is the last indexing failure's message; empty once a
	// subsequent attempt succeeds.
	Error string `json:"error,omitempty"`
	// Model and Dim identify the embedding space Embedded counts rows
	// under; both zero when State is "off", and Dim may be zero even when
	// on if no embed call has completed yet (internal/semantic.Embedder's
	// Dim is learned from the first response).
	Model string `json:"model,omitempty"`
	Dim   int    `json:"dim,omitempty"`
	// Embedded counts vectors-table rows under (Model, Dim); Total counts
	// operations + doc sections + memories the indexer would embed, as
	// internal/engine/local's indexer defines them.
	Embedded int `json:"embedded"`
	Total    int `json:"total"`
}

// SemanticSettings is GET (and PUT's response) /v1/settings/semantic's
// shape: the effective, merged configuration -- workspace overriding user,
// PLAN §16 -- with the api_key itself never included, plus live status.
type SemanticSettings struct {
	Enabled   bool   `json:"enabled"`
	Kind      string `json:"kind,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model,omitempty"`
	BatchSize int    `json:"batch_size,omitempty"`
	// APIKeySet reports whether an api_key is configured, without ever
	// revealing it.
	APIKeySet bool `json:"api_key_set"`
	// Source names which file the effective configuration came from:
	// "user", "workspace", or "default" (neither file sets it).
	Source string         `json:"source"`
	Status SemanticStatus `json:"status"`
}

// SemanticProbe is what POST /v1/settings/semantic/test sends (and what a
// PUT's pre-save validation builds from its own request): one embedding
// config to try, without saving it anywhere.
type SemanticProbe struct {
	Kind      string `json:"kind"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key,omitempty"`
	BatchSize int    `json:"batch_size,omitempty"`
}

// SemanticTestResult is POST /v1/settings/semantic/test's response.
// Reported at HTTP 200 whether OK is true or false: a provider that
// rejects the request is a normal, expected outcome, not a transport
// failure.
type SemanticTestResult struct {
	OK        bool   `json:"ok"`
	Dim       int    `json:"dim,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// OllamaModel is one entry of OllamaStatus.Models.
type OllamaModel struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// OllamaStatus is GET /v1/settings/semantic/ollama's response. Reported at
// HTTP 200 whether Reachable is true or false.
type OllamaStatus struct {
	Reachable bool          `json:"reachable"`
	BaseURL   string        `json:"base_url"`
	Models    []OllamaModel `json:"models,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// OllamaPullRequest is POST /v1/settings/semantic/ollama/pull's request
// body. BaseURL "" defers to the daemon's configured semantic.base_url,
// defaulting to http://127.0.0.1:11434.
type OllamaPullRequest struct {
	Model   string `json:"model"`
	BaseURL string `json:"base_url,omitempty"`
}

// SemanticIndexEvent is the semantic.index event's payload.
type SemanticIndexEvent struct {
	State    SemanticState `json:"state"`
	Embedded int           `json:"embedded"`
	Total    int           `json:"total"`
}

// SemanticPullEvent is the semantic.pull event's payload: one line of an
// `ollama pull`'s NDJSON progress stream, relayed close to verbatim.
type SemanticPullEvent struct {
	Model     string `json:"model"`
	Status    string `json:"status"`
	Completed int64  `json:"completed,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
}
