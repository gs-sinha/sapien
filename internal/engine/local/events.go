package local

import (
	"context"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// eventAPI implements engine.EventAPI over Local's bus.
type eventAPI struct{ l *Local }

var _ engine.EventAPI = (*eventAPI)(nil)

func (e *eventAPI) Subscribe(ctx context.Context) (<-chan domain.Event, func()) {
	return e.l.bus.Subscribe(ctx)
}
