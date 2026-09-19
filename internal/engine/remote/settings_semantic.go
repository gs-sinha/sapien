package remote

import (
	"context"
	"net/http"
	"net/url"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// settingsAPI is the remote view of engine.SettingsAPI.
type settingsAPI Remote

func (s *settingsAPI) r() *Remote { return (*Remote)(s) }

// Settings returns the settings API.
func (r *Remote) Settings() engine.SettingsAPI { return (*settingsAPI)(r) }

var _ engine.SettingsAPI = (*settingsAPI)(nil)

// GetSemantic maps to GET /v1/settings/semantic.
func (s *settingsAPI) GetSemantic(ctx context.Context) (*domain.SemanticSettings, error) {
	var out domain.SemanticSettings
	if err := s.r().do(ctx, http.MethodGet, "/v1/settings/semantic", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutSemantic maps to PUT /v1/settings/semantic.
func (s *settingsAPI) PutSemantic(ctx context.Context, req engine.SemanticPutRequest) (*domain.SemanticSettings, error) {
	var out domain.SemanticSettings
	if err := s.r().do(ctx, http.MethodPut, "/v1/settings/semantic", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TestSemantic maps to POST /v1/settings/semantic/test.
func (s *settingsAPI) TestSemantic(ctx context.Context, req domain.SemanticProbe) (*domain.SemanticTestResult, error) {
	var out domain.SemanticTestResult
	if err := s.r().do(ctx, http.MethodPost, "/v1/settings/semantic/test", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReindexSemantic maps to POST /v1/settings/semantic/reindex.
func (s *settingsAPI) ReindexSemantic(ctx context.Context) error {
	return s.r().do(ctx, http.MethodPost, "/v1/settings/semantic/reindex", nil, nil, nil)
}

// OllamaStatus maps to GET /v1/settings/semantic/ollama.
func (s *settingsAPI) OllamaStatus(ctx context.Context, baseURL string) (*domain.OllamaStatus, error) {
	var q url.Values
	if baseURL != "" {
		q = url.Values{"base_url": {baseURL}}
	}
	var out domain.OllamaStatus
	if err := s.r().do(ctx, http.MethodGet, "/v1/settings/semantic/ollama", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OllamaPull maps to POST /v1/settings/semantic/ollama/pull.
func (s *settingsAPI) OllamaPull(ctx context.Context, req domain.OllamaPullRequest) error {
	return s.r().do(ctx, http.MethodPost, "/v1/settings/semantic/ollama/pull", nil, req, nil)
}
