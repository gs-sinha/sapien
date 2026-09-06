package local

import (
	"context"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// purgeMaxAge mirrors PLAN §9's retention default: unpinned runs older than
// 30 days are purged alongside whatever the count-based `keep` rule drops.
const purgeMaxAge = 30 * 24 * time.Hour

// runAPI implements engine.RunAPI as a pass-through to internal/runs.
type runAPI struct{ l *Local }

var _ engine.RunAPI = (*runAPI)(nil)

func (r *runAPI) List(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	return r.l.runsStore.List(ctx, filter)
}

func (r *runAPI) Get(ctx context.Context, id string) (*domain.Run, error) {
	return r.l.runsStore.Get(ctx, id)
}

func (r *runAPI) Pin(ctx context.Context, id string, pinned bool) error {
	return r.l.runsStore.Pin(ctx, id, pinned)
}

func (r *runAPI) Purge(ctx context.Context, keep int) (int, error) {
	return r.l.runsStore.Purge(ctx, keep, purgeMaxAge)
}
