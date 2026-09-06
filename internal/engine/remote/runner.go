package remote

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// runOptionsWire is the wire form of engine.RunOptions. Observer is a Go
// func value with no HTTP representation (see the package doc); Remote
// drops it before encoding a request and never reconstructs one on
// responses, since RunFlow/RunFlowSource/Call return only after the run has
// already finished.
type runOptionsWire struct {
	Environment       string         `json:"environment,omitempty"`
	Inputs            map[string]any `json:"inputs,omitempty"`
	ContinueOnFailure bool           `json:"continue_on_failure,omitempty"`
	AllowProduction   bool           `json:"allow_production,omitempty"`
	Trigger           string         `json:"trigger,omitempty"`
	// Resume and partial runs (engine.RunOptions; PLAN §34d).
	ResumeFrom string `json:"resume_from,omitempty"`
	FromStep   string `json:"from_step,omitempty"`
	UntilStep  string `json:"until_step,omitempty"`
}

func wireOpts(opts engine.RunOptions) runOptionsWire {
	return runOptionsWire{
		Environment:       opts.Environment,
		Inputs:            opts.Inputs,
		ContinueOnFailure: opts.ContinueOnFailure,
		AllowProduction:   opts.AllowProduction,
		Trigger:           opts.Trigger,
		ResumeFrom:        opts.ResumeFrom,
		FromStep:          opts.FromStep,
		UntilStep:         opts.UntilStep,
	}
}

// RunFlow maps to POST /v1/flows/{id}/run.
func (r *runnerAPI) RunFlow(ctx context.Context, flowID string, opts engine.RunOptions) (*domain.Run, error) {
	var out domain.Run
	if err := r.r().do(ctx, http.MethodPost, "/v1/flows/"+flowID+"/run", nil, wireOpts(opts), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type runFlowSourceRequest struct {
	YAML string         `json:"yaml"`
	Opts runOptionsWire `json:"opts"`
}

// RunFlowSource maps to POST /v1/runs/source.
func (r *runnerAPI) RunFlowSource(ctx context.Context, yamlSrc string, opts engine.RunOptions) (*domain.Run, error) {
	var out domain.Run
	body := runFlowSourceRequest{YAML: yamlSrc, Opts: wireOpts(opts)}
	if err := r.r().do(ctx, http.MethodPost, "/v1/runs/source", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Call maps to POST /v1/call.
func (r *runnerAPI) Call(ctx context.Context, req engine.CallRequest) (*domain.Run, error) {
	var out domain.Run
	if err := r.r().do(ctx, http.MethodPost, "/v1/call", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Cancel maps to POST /v1/runs/{id}/cancel.
func (r *runnerAPI) Cancel(ctx context.Context, runID string) error {
	return r.r().do(ctx, http.MethodPost, "/v1/runs/"+runID+"/cancel", nil, nil, nil)
}

// List maps to GET /v1/runs?flow=&status=&operation=&limit=&offset=.
func (r *runAPI) List(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	q := url.Values{}
	if filter.FlowID != "" {
		q.Set("flow", filter.FlowID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Operation != "" {
		q.Set("operation", filter.Operation)
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.Offset > 0 {
		q.Set("offset", strconv.Itoa(filter.Offset))
	}
	var out []domain.Run
	if err := r.r().do(ctx, http.MethodGet, "/v1/runs", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Get maps to GET /v1/runs/{id}.
func (r *runAPI) Get(ctx context.Context, id string) (*domain.Run, error) {
	var out domain.Run
	if err := r.r().do(ctx, http.MethodGet, "/v1/runs/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type pinRunRequest struct {
	Pinned bool `json:"pinned"`
}

// Pin maps to POST /v1/runs/{id}/pin.
func (r *runAPI) Pin(ctx context.Context, id string, pinned bool) error {
	return r.r().do(ctx, http.MethodPost, "/v1/runs/"+id+"/pin", nil, pinRunRequest{Pinned: pinned}, nil)
}

type purgeRunsRequest struct {
	Keep int `json:"keep"`
}

type purgeRunsResponse struct {
	Removed int `json:"removed"`
}

// Purge maps to POST /v1/runs/purge.
func (r *runAPI) Purge(ctx context.Context, keep int) (int, error) {
	var out purgeRunsResponse
	if err := r.r().do(ctx, http.MethodPost, "/v1/runs/purge", nil, purgeRunsRequest{Keep: keep}, &out); err != nil {
		return 0, err
	}
	return out.Removed, nil
}

var (
	_ engine.RunnerAPI = (*runnerAPI)(nil)
	_ engine.RunAPI    = (*runAPI)(nil)
)
