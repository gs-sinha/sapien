package cli

import (
	"bytes"
	"encoding/json"
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
	baseURL  string
	token    string
	resolver remote.EndpointResolver

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
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/v1/workspaces", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errs.New(errs.Internal, "listing workspaces: HTTP %d", resp.StatusCode)
	}

	var out []workspaces.Info
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
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

	eng, err := remote.New(s.baseURL, s.token,
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
	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/v1/workspaces", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errs.New(errs.Internal, "opening workspace %s on the daemon: HTTP %d", dir, resp.StatusCode)
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
