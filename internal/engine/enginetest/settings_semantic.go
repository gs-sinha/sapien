package enginetest

import (
	"context"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// settingsAPI is the Fake's view of engine.SettingsAPI: an in-memory
// domain.SemanticSettings a test seeds through SetSemanticSettings, plus
// simple, deterministic stand-ins for test/reindex/Ollama that record the
// call and return a canned result rather than making a real embedding or
// Ollama call (see the package doc: Fake is a wire-format stand-in, not a
// behavioral model).
type settingsAPI Fake

func (s *settingsAPI) f() *Fake { return (*Fake)(s) }

// Settings returns the fake settings API.
func (f *Fake) Settings() engine.SettingsAPI { return (*settingsAPI)(f) }

// SetSemanticSettings seeds what Settings().GetSemantic reports.
func (f *Fake) SetSemanticSettings(s domain.SemanticSettings) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.semantic = s
}

func (s *settingsAPI) GetSemantic(ctx context.Context) (*domain.SemanticSettings, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Settings.GetSemantic", nil)
	cp := f.semantic
	return &cp, nil
}

// PutSemantic overwrites the seeded settings with req's fields (api_key is
// never stored back into what GetSemantic reports, matching the real
// engine never returning one) and reports APIKeySet from whether req set a
// non-empty key.
func (s *settingsAPI) PutSemantic(ctx context.Context, req engine.SemanticPutRequest) (*domain.SemanticSettings, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Settings.PutSemantic", req)

	if req.Enabled && strings.TrimSpace(req.Kind) == "" {
		return nil, errs.New(errs.Invalid, "settings: kind is required when semantic search is enabled")
	}

	f.semantic.Enabled = req.Enabled
	f.semantic.Kind = req.Kind
	f.semantic.BaseURL = req.BaseURL
	f.semantic.Model = req.Model
	f.semantic.BatchSize = req.BatchSize
	if req.APIKey != nil {
		f.semantic.APIKeySet = *req.APIKey != ""
	}
	if req.Kinds != nil {
		f.semantic.Kinds = append([]string(nil), (*req.Kinds)...)
	}
	switch {
	case req.ResetPrefixes:
		f.semantic.QueryPrefix, f.semantic.DocumentPrefix = f.semantic.DefaultQueryPrefix, f.semantic.DefaultDocumentPrefix
		f.semantic.PrefixesCustom = false
	case req.QueryPrefix != nil || req.DocumentPrefix != nil:
		if req.QueryPrefix != nil {
			f.semantic.QueryPrefix = *req.QueryPrefix
		}
		if req.DocumentPrefix != nil {
			f.semantic.DocumentPrefix = *req.DocumentPrefix
		}
		f.semantic.PrefixesCustom = true
	}
	scope := req.Scope
	if scope == "" {
		scope = "user"
	}
	f.semantic.Source = scope
	if req.Enabled {
		f.semantic.Status = domain.SemanticStatus{State: domain.SemanticReady, Model: req.Model}
	} else {
		f.semantic.Status = domain.SemanticStatus{State: domain.SemanticOff}
	}
	cp := f.semantic
	return &cp, nil
}

func (s *settingsAPI) TestSemantic(ctx context.Context, req domain.SemanticProbe) (*domain.SemanticTestResult, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Settings.TestSemantic", req)

	if req.Kind != "openai" && req.Kind != "ollama" {
		return &domain.SemanticTestResult{OK: false, Error: "unsupported kind"}, nil
	}
	return &domain.SemanticTestResult{OK: true, Dim: 8, LatencyMS: 1}, nil
}

func (s *settingsAPI) ReindexSemantic(ctx context.Context) error {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Settings.ReindexSemantic", nil)
	return nil
}

func (s *settingsAPI) OllamaStatus(ctx context.Context, baseURL string) (*domain.OllamaStatus, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Settings.OllamaStatus", baseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	return &domain.OllamaStatus{Reachable: true, BaseURL: baseURL, Models: []domain.OllamaModel{{Name: "nomic-embed-text", Size: 123}}}, nil
}

func (s *settingsAPI) OllamaPull(ctx context.Context, req domain.OllamaPullRequest) error {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Settings.OllamaPull", req)
	return nil
}
