package mcp

import (
	"context"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/diagnose"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// This file exercises execute_api's "Might explain it:" wiring: the feedback
// this closes ("Booking returned 400 ... Sapien knew the service graph but
// could not tell me the next place to look") described exactly the
// fixtures/logistics/allocation-service shape -- allocate returns 409
// NO_RIDER_AVAILABLE, and docs/allocation.md plus a memory both explain it.
//
// fakeRunner.Call (fake_engine_test.go) always returns a passing step, so it
// can't produce that run on its own. hintsEngine below wraps *fakeEngine and
// overrides just Runner(), to script a specific failing run without
// restructuring fake_engine_test.go (which this package doesn't own beyond
// appending fixture docs/memories).

// hintsEngine wraps *fakeEngine, embedding it so every method except
// Runner() is unchanged (Catalog, Search, Memories, Envs, ...), and
// overriding Runner() to hand back a scripted run instead of fakeRunner's
// always-passing canned one.
type hintsEngine struct {
	*fakeEngine
	run *domain.Run
}

func (e hintsEngine) Runner() engine.RunnerAPI { return hintsRunner{e.run} }

var _ engine.Engine = hintsEngine{}

// hintsRunner returns a copy of the same scripted run from Call, RunFlow,
// and RunFlowSource alike -- execute_api only needs Call, but this satisfies
// engine.RunnerAPI in full.
type hintsRunner struct{ run *domain.Run }

func (r hintsRunner) Call(context.Context, engine.CallRequest) (*domain.Run, error) {
	cp := *r.run
	return &cp, nil
}
func (r hintsRunner) RunFlow(context.Context, string, engine.RunOptions) (*domain.Run, error) {
	cp := *r.run
	return &cp, nil
}
func (r hintsRunner) RunFlowSource(context.Context, string, engine.RunOptions) (*domain.Run, error) {
	cp := *r.run
	return &cp, nil
}
func (r hintsRunner) Cancel(context.Context, string) error { return nil }

var _ engine.RunnerAPI = hintsRunner{}

// seedNoRiderFixture appends the allocation-service fixture the motivating
// feedback session hit (fixtures/logistics/allocation-service): POST
// /v1/allocations documents 409 for "no eligible rider is currently
// online", docs/allocation.md explains NO_RIDER_AVAILABLE, and a memory
// captures the same gotcha. It appends to st's ops/docs/memories rather
// than replacing anything newFixtureEngine already put there.
func seedNoRiderFixture(st *fakeState) {
	allocate := domain.Operation{
		ID: "allocation-service.allocate", ServiceID: "allocation-service", Protocol: domain.ProtocolHTTP,
		HTTP:    &domain.HTTPBinding{Method: "POST", Path: "/v1/allocations"},
		RawOpID: "allocate", Summary: "Allocate a rider to an order",
		Responses: []domain.Response{
			{Status: "201", Description: "Allocation created"},
			{Status: "409", Description: "No eligible rider is currently online"},
		},
		Hash: "halloc",
	}
	st.ops = append(st.ops, allocate)

	doc := domain.Doc{
		ID: "allocation-service/docs/allocation.md", ServiceID: "allocation-service", Path: "docs/allocation.md",
		Title: "Allocation", Source: domain.DocSourceFile,
		Sections: []domain.DocSection{{
			ID: "allocation-service/docs/allocation.md#no-eligible-rider", Ord: 0,
			Heading: "No eligible rider", Level: 2,
			Body: "If no rider satisfies the matching rule, allocation-service.allocate returns 409 with code NO_RIDER_AVAILABLE.",
			Refs: []domain.DocRef{{Kind: domain.RefOperation, Value: allocate.ID}},
		}},
	}
	st.docs = append(st.docs, doc)

	now := time.Now()
	mem := domain.Memory{
		ID: "mem_no_rider", Type: domain.MemoryGotcha, Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "allocation-service", Operation: allocate.ID},
		Source:  domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Created: now, Updated: now,
		Text: "NO_RIDER_AVAILABLE clears once a rider comes back online or an allocation is released.",
	}
	st.memories = append(st.memories, mem)
}

// noRiderRun is a one-step run for allocation-service.allocate whose
// response is the fixture's 409 NO_RIDER_AVAILABLE body.
func noRiderRun() *domain.Run {
	now := time.Now().UTC()
	finished := now.Add(5 * time.Millisecond)
	return &domain.Run{
		ID: "run_no_rider", Status: domain.RunFailed,
		Started: now, Finished: finished, DurationMs: 5,
		Steps: []domain.StepResult{{
			StepID: "call", Index: 0, Operation: "allocation-service.allocate", Status: domain.StepFailed,
			Response: &domain.ResponseRecord{
				Status: 409,
				Body:   map[string]any{"code": "NO_RIDER_AVAILABLE", "message": "no eligible rider is online for order \"ord_0001\""},
			},
			Started: now, Finished: finished,
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsFailed: 1},
	}
}

// newHintsSession connects an in-memory client to a server backed by the
// fixture engine plus seedNoRiderFixture, whose Runner always returns run.
func newHintsSession(t *testing.T, run *domain.Run) *sdkmcp.ClientSession {
	t.Helper()
	base := newFixtureEngine()
	seedNoRiderFixture(base.st)
	eng := hintsEngine{fakeEngine: base, run: run}

	srv := NewServer(Options{
		Engine:  eng,
		Config:  Config{Default: Permissions{ExecuteRead: true, ExecuteMutation: true, ReadRuns: true}},
		Version: "test", Logger: silentLogger,
	})

	c1, c2 := sdkmcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, c1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "claude-code", Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, c2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestTool_ExecuteAPI_Hints_OnFailure(t *testing.T) {
	cs := newHintsSession(t, noRiderRun())
	res := callTool(t, cs, "execute_api", map[string]any{
		"id": "allocation-service.allocate", "env": "staging",
	})
	require.False(t, res.IsError, firstText(res))

	text := firstText(res)
	assert.Contains(t, text, "Might explain it:")
	assert.Contains(t, text, "contract contract says 409: No eligible rider is currently online")
	assert.Contains(t, text, "allocation-service/docs/allocation.md # No eligible rider")
	assert.Contains(t, text, "(matched: NO_RIDER_AVAILABLE)")
	assert.Contains(t, text, `sapien docs show allocation-service docs/allocation.md --section "No eligible rider"`)
	assert.Contains(t, text, "mem_no_rider")
	assert.Contains(t, text, "sapien memory show mem_no_rider")

	out := decodeStructured[RunViewWithHints](t, res.StructuredContent)
	require.NotEmpty(t, out.Hints)

	var sawContract, sawDoc, sawMemory bool
	for _, h := range out.Hints {
		switch h.Kind {
		case diagnose.KindContract:
			sawContract = true
			assert.Equal(t, "contract says 409: No eligible rider is currently online", h.Title)
		case diagnose.KindDoc:
			sawDoc = true
			assert.Equal(t, "NO_RIDER_AVAILABLE", h.MatchedOn)
			assert.Equal(t, "docs/allocation.md", h.Ref.Path)
		case diagnose.KindMemory:
			sawMemory = true
			assert.Equal(t, "mem_no_rider", h.Ref.MemoryID)
		}
	}
	assert.True(t, sawContract, "expected a contract hint")
	assert.True(t, sawDoc, "expected a doc hint")
	assert.True(t, sawMemory, "expected a memory hint")
}

func TestTool_ExecuteAPI_NoHints_WhenPassed(t *testing.T) {
	now := time.Now().UTC()
	passed := &domain.Run{
		ID: "run_ok", Status: domain.RunPassed, Started: now, Finished: now,
		Steps: []domain.StepResult{{
			StepID: "call", Operation: "rider-service.getRider", Status: domain.StepPassed,
			Response: &domain.ResponseRecord{Status: 200, Body: map[string]any{"riderId": "r1"}},
			Started:  now, Finished: now,
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1},
	}
	cs := newHintsSession(t, passed)
	res := callTool(t, cs, "execute_api", map[string]any{
		"id": "rider-service.getRider", "env": "staging",
	})
	require.False(t, res.IsError, firstText(res))

	text := firstText(res)
	assert.NotContains(t, text, "Might explain it:")

	out := decodeStructured[RunViewWithHints](t, res.StructuredContent)
	assert.Empty(t, out.Hints)
}

func TestTool_ExecuteAPI_Hints_ErroredStepWithoutResponseYieldsNoHints(t *testing.T) {
	now := time.Now().UTC()
	transportErr := &domain.Run{
		ID: "run_transport_err", Status: domain.RunErrored, Started: now, Finished: now,
		Error: &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "connection refused"},
		Steps: []domain.StepResult{{
			StepID: "call", Operation: "allocation-service.allocate", Status: domain.StepErrored,
			Error:   &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "connection refused"},
			Started: now, Finished: now,
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsErrored: 1},
	}
	cs := newHintsSession(t, transportErr)
	res := callTool(t, cs, "execute_api", map[string]any{
		"id": "allocation-service.allocate", "env": "staging",
	})
	require.False(t, res.IsError, firstText(res))
	assert.NotContains(t, firstText(res), "Might explain it:")

	out := decodeStructured[RunViewWithHints](t, res.StructuredContent)
	assert.Empty(t, out.Hints)
}

// --- appendHintsText / hintOpenCommand, exercised directly ---

func TestAppendHintsText_EmptyHintsLeavesTextUnchanged(t *testing.T) {
	assert.Equal(t, "run x: passed\n", appendHintsText("run x: passed\n", nil))
}

func TestAppendHintsText_AddsTrailingNewlineBeforeBlock(t *testing.T) {
	got := appendHintsText("run x: failed", []diagnose.Hint{{Kind: diagnose.KindContract, Title: "contract says 409"}})
	assert.Contains(t, got, "run x: failed\n\nMight explain it:\n")
}

func TestHintOpenCommand(t *testing.T) {
	cases := []struct {
		name string
		hint diagnose.Hint
		want string
	}{
		{
			name: "doc with section",
			hint: diagnose.Hint{Kind: diagnose.KindDoc, Ref: diagnose.Ref{Service: "s", Path: "docs/x.md", Section: "Heading"}},
			want: `sapien docs show s docs/x.md --section "Heading"`,
		},
		{
			name: "doc without section",
			hint: diagnose.Hint{Kind: diagnose.KindDoc, Ref: diagnose.Ref{Service: "s", Path: "docs/x.md"}},
			want: "sapien docs show s docs/x.md",
		},
		{
			name: "memory",
			hint: diagnose.Hint{Kind: diagnose.KindMemory, Ref: diagnose.Ref{MemoryID: "mem_1"}},
			want: "sapien memory show mem_1",
		},
		{
			name: "contract has nothing further to open",
			hint: diagnose.Hint{Kind: diagnose.KindContract},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hintOpenCommand(tc.hint))
		})
	}
}

func TestHintsFor_NilRun(t *testing.T) {
	assert.Nil(t, hintsFor(context.Background(), nil, nil))
}
