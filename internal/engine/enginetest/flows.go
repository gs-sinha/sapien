package enginetest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

const (
	flowReferenceText        = "# Flow DSL reference\n\nA flow is a YAML document with `version`, `steps`, and optional `inputs`.\nEach step has an `id` and a `call` (an operation ID), plus optional `input`,\n`extract`, `assert`, and `until`.\n"
	memoryReferenceText      = "# Memory reference\n\nA memory captures one fact: subject, type, scope, and a Markdown body.\n"
	expressionsReferenceText = "# Expression reference\n\nExpressions are CEL, evaluated against `request`, `response`, and prior steps'\n`out` values.\n"
)

// parseFlow unmarshals yamlSrc into a domain.Flow using the struct's yaml
// tags. It does not validate references.
func parseFlow(yamlSrc string) (*domain.Flow, error) {
	var flow domain.Flow
	if err := yaml.Unmarshal([]byte(yamlSrc), &flow); err != nil {
		return nil, errs.Wrap(errs.FlowInvalid, err, "parsing flow yaml")
	}
	flow.Source = yamlSrc
	return &flow, nil
}

func (fl *flowAPI) Parse(ctx context.Context, yamlSrc string) (*domain.Flow, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Parse", yamlSrc)
	return parseFlow(yamlSrc)
}

func (fl *flowAPI) Validate(ctx context.Context, yamlSrc string) (*domain.ValidationResult, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Validate", yamlSrc)

	flow, err := parseFlow(yamlSrc)
	if err != nil {
		return &domain.ValidationResult{Valid: false, Diagnostics: []domain.Diagnostic{{
			Code:     "E_FLOW_INVALID",
			Severity: domain.SeverityError,
			Message:  err.Error(),
		}}}, nil
	}

	var diags []domain.Diagnostic
	for _, step := range flow.Steps {
		if _, rerr := f.resolveOperationLocked(step.Call); rerr != nil {
			diags = append(diags, domain.Diagnostic{
				Code:     "E_FLOW_INVALID",
				Severity: domain.SeverityError,
				Message:  fmt.Sprintf("step %q calls unknown operation %q", step.ID, step.Call),
				StepID:   step.ID,
				Line:     step.Line,
			})
		}
	}
	return &domain.ValidationResult{Valid: len(diags) == 0, Diagnostics: diags}, nil
}

func (fl *flowAPI) Create(ctx context.Context, yamlSrc string, path string) (*domain.Flow, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Create", map[string]string{"yaml": yamlSrc, "path": path})

	flow, err := parseFlow(yamlSrc)
	if err != nil {
		return nil, err
	}
	if flow.ID == "" {
		flow.ID = stemOf(path)
	}
	if _, exists := f.flows[flow.ID]; exists {
		return nil, errs.New(errs.Conflict, "flow %q already exists", flow.ID).WithDetail("id", flow.ID)
	}
	flow.Path = path
	flow.OwnerKind = "workspace"

	f.flows[flow.ID] = *flow
	f.flowUpdated[flow.ID] = time.Now().UTC()
	f.events.publish(domain.Event{Type: domain.EventFlowChanged, Time: time.Now().UTC(), Payload: flow.ID})

	cp := *flow
	return &cp, nil
}

func (fl *flowAPI) Update(ctx context.Context, id string, yamlSrc string) (*domain.Flow, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Update", map[string]string{"id": id, "yaml": yamlSrc})

	existing, ok := f.flows[id]
	if !ok {
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", id).WithDetail("id", id)
	}
	flow, err := parseFlow(yamlSrc)
	if err != nil {
		return nil, err
	}
	flow.ID = id
	flow.Path = existing.Path
	flow.OwnerKind = existing.OwnerKind
	flow.OwnerID = existing.OwnerID

	f.flows[id] = *flow
	f.flowUpdated[id] = time.Now().UTC()
	f.events.publish(domain.Event{Type: domain.EventFlowChanged, Time: time.Now().UTC(), Payload: id})

	cp := *flow
	return &cp, nil
}

func (fl *flowAPI) Delete(ctx context.Context, id string) error {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Delete", id)

	if _, ok := f.flows[id]; !ok {
		return errs.New(errs.FlowNotFound, "flow %q not found", id).WithDetail("id", id)
	}
	delete(f.flows, id)
	delete(f.flowUpdated, id)
	f.events.publish(domain.Event{Type: domain.EventFlowChanged, Time: time.Now().UTC(), Payload: id})
	return nil
}

func (fl *flowAPI) Reference(ctx context.Context, topic string) (string, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Reference", topic)

	switch topic {
	case "flow":
		return flowReferenceText, nil
	case "memory":
		return memoryReferenceText, nil
	case "expressions":
		return expressionsReferenceText, nil
	default:
		return "", errs.New(errs.Invalid, "unknown reference topic %q", topic).WithDetail("topic", topic)
	}
}

func (fl *flowAPI) List(ctx context.Context, query string) ([]domain.FlowSummary, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.List", query)

	q := strings.ToLower(strings.TrimSpace(query))
	var out []domain.FlowSummary
	for _, flow := range f.flows {
		if q != "" && !strings.Contains(strings.ToLower(flow.ID+" "+flow.Name), q) {
			continue
		}
		ops := make([]string, 0, len(flow.Steps))
		for _, st := range flow.Steps {
			ops = append(ops, st.Call)
		}
		out = append(out, domain.FlowSummary{
			ID:         flow.ID,
			Name:       flow.Name,
			Path:       flow.Path,
			OwnerKind:  flow.OwnerKind,
			OwnerID:    flow.OwnerID,
			Tags:       flow.Tags,
			Operations: ops,
			StepCount:  len(flow.Steps),
			Hash:       hashOf(flow.Source),
			Updated:    f.flowUpdated[flow.ID],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (fl *flowAPI) Get(ctx context.Context, id string) (*domain.Flow, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Get", id)

	flow, ok := f.flows[id]
	if !ok {
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", id).WithDetail("id", id)
	}
	cp := flow
	return &cp, nil
}

var _ engine.FlowAPI = (*flowAPI)(nil)

// CreateIn is Create with the owner recorded: the fake has no tiers on
// disk, so the owner is simply stamped on the stored flow.
func (fl *flowAPI) CreateIn(ctx context.Context, yamlSrc string, opts engine.CreateFlowOptions) (*domain.Flow, error) {
	kind := opts.OwnerKind
	if kind == "" {
		kind = domain.FlowOwnerLocal
	}
	created, err := fl.Create(ctx, yamlSrc, opts.Path)
	if err != nil {
		return nil, err
	}
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.CreateIn", map[string]string{"id": created.ID, "owner_kind": kind, "owner_id": opts.OwnerID})
	stored := f.flows[created.ID]
	stored.OwnerKind, stored.OwnerID = kind, opts.OwnerID
	f.flows[created.ID] = stored
	cp := stored
	return &cp, nil
}

// Rescope re-stamps the owner on a stored flow.
func (fl *flowAPI) Rescope(ctx context.Context, id string, ownerKind, ownerID string) (*domain.Flow, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Rescope", map[string]string{"id": id, "owner_kind": ownerKind, "owner_id": ownerID})

	stored, ok := f.flows[id]
	if !ok {
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", id).WithDetail("id", id)
	}
	stored.OwnerKind, stored.OwnerID = ownerKind, ownerID
	f.flows[id] = stored
	f.flowUpdated[id] = time.Now().UTC()
	f.events.publish(domain.Event{Type: domain.EventFlowChanged, Time: time.Now().UTC(), Payload: id})
	cp := stored
	return &cp, nil
}

// RescopeWith is Rescope for the fake; a commit is recorded, not made.
func (fl *flowAPI) RescopeWith(ctx context.Context, id string, ownerKind, ownerID string, opts engine.RescopeOptions) (*domain.Flow, error) {
	f := fl.f()
	f.mu.Lock()
	f.recordLocked("Flows.RescopeWith", map[string]any{"id": id, "owner_kind": ownerKind, "owner_id": ownerID, "commit": opts.Commit, "message": opts.Message})
	f.mu.Unlock()
	return fl.Rescope(ctx, id, ownerKind, ownerID)
}

// Commit marks the stored flow's summary as unpushed: the fake has no
// repository, so a commit is a recorded call plus the state change a
// caller would observe.
func (fl *flowAPI) Commit(ctx context.Context, id, message string) (*domain.FlowSummary, error) {
	f := fl.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Flows.Commit", map[string]string{"id": id, "message": message})
	stored, ok := f.flows[id]
	if !ok {
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", id).WithDetail("id", id)
	}
	if stored.OwnerKind != "" && stored.OwnerKind != domain.FlowOwnerWorkspace {
		return nil, errs.New(errs.Invalid, "only a workspace-tier flow can be committed; %q is %s", id, stored.OwnerKind)
	}
	ops := make([]string, 0, len(stored.Steps))
	for _, st := range stored.Steps {
		ops = append(ops, st.Call)
	}
	return &domain.FlowSummary{
		ID: stored.ID, Name: stored.Name, Path: stored.Path,
		OwnerKind: stored.OwnerKind, OwnerID: stored.OwnerID, Tags: stored.Tags,
		Operations: ops, StepCount: len(stored.Steps), Hash: hashOf(stored.Source),
		Updated: f.flowUpdated[stored.ID], Shipped: domain.ShipUnpushed,
	}, nil
}
