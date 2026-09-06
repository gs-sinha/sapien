package server

import (
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// runOptionsWire is the JSON wire form of engine.RunOptions.
//
// engine.RunOptions carries an Observer func(domain.Event) field. Go's
// encoding/json always rejects the Func kind -- even a nil func value --
// because the encoder chosen for a struct field is picked from its static
// type, not its runtime value, so marshaling engine.RunOptions directly
// panics/errors regardless of whether Observer is set. Observer also has no
// meaningful HTTP representation: it is an in-process callback. This wire
// type carries every other field; the server reconstructs an
// engine.RunOptions with Observer always nil. Callers that want progress
// notifications must subscribe to GET /v1/events instead (see
// internal/engine/remote's package doc).
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

func (w runOptionsWire) toEngine() engine.RunOptions {
	return engine.RunOptions{
		Environment:       w.Environment,
		Inputs:            w.Inputs,
		ContinueOnFailure: w.ContinueOnFailure,
		AllowProduction:   w.AllowProduction,
		Trigger:           w.Trigger,
		ResumeFrom:        w.ResumeFrom,
		FromStep:          w.FromStep,
		UntilStep:         w.UntilStep,
	}
}

type addServiceRequest struct {
	Name   string        `json:"name"`
	Source domain.Source `json:"source"`
}

type flowYAMLRequest struct {
	YAML string `json:"yaml"`
	Path string `json:"path,omitempty"`
}

type runFlowSourceRequest struct {
	YAML string         `json:"yaml"`
	Opts runOptionsWire `json:"opts"`
}

type pinRunRequest struct {
	Pinned bool `json:"pinned"`
}

type purgeRunsRequest struct {
	Keep int `json:"keep"`
}

type purgeRunsResponse struct {
	Removed int `json:"removed"`
}

type relevantMemoriesRequest struct {
	Subjects []domain.Subject `json:"subjects"`
	Limit    int              `json:"limit,omitempty"`
}

type defaultEnvironmentRequest struct {
	Name string `json:"name"`
}

type defaultEnvironmentResponse struct {
	Name string `json:"name"`
}

type setSecretRequest struct {
	Value string `json:"value"`
}

// registerWorkspaceRequest is POST /v1/workspaces' body.
type registerWorkspaceRequest struct {
	Dir string `json:"dir"`
}
