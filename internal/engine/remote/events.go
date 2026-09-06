package remote

import (
	"context"
	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// Subscribe maps to GET /v1/events: it dials the WebSocket and forwards
// decoded domain.Event messages to the returned channel until ctx is
// cancelled, the cancel func is called, or the connection drops for any
// other reason, at which point the channel closes.
func (e *eventAPI) Subscribe(ctx context.Context) (<-chan domain.Event, func()) {
	r := e.r()
	subCtx, cancel := context.WithCancel(ctx)
	out := make(chan domain.Event)

	go func() {
		defer close(out)

		u := r.currentBase()
		u.Path = "/v1/events"
		conn, _, err := websocket.Dial(subCtx, u.String(), &websocket.DialOptions{
			HTTPClient: r.client,
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + r.currentToken()}},
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for {
			var ev domain.Event
			if err := wsjson.Read(subCtx, conn, &ev); err != nil {
				return
			}
			select {
			case out <- ev:
			case <-subCtx.Done():
				return
			}
		}
	}()

	return out, cancel
}

var _ engine.EventAPI = (*eventAPI)(nil)
