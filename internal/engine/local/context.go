package local

import (
	"context"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// contextAPI implements engine.ContextAPI as a pass-through to
// internal/retrieval's Builder (PLAN §14).
type contextAPI struct{ l *Local }

var _ engine.ContextAPI = (*contextAPI)(nil)

// Build wires l.Examples() into the Builder's examples tier (PLAN §14/§34b)
// before delegating. l.ctxBuilder is constructed once, in Open, before the
// examples tier existed on retrieval.New's signature; rather than change
// that signature (and every other caller of it), Builder exposes a
// SetExamples setter that this re-applies on every call. That is cheap
// (engine.ExampleAPI wrappers here and in examples.go are themselves
// stateless per-call views over *Local) and keeps the example source
// current even if it ever became swappable; SetExamples is safe under
// concurrent Build calls. l.Examples() is never nil, but Builder treats a
// nil ExampleSource as "no examples" regardless, so this stays correct even
// if that ever changed.
func (c *contextAPI) Build(ctx context.Context, req domain.ContextRequest) (*domain.ContextBundle, error) {
	c.l.ctxBuilder.SetExamples(c.l.Examples())
	return c.l.ctxBuilder.Build(ctx, req)
}
