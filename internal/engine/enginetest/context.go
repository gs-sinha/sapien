package enginetest

import (
	"context"
	"sort"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

func estimateTokens(b *domain.ContextBundle) int {
	n := len(b.Intent)
	for _, op := range b.Operations {
		n += len(op.Summary) + len(op.Description) + 20
	}
	for _, d := range b.Docs {
		n += len(d.Body)
	}
	for _, mem := range b.Memories {
		n += len(mem.Text)
	}
	return n / 4
}

// Build assembles a deterministic bundle: every requested operation (or, if
// none were requested, every known operation), every doc section, every
// memory, and the requested flow's steps. It is not a relevance model.
func (c *contextAPI) Build(ctx context.Context, req domain.ContextRequest) (*domain.ContextBundle, error) {
	f := c.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Context.Build", req)

	bundle := &domain.ContextBundle{Intent: req.Intent}

	ids := req.Operations
	if len(ids) == 0 {
		for id := range f.operations {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	for _, id := range ids {
		op, ok := f.operations[id]
		if !ok {
			continue
		}
		var method, path string
		if op.HTTP != nil {
			method, path = op.HTTP.Method, op.HTTP.Path
		}
		bundle.Operations = append(bundle.Operations, domain.OperationContext{
			Tier:        domain.TierContract,
			ID:          op.ID,
			Method:      method,
			Path:        path,
			Summary:     op.Summary,
			Description: op.Description,
		})
	}

	for _, d := range f.docs {
		for _, sec := range d.Sections {
			bundle.Docs = append(bundle.Docs, domain.DocContext{
				Tier:    domain.TierDocumentation,
				Service: d.ServiceID,
				Path:    d.Path,
				Heading: sec.Heading,
				Body:    sec.Body,
				URI:     "sapien://services/" + d.ServiceID + "/docs/" + d.Path + "#" + sec.ID,
			})
		}
	}
	sort.Slice(bundle.Docs, func(i, j int) bool { return bundle.Docs[i].Path < bundle.Docs[j].Path })

	for _, mem := range f.memories {
		bundle.Memories = append(bundle.Memories, domain.MemoryContext{
			Tier:    domain.TierMemory,
			ID:      mem.ID,
			Type:    mem.Type,
			Scope:   mem.Scope,
			Source:  string(mem.Source.Kind),
			Subject: mem.Subject,
			Text:    mem.Text,
		})
	}
	sort.Slice(bundle.Memories, func(i, j int) bool { return bundle.Memories[i].ID < bundle.Memories[j].ID })

	if req.Flow != "" {
		if flow, ok := f.flows[req.Flow]; ok {
			steps := make([]string, 0, len(flow.Steps))
			for _, st := range flow.Steps {
				steps = append(steps, st.ID+": "+st.Call)
			}
			bundle.Flows = append(bundle.Flows, domain.FlowContext{ID: flow.ID, Name: flow.Name, Steps: steps})
		}
	}

	bundle.EstimatedTokens = estimateTokens(bundle)
	return bundle, nil
}

var _ engine.ContextAPI = (*contextAPI)(nil)
