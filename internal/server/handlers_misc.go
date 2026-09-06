package server

import "net/http"

type healthResponse struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version"`
	Workspace string `json:"workspace"`
}

// handleHealth implements GET /v1/health, the one unauthenticated route.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	var dir string
	if ws := s.engine.Workspace(); ws != nil {
		dir = ws.Dir
	}
	writeJSON(w, http.StatusOK, healthResponse{OK: true, Version: s.version, Workspace: dir})
}

// handleWorkspace implements GET /v1/workspace.
func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Workspace())
}

// handleOpenAPI implements GET /v1/openapi.json: a static, hand-written
// document generated once (in New) from the same route table used to
// register routes, so the two can't drift apart.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.openAPI)
}
