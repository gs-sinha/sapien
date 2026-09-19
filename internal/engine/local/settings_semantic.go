// PLAN §34f item 5's settings surface: engine.SettingsAPI's semantic-search
// methods, the pre-save/test probe, and Ollama discovery/pull. Kept apart
// from semantic.go (which owns the embedder/index plumbing and status
// tracking every method here calls into) so that file stays about
// indexing, and this one about the HTTP-shaped settings contract.
package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/semantic"
)

// settingsAPI implements engine.SettingsAPI over a Local.
type settingsAPI struct{ l *Local }

var _ engine.SettingsAPI = (*settingsAPI)(nil)

func (a *settingsAPI) GetSemantic(ctx context.Context) (*domain.SemanticSettings, error) {
	return a.l.getSemanticSettings(ctx)
}

func (a *settingsAPI) PutSemantic(ctx context.Context, req engine.SemanticPutRequest) (*domain.SemanticSettings, error) {
	return a.l.putSemanticSettings(ctx, req)
}

func (a *settingsAPI) TestSemantic(ctx context.Context, req domain.SemanticProbe) (*domain.SemanticTestResult, error) {
	return probeSemantic(ctx, req), nil
}

func (a *settingsAPI) ReindexSemantic(ctx context.Context) error {
	return a.l.startSemanticReindex(ctx)
}

func (a *settingsAPI) OllamaStatus(ctx context.Context, baseURL string) (*domain.OllamaStatus, error) {
	return a.l.ollamaStatus(ctx, baseURL)
}

func (a *settingsAPI) OllamaPull(ctx context.Context, req domain.OllamaPullRequest) error {
	return a.l.startOllamaPull(ctx, req)
}

// getSemanticSettings builds GET /v1/settings/semantic's response from the
// merged, effective config.Config plus a live SemanticStatus.
func (l *Local) getSemanticSettings(ctx context.Context) (*domain.SemanticSettings, error) {
	cfg, err := config.Load(l.ws)
	if err != nil {
		return nil, err
	}
	source, err := config.SemanticSource(l.ws)
	if err != nil {
		return nil, err
	}
	status, err := l.SemanticStatus(ctx)
	if err != nil {
		return nil, err
	}
	s := cfg.Semantic
	return &domain.SemanticSettings{
		Enabled:   s.Enabled,
		Kind:      s.Kind,
		BaseURL:   s.BaseURL,
		Model:     s.Model,
		BatchSize: s.BatchSize,
		APIKeySet: s.APIKey != "",
		Source:    source,
		Status:    status,
	}, nil
}

// putSemanticSettings implements PUT /v1/settings/semantic (PLAN §34f item
// 5): resolve defaults, probe unless disabled or forced, write the file
// req.Scope names, apply live to l, and trigger a reindex when the
// live-applied config actually changed (just turned on, or kind/base_url/
// model differ from what was applied before).
func (l *Local) putSemanticSettings(ctx context.Context, req engine.SemanticPutRequest) (*domain.SemanticSettings, error) {
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "workspace" {
		return nil, errs.New(errs.Invalid, "settings: scope must be \"user\" or \"workspace\", got %q", scope)
	}

	currentCfg, err := config.Load(l.ws)
	if err != nil {
		return nil, err
	}

	resolved, err := resolveSemanticDefaults(req)
	if err != nil {
		return nil, err
	}

	apiKeyForProbe := currentCfg.Semantic.APIKey
	if req.APIKey != nil {
		apiKeyForProbe = *req.APIKey
	}

	if resolved.Enabled && !req.Force {
		result := probeSemantic(ctx, domain.SemanticProbe{
			Kind: resolved.Kind, BaseURL: resolved.BaseURL, Model: resolved.Model,
			APIKey: apiKeyForProbe, BatchSize: resolved.BatchSize,
		})
		if !result.OK {
			return nil, errs.New(errs.Invalid, "semantic: %s", result.Error).
				WithHint(`pass "force": true to save it anyway`)
		}
	}

	path := config.UserPath()
	if scope == "workspace" {
		path = config.WorkspacePath(l.ws)
	}
	if err := config.WriteSemantic(path, config.SemanticWrite{
		Enabled: resolved.Enabled, Kind: resolved.Kind, BaseURL: resolved.BaseURL,
		Model: resolved.Model, BatchSize: resolved.BatchSize, APIKey: req.APIKey,
	}); err != nil {
		return nil, err
	}

	after, err := config.Load(l.ws)
	if err != nil {
		return nil, err
	}

	before := l.appliedSemanticConfig()
	if err := l.ApplySemantic(after.Semantic); err != nil {
		return nil, err
	}
	if semanticLiveChangeNeeded(before, after.Semantic) {
		l.spawnSemanticReindex()
	}

	return l.getSemanticSettings(ctx)
}

// resolveSemanticDefaults applies PUT /v1/settings/semantic's server-side
// validation and defaults (PLAN §34f item 5): kind is required (and must be
// "openai" or "ollama") when enabled; base_url defaults to
// http://127.0.0.1:11434 for ollama and is otherwise required, and must
// parse as http/https; batch_size defaults to 8 for ollama, 32 otherwise,
// when zero. A disabled request skips every check past trimming: turning
// semantic search off never needs a valid provider.
func resolveSemanticDefaults(req engine.SemanticPutRequest) (config.Semantic, error) {
	out := config.Semantic{
		Enabled:   req.Enabled,
		Kind:      strings.ToLower(strings.TrimSpace(req.Kind)),
		BaseURL:   strings.TrimSpace(req.BaseURL),
		Model:     strings.TrimSpace(req.Model),
		BatchSize: req.BatchSize,
	}
	if !out.Enabled {
		return out, nil
	}
	if out.Kind == "" {
		return config.Semantic{}, errs.New(errs.Invalid, "settings: kind is required when semantic search is enabled")
	}
	if out.Kind != "openai" && out.Kind != "ollama" {
		return config.Semantic{}, errs.New(errs.Invalid, "settings: kind must be \"openai\" or \"ollama\", got %q", out.Kind)
	}
	if out.Model == "" {
		return config.Semantic{}, errs.New(errs.Invalid, "settings: model is required when semantic search is enabled")
	}
	if out.BaseURL == "" {
		if out.Kind == "ollama" {
			out.BaseURL = ollamaDefaultBaseURL
		} else {
			return config.Semantic{}, errs.New(errs.Invalid, "settings: base_url is required")
		}
	}
	if err := validateHTTPURL(out.BaseURL); err != nil {
		return config.Semantic{}, err
	}
	if out.BatchSize == 0 {
		if out.Kind == "ollama" {
			out.BatchSize = 8
		} else {
			out.BatchSize = 32
		}
	}
	return out, nil
}

// semanticLiveChangeNeeded reports whether after's config warrants a
// reindex given what before had actually been applied: never for turning
// off, always for turning on, otherwise only when the provider/model
// itself changed (a batch_size or api_key change alone never invalidates
// already-embedded vectors).
func semanticLiveChangeNeeded(before, after config.Semantic) bool {
	if !after.Enabled {
		return false
	}
	if !before.Enabled {
		return true
	}
	return before.Kind != after.Kind || before.BaseURL != after.BaseURL || before.Model != after.Model
}

// spawnSemanticReindex runs a full SemanticReindex in the background,
// tracked by l.semWG so Close (and thus a one-shot CLI invocation) waits
// for it before closing l.db.
func (l *Local) spawnSemanticReindex() {
	l.semWG.Add(1)
	go func() {
		defer l.semWG.Done()
		if err := l.SemanticReindex(context.Background()); err != nil {
			l.logger.Warn("semantic: background reindex failed", "error", err)
		}
	}()
}

// startSemanticReindex implements ReindexSemantic: refuses when semantic
// search is off (there is nothing to (re)index into), otherwise starts a
// tracked background SemanticReindex and returns immediately.
func (l *Local) startSemanticReindex(ctx context.Context) error {
	if l.semanticIndex() == nil {
		return errs.New(errs.Invalid, "settings: semantic search is off; enable it first")
	}
	l.spawnSemanticReindex()
	return nil
}

// ReloadSemantic reapplies l's current merged, effective config.Semantic
// (user + workspace) live, and triggers a reindex if that actually changed
// anything. It is how the daemon (internal/server) propagates a
// "user"-scope settings change to every OTHER open workspace, once
// Settings().PutSemantic has already applied it to the one the request
// named -- see PLAN §34f item 5's "a user-scope change must be applied to
// every open workspace engine". Not part of engine.SettingsAPI: engine.
// Remote has no equivalent (a daemon's own workspaces are always
// engine.Local), so the server type-asserts for it instead of it being a
// facade method every Engine must implement.
func (l *Local) ReloadSemantic(ctx context.Context) error {
	before := l.appliedSemanticConfig()
	cfg, err := config.Load(l.ws)
	if err != nil {
		return err
	}
	if err := l.ApplySemantic(cfg.Semantic); err != nil {
		return err
	}
	if semanticLiveChangeNeeded(before, cfg.Semantic) {
		l.spawnSemanticReindex()
	}
	return nil
}

// semanticProbeText/semanticProbeTimeout bound POST .../test's (and PUT's
// pre-save validation's) one-off embed call.
const (
	semanticProbeText    = "sapien semantic search connectivity check"
	semanticProbeTimeout = 10 * time.Second
)

// probeSemantic embeds one short string with req's config without saving
// anything (PLAN §34f item 5: `test`, and PUT's pre-save validation unless
// force). It never returns a Go error: every failure -- an invalid kind, an
// invalid base_url, a missing model, a request the provider itself
// rejects -- comes back as {ok:false, error}, matching "HTTP 200 both
// ways".
func probeSemantic(ctx context.Context, req domain.SemanticProbe) *domain.SemanticTestResult {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind != "openai" && kind != "ollama" {
		return &domain.SemanticTestResult{OK: false, Error: fmt.Sprintf("kind must be \"openai\" or \"ollama\", got %q", req.Kind)}
	}
	if strings.TrimSpace(req.Model) == "" {
		return &domain.SemanticTestResult{OK: false, Error: "model is required"}
	}
	if err := validateHTTPURL(req.BaseURL); err != nil {
		return &domain.SemanticTestResult{OK: false, Error: err.Error()}
	}

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind: req.Kind, BaseURL: req.BaseURL, Model: req.Model,
		APIKey: req.APIKey, BatchSize: req.BatchSize, Timeout: semanticProbeTimeout,
	})
	if err != nil {
		return &domain.SemanticTestResult{OK: false, Error: err.Error()}
	}

	start := time.Now()
	vecs, err := emb.Embed(ctx, []string{semanticProbeText})
	latency := time.Since(start)
	if err != nil {
		return &domain.SemanticTestResult{OK: false, Error: err.Error()}
	}
	dim := 0
	if len(vecs) > 0 {
		dim = len(vecs[0])
	}
	return &domain.SemanticTestResult{OK: true, Dim: dim, LatencyMS: latency.Milliseconds()}
}

// validateHTTPURL rejects anything that is not an absolute http(s) URL --
// shared by PUT's base_url, test's base_url, and both Ollama endpoints'
// base_url.
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errs.New(errs.Invalid, "settings: base_url must be a valid http(s) URL, got %q", raw)
	}
	return nil
}

// ollamaDefaultBaseURL is used whenever an Ollama base_url is unset and the
// workspace's own configured semantic.base_url is also unset or not
// pointed at Ollama (PLAN §34f item 5).
const ollamaDefaultBaseURL = "http://127.0.0.1:11434"

// ollamaProbeTimeout bounds GET .../ollama's /api/tags probe.
const ollamaProbeTimeout = 5 * time.Second

// resolveOllamaBaseURL fills in baseURL when empty: the workspace's own
// configured semantic.base_url when its kind is ollama, else
// ollamaDefaultBaseURL. Either way, the result must parse as http/https.
func (l *Local) resolveOllamaBaseURL(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		if cfg, err := config.Load(l.ws); err == nil && cfg.Semantic.Kind == "ollama" && cfg.Semantic.BaseURL != "" {
			baseURL = cfg.Semantic.BaseURL
		}
	}
	if baseURL == "" {
		baseURL = ollamaDefaultBaseURL
	}
	if err := validateHTTPURL(baseURL); err != nil {
		return "", err
	}
	return baseURL, nil
}

func (l *Local) ollamaStatus(ctx context.Context, baseURL string) (*domain.OllamaStatus, error) {
	resolved, err := l.resolveOllamaBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	return probeOllamaTags(ctx, resolved), nil
}

// ollamaTagsResponse is Ollama's GET /api/tags response shape (just the
// fields Sapien reports).
type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"models"`
}

// probeOllamaTags GETs baseURL's /api/tags. Like probeSemantic, it never
// returns a Go error: an unreachable endpoint, a non-200 status, or a
// malformed response all come back as {reachable:false, error}.
func probeOllamaTags(ctx context.Context, baseURL string) *domain.OllamaStatus {
	client := &http.Client{Timeout: ollamaProbeTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/tags", nil)
	if err != nil {
		return &domain.OllamaStatus{Reachable: false, BaseURL: baseURL, Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return &domain.OllamaStatus{Reachable: false, BaseURL: baseURL, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &domain.OllamaStatus{Reachable: false, BaseURL: baseURL,
			Error: fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	var out ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return &domain.OllamaStatus{Reachable: false, BaseURL: baseURL, Error: err.Error()}
	}
	models := make([]domain.OllamaModel, 0, len(out.Models))
	for _, m := range out.Models {
		models = append(models, domain.OllamaModel{Name: m.Name, Size: m.Size})
	}
	return &domain.OllamaStatus{Reachable: true, BaseURL: baseURL, Models: models}
}

// startOllamaPull implements OllamaPull: validates the model/base_url,
// refuses (errs.Conflict) a model already being pulled, then starts the
// pull in the background and returns immediately.
func (l *Local) startOllamaPull(ctx context.Context, req domain.OllamaPullRequest) error {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return errs.New(errs.Invalid, "settings: model is required")
	}
	baseURL, err := l.resolveOllamaBaseURL(req.BaseURL)
	if err != nil {
		return err
	}

	l.semPullMu.Lock()
	if l.semPulls == nil {
		l.semPulls = map[string]bool{}
	}
	if l.semPulls[model] {
		l.semPullMu.Unlock()
		return errs.New(errs.Conflict, "settings: %q is already being pulled", model)
	}
	l.semPulls[model] = true
	l.semPullMu.Unlock()

	// context.Background(), not ctx: the HTTP request that started this
	// returns (202) well before a model finishes downloading, and the pull
	// must outlive it. It is not tracked by l.semWG (see local.go's
	// comment on that field): it makes no l.db calls, so it is safe to
	// outlive a workspace close or daemon shutdown.
	go l.runOllamaPull(context.Background(), baseURL, model)
	return nil
}

func (l *Local) finishOllamaPull(model string) {
	l.semPullMu.Lock()
	delete(l.semPulls, model)
	l.semPullMu.Unlock()
}

// ollamaPullLine is one line of `ollama pull`'s NDJSON progress stream.
type ollamaPullLine struct {
	Status    string `json:"status"`
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
	Error     string `json:"error"`
}

// runOllamaPull POSTs {"model", "stream": true} to baseURL's /api/pull and
// relays each NDJSON line as a semantic.pull event, until the stream ends
// or reports an error. Always emits a final Done: true event, even if the
// stream never sent an explicit terminal status, so a client waiting on
// Done never hangs.
func (l *Local) runOllamaPull(ctx context.Context, baseURL, model string) {
	defer l.finishOllamaPull(model)

	body, err := json.Marshal(map[string]any{"model": model, "stream": true})
	if err != nil {
		l.emit(domain.EventSemanticPull, domain.SemanticPullEvent{Model: model, Done: true, Error: err.Error()})
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/pull", bytes.NewReader(body))
	if err != nil {
		l.emit(domain.EventSemanticPull, domain.SemanticPullEvent{Model: model, Done: true, Error: err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.emit(domain.EventSemanticPull, domain.SemanticPullEvent{Model: model, Done: true, Error: err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		l.emit(domain.EventSemanticPull, domain.SemanticPullEvent{Model: model, Done: true,
			Error: fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))})
		return
	}

	dec := json.NewDecoder(resp.Body)
	sawDone := false
	var lastErr string
	for {
		var line ollamaPullLine
		if err := dec.Decode(&line); err != nil {
			if err == io.EOF {
				break
			}
			l.emit(domain.EventSemanticPull, domain.SemanticPullEvent{Model: model, Done: true, Error: err.Error()})
			return
		}
		if line.Error != "" {
			lastErr = line.Error
		}
		done := strings.EqualFold(line.Status, "success")
		if done {
			sawDone = true
		}
		l.emit(domain.EventSemanticPull, domain.SemanticPullEvent{
			Model: model, Status: line.Status, Completed: line.Completed, Total: line.Total,
			Done: done, Error: line.Error,
		})
	}
	if !sawDone {
		l.emit(domain.EventSemanticPull, domain.SemanticPullEvent{Model: model, Status: "success", Done: true, Error: lastErr})
	}
}
