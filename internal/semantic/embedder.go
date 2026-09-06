package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Embedder turns text into vectors. Model and Dim identify the embedding
// space so callers (Index) can tell whether two embeddings are comparable.
// Dim may be 0 before the first successful Embed call for an HTTP embedder,
// since it is learned from the first response rather than configured.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
	Dim() int
}

// HTTPConfig configures an HTTP-backed Embedder against an OpenAI-compatible
// or Ollama embedding endpoint.
type HTTPConfig struct {
	Kind string // "openai" | "ollama"

	BaseURL string

	// APIKey is either the literal key, or a reference to an environment
	// variable to read it from: "${env.NAME}" or "$NAME". An empty APIKey
	// sends no Authorization header (Ollama typically needs none).
	APIKey string

	Model string

	// BatchSize caps how many texts go in one HTTP request; Embed splits a
	// larger input into sequential batches of this size. Default 32.
	BatchSize int

	// Timeout bounds each HTTP request. Default 30s.
	Timeout time.Duration
}

const (
	defaultBatchSize = 32
	defaultTimeout   = 30 * time.Second
)

// httpEmbedder implements Embedder against an OpenAI-compatible ("openai")
// or Ollama ("ollama") HTTP embeddings endpoint.
type httpEmbedder struct {
	kind    string
	baseURL string
	apiKey  string
	model   string
	batch   int
	client  *http.Client

	mu  sync.Mutex
	dim int // learned from the first response; 0 until then
}

// NewHTTPEmbedder validates cfg and returns an Embedder that calls out to an
// external embedding endpoint over HTTP. It makes no network call itself;
// Dim() returns 0 until the first successful Embed.
func NewHTTPEmbedder(cfg HTTPConfig) (Embedder, error) {
	kind := strings.ToLower(strings.TrimSpace(cfg.Kind))
	if kind != "openai" && kind != "ollama" {
		return nil, fmt.Errorf("semantic: unsupported embedder kind %q (want \"openai\" or \"ollama\")", cfg.Kind)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("semantic: base_url is required")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("semantic: model is required")
	}

	batch := cfg.BatchSize
	if batch <= 0 {
		batch = defaultBatchSize
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &httpEmbedder{
		kind:    kind,
		baseURL: baseURL,
		apiKey:  resolveAPIKey(cfg.APIKey),
		model:   model,
		batch:   batch,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

// resolveAPIKey treats "${env.NAME}" or "$NAME" as an environment variable
// lookup; anything else (including "") is returned as-is (a literal key, or
// no key at all).
func resolveAPIKey(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return ""
	case strings.HasPrefix(raw, "${env.") && strings.HasSuffix(raw, "}"):
		name := strings.TrimSuffix(strings.TrimPrefix(raw, "${env."), "}")
		return os.Getenv(name)
	case strings.HasPrefix(raw, "$") && len(raw) > 1:
		return os.Getenv(raw[1:])
	default:
		return raw
	}
}

func (e *httpEmbedder) Model() string { return e.model }

func (e *httpEmbedder) Dim() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dim
}

func (e *httpEmbedder) learnDim(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dim == 0 {
		e.dim = n
	}
}

// Embed embeds texts, splitting into sequential batches of at most e.batch
// texts each. An empty texts returns (nil, nil) without any HTTP call.
func (e *httpEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += e.batch {
		end := start + e.batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := e.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}

	if len(out) > 0 && len(out[0]) > 0 {
		e.learnDim(len(out[0]))
	}
	return out, nil
}

func (e *httpEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	switch e.kind {
	case "openai":
		return e.embedOpenAI(ctx, texts)
	case "ollama":
		return e.embedOllama(ctx, texts)
	default:
		return nil, fmt.Errorf("semantic: unsupported embedder kind %q", e.kind)
	}
}

// doJSON POSTs body (already-marshaled JSON) to path and decodes a JSON
// response into out, returning an error that includes the response body on
// any non-200 status.
func (e *httpEmbedder) doJSON(ctx context.Context, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("semantic: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("semantic: request %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("semantic: read response %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("semantic: %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("semantic: decode response %s: %w", path, err)
	}
	return nil
}

// ---- OpenAI-compatible: POST {BaseURL}/embeddings {model, input[]} -> data[i].embedding ----

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (e *httpEmbedder) embedOpenAI(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(openAIEmbedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("semantic: marshal openai request: %w", err)
	}

	var out openAIEmbedResponse
	if err := e.doJSON(ctx, "/embeddings", body, &out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("semantic: openai returned %d embeddings for %d inputs", len(out.Data), len(texts))
	}

	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

// ---- Ollama: POST {BaseURL}/api/embed {model, input[]} -> embeddings[][] ----

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (e *httpEmbedder) embedOllama(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("semantic: marshal ollama request: %w", err)
	}

	var out ollamaEmbedResponse
	if err := e.doJSON(ctx, "/api/embed", body, &out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("semantic: ollama returned %d embeddings for %d inputs", len(out.Embeddings), len(texts))
	}
	return out.Embeddings, nil
}
