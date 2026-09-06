package server

import (
	"context"
	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// handleEvents implements GET /v1/events: a WebSocket stream of
// domain.Event JSON, one message per event, until the client disconnects.
//
// Origin verification is left to hostOriginMiddleware, which already ran
// for this request with our (broader, tauri://localhost-aware) policy, so
// Accept is told to skip its own same-origin check.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return // Accept already wrote an HTTP error response.
	}

	s.idle.wsOpened()
	defer s.idle.wsClosed()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	events, unsubscribe := engineFrom(r.Context()).Events().Subscribe(ctx)
	defer unsubscribe()

	// The client sends nothing, but frames (pings, close) still need
	// draining so the connection notices when the peer goes away.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := wsjson.Write(ctx, conn, ev); err != nil {
				return
			}
		}
	}
}
