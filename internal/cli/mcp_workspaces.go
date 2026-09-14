package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/remote"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// remoteSwitcher lets a stdio `sapien mcp` bridge switch workspaces on the
// daemon it is bridging to. The daemon holds the engines (one process, many
// workspaces); this side just opens a second Remote client against the same
// daemon with a different X-Sapien-Workspace, and caches it so repeated
// switches between two workspaces don't reconnect every time.
//
// It deliberately asks the daemon for the list rather than reading the user
// config directly: the daemon knows which workspaces it already has open,
// and is the thing that will refuse one whose lock is held elsewhere.
type remoteSwitcher struct {
	resolver remote.EndpointResolver

	// epMu guards baseURL and token, which the resolver replaces when the
	// daemon behind them is gone or no longer accepts the token. Separate
	// from mu because Engine holds mu while it registers.
	epMu    sync.Mutex
	baseURL string
	token   string

	mu    sync.Mutex
	cache map[string]engine.Engine
}

func newRemoteSwitcher(baseURL, token string, resolver remote.EndpointResolver, primary engine.Engine) *remoteSwitcher {
	s := &remoteSwitcher{baseURL: baseURL, token: token, resolver: resolver, cache: map[string]engine.Engine{}}
	if ws := primary.Workspace(); ws != nil {
		s.cache[ws.Dir] = primary
	}
	return s
}

// List asks the daemon what it can serve, falling back to this machine's
// registered workspaces if the daemon is too old to answer (the route is
// absent) or unreachable -- a switcher that lists nothing would make the
// tools look broken rather than degraded.
func (s *remoteSwitcher) List() []workspaces.Info {
	if list, err := s.fetchList(); err == nil {
		return list
	}
	return localWorkspaceInfos()
}

func (s *remoteSwitcher) fetchList() ([]workspaces.Info, error) {
	var out []workspaces.Info
	if err := s.do(context.Background(), http.MethodGet, "/v1/workspaces", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// endpoint returns the daemon address and token this switcher currently
// believes in.
func (s *remoteSwitcher) endpoint() (string, string) {
	s.epMu.Lock()
	defer s.epMu.Unlock()
	return s.baseURL, s.token
}

// reresolve asks the resolver where the daemon is now and remembers it.
func (s *remoteSwitcher) reresolve(ctx context.Context) error {
	if s.resolver == nil {
		return errs.New(errs.Internal, "no daemon resolver configured")
	}
	baseURL, token, err := s.resolver(ctx)
	if err != nil {
		return err
	}
	s.epMu.Lock()
	s.baseURL, s.token = baseURL, token
	s.epMu.Unlock()
	return nil
}

// do performs one JSON request against the daemon the way engine.Remote
// does: a connection-level failure or an HTTP 401 triggers exactly one
// re-resolve of the endpoint and a replay. This is the fix for the first
// friction report an agent filed (github.com/gs-sinha/sapien/discussions/1):
// the switcher held the token captured when the bridge started, so after
// `serve --restart` every switch_workspace failed with a 401 while the
// primary client, which re-resolves, kept working -- and list_workspaces
// hid the failure by falling back to the local registry. Non-2xx answers
// are decoded as the daemon's errs envelope so the agent sees the real
// code rather than "HTTP 409".
func (s *remoteSwitcher) do(ctx context.Context, method, path string, body []byte, out any) error {
	attempt := func() (*http.Response, error) {
		baseURL, token := s.endpoint()
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return http.DefaultClient.Do(req)
	}

	resp, err := attempt()
	if (err != nil || resp.StatusCode == http.StatusUnauthorized) && s.resolver != nil && ctx.Err() == nil {
		if resp != nil {
			resp.Body.Close()
		}
		if rerr := s.reresolve(ctx); rerr != nil {
			return errs.Wrap(errs.Internal, rerr, "%s %s: the daemon did not answer and re-resolving it failed", method, path)
		}
		resp, err = attempt()
	}
	if err != nil {
		return errs.Wrap(errs.Internal, err, "%s %s", method, path)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error *errs.Error `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error != nil && envelope.Error.Code != "" {
			return envelope.Error
		}
		var bare errs.Error
		if json.Unmarshal(data, &bare) == nil && bare.Code != "" {
			return &bare
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return errs.New(errs.PermissionDenied, "%s %s: the daemon rejected this session's token even after re-resolving it", method, path).
				WithHint("restart the MCP session; the daemon was replaced and its new token could not be read")
		}
		return errs.New(errs.Internal, "%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return errs.Wrap(errs.Internal, err, "decoding %s %s", method, path)
		}
	}
	return nil
}

// Engine returns a client bound to dir on the same daemon.
func (s *remoteSwitcher) Engine(dir string) (engine.Engine, error) {
	ws, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if eng, ok := s.cache[ws.Dir]; ok {
		return eng, nil
	}

	// Registering the workspace with the daemon first means the failure
	// (a workspace whose lock another daemon holds, say) surfaces here
	// rather than on the agent's next unrelated tool call.
	if err := s.register(ws.Dir); err != nil {
		return nil, err
	}

	baseURL, token := s.endpoint()
	eng, err := remote.New(baseURL, token,
		remote.WithEndpointResolver(s.resolver),
		remote.WithWorkspace(ws.Dir))
	if err != nil {
		return nil, err
	}
	s.cache[ws.Dir] = eng
	return eng, nil
}

func (s *remoteSwitcher) register(dir string) error {
	body, err := json.Marshal(map[string]string{"dir": dir})
	if err != nil {
		return err
	}
	if err := s.do(context.Background(), http.MethodPost, "/v1/workspaces", body, nil); err != nil {
		return errs.As(err).WithDetail("dir", dir)
	}
	return nil
}

// Close closes every client this switcher opened. The primary engine it was
// seeded with belongs to the caller and is left alone.
func (s *remoteSwitcher) Close(primary engine.Engine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, eng := range s.cache {
		if eng != primary {
			_ = eng.Close()
		}
	}
}

// localWorkspaceInfos builds the list from this machine's registry, for the
// in-process (`SAPIEN_NO_DAEMON=1`) path and as List's fallback.
func localWorkspaceInfos() []workspaces.Info {
	dirs, err := config.KnownWorkspaces()
	if err != nil {
		return nil
	}
	out := make([]workspaces.Info, 0, len(dirs))
	for _, dir := range dirs {
		info := workspaces.Info{Dir: dir}
		ws, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
		if err != nil {
			info.Error = err.Error()
			info.Name = filepath.Base(dir)
		} else {
			info.Name = ws.Name
			info.Services = len(ws.Services)
		}
		out = append(out, info)
	}
	return out
}
