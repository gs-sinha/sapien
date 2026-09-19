package diagnose

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// --- a minimal engine.Engine stub -----------------------------------------
//
// diagnose only ever calls Catalog().GetOperation, Search().Docs, and
// Memories().Search, so every other sub-API can safely be nil: returning a
// nil engine.XxxAPI is a valid interface value, and diagnose never invokes a
// method on it.

type stubEngine struct {
	getOperation func(ctx context.Context, id string) (*domain.Operation, error)
	searchDocs   func(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error)
	memSearch    func(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error)

	docsCalls []docsCall
	memCalls  []domain.MemoryQuery
}

type docsCall struct {
	query string
	opts  domain.SearchOptions
}

func (s *stubEngine) Workspace() *domain.Workspace { return nil }
func (s *stubEngine) Services() engine.ServiceAPI  { return nil }
func (s *stubEngine) Catalog() engine.CatalogAPI   { return stubCatalog{s} }
func (s *stubEngine) Search() engine.SearchAPI     { return stubSearch{s} }
func (s *stubEngine) Flows() engine.FlowAPI        { return nil }
func (s *stubEngine) Runner() engine.RunnerAPI     { return nil }
func (s *stubEngine) Runs() engine.RunAPI          { return nil }
func (s *stubEngine) Memories() engine.MemoryAPI   { return stubMemories{s} }
func (s *stubEngine) Examples() engine.ExampleAPI  { return nil }
func (s *stubEngine) Context() engine.ContextAPI   { return nil }
func (s *stubEngine) Envs() engine.EnvAPI          { return nil }
func (s *stubEngine) Events() engine.EventAPI      { return nil }
func (s *stubEngine) Repo() engine.RepoAPI         { return nil }
func (s *stubEngine) Settings() engine.SettingsAPI { return nil }
func (s *stubEngine) Close() error                 { return nil }

var _ engine.Engine = (*stubEngine)(nil)

type stubCatalog struct{ s *stubEngine }

func (c stubCatalog) GetOperation(ctx context.Context, id string) (*domain.Operation, error) {
	if c.s.getOperation == nil {
		return nil, errors.New("stub: no operation")
	}
	return c.s.getOperation(ctx, id)
}
func (c stubCatalog) ResolveOperation(context.Context, string) (*domain.Operation, error) {
	return nil, errors.New("stub: not implemented")
}
func (c stubCatalog) ListOperations(context.Context, string) ([]domain.Operation, error) {
	return nil, nil
}
func (c stubCatalog) Fields(context.Context, string) ([]domain.Field, error) { return nil, nil }
func (c stubCatalog) GetSchema(context.Context, string, string) (*domain.NamedSchema, error) {
	return nil, nil
}
func (c stubCatalog) ListDocs(context.Context, string) ([]domain.Doc, error)      { return nil, nil }
func (c stubCatalog) GetDoc(context.Context, string, string) (*domain.Doc, error) { return nil, nil }

var _ engine.CatalogAPI = stubCatalog{}

type stubSearch struct{ s *stubEngine }

func (s stubSearch) Operations(context.Context, string, domain.SearchOptions) ([]domain.SearchResult, error) {
	return nil, nil
}
func (s stubSearch) Docs(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
	s.s.docsCalls = append(s.s.docsCalls, docsCall{query: query, opts: opts})
	if s.s.searchDocs == nil {
		return nil, nil
	}
	return s.s.searchDocs(ctx, query, opts)
}

var _ engine.SearchAPI = stubSearch{}

type stubMemories struct{ s *stubEngine }

func (m stubMemories) Create(context.Context, domain.Memory) (*domain.Memory, error) { return nil, nil }
func (m stubMemories) Get(context.Context, string) (*domain.Memory, error)           { return nil, nil }
func (m stubMemories) Update(context.Context, domain.Memory) (*domain.Memory, error) { return nil, nil }
func (m stubMemories) Delete(context.Context, string) error                          { return nil }
func (m stubMemories) List(context.Context, domain.MemoryQuery) ([]domain.Memory, error) {
	return nil, nil
}
func (m stubMemories) Search(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
	m.s.memCalls = append(m.s.memCalls, q)
	if m.s.memSearch == nil {
		return nil, nil
	}
	return m.s.memSearch(ctx, q)
}
func (m stubMemories) Relevant(context.Context, []domain.Subject, int) ([]domain.ScoredMemory, error) {
	return nil, nil
}
func (m stubMemories) PromotionTarget(context.Context, string) (*engine.PromotionTarget, error) {
	return nil, nil
}
func (m stubMemories) Reindex(context.Context) error { return nil }

var _ engine.MemoryAPI = stubMemories{}

// --- fixtures --------------------------------------------------------------

// allocateOp mirrors fixtures/logistics/allocation-service: POST
// /v1/allocations returns 409 NO_RIDER_AVAILABLE when no eligible rider is
// online, and the contract documents that response.
func allocateOp() domain.Operation {
	return domain.Operation{
		ID:        "allocation-service.allocate",
		ServiceID: "allocation-service",
		HTTP:      &domain.HTTPBinding{Method: "POST", Path: "/v1/allocations"},
		Responses: []domain.Response{
			{Status: "201", Description: "Allocation created"},
			{Status: "409", Description: "No eligible rider is currently online"},
		},
	}
}

func noRiderStep() domain.StepResult {
	return domain.StepResult{
		StepID:    "allocate",
		Operation: "allocation-service.allocate",
		Status:    domain.StepFailed,
		Response: &domain.ResponseRecord{
			Status: 409,
			Body:   map[string]any{"code": "NO_RIDER_AVAILABLE", "message": "no eligible rider is online for order \"ord_0001\""},
		},
	}
}

func noRiderDoc() domain.DocSearchResult {
	return domain.DocSearchResult{
		Service: "allocation-service", Path: "docs/allocation.md",
		Heading: "No eligible rider", Score: 2.0,
	}
}

func noRiderMemory() domain.ScoredMemory {
	return domain.ScoredMemory{
		Memory: domain.Memory{ID: "mem_1", Text: "NO_RIDER_AVAILABLE clears once a rider comes back online.\nSee allocation.md."},
		Score:  1.5,
	}
}

// --- extractTokens -----------------------------------------------------

func TestExtractTokens(t *testing.T) {
	cases := []struct {
		name string
		body any
		want []string
	}{
		{
			name: "flat code and message",
			body: map[string]any{"code": "NO_RIDER_AVAILABLE", "message": "no eligible rider is online"},
			want: []string{"NO_RIDER_AVAILABLE", "no eligible rider is online"},
		},
		{
			name: "nested error object recurses one level",
			body: map[string]any{"error": map[string]any{"code": "XXX", "message": "YYY"}},
			want: []string{"XXX", "YYY"},
		},
		{
			name: "array of objects under details recurses one level",
			body: map[string]any{"details": []any{map[string]any{"code": "AAA"}, map[string]any{"reason": "BBB"}}},
			want: []string{"AAA", "BBB"},
		},
		{
			name: "array of plain strings",
			body: map[string]any{"details": []any{"first", "second"}},
			want: []string{"first", "second"},
		},
		{
			name: "short tokens dropped",
			body: map[string]any{"code": "NO", "type": "ab"},
			want: nil,
		},
		{
			name: "long token truncated to 120 chars",
			body: map[string]any{"code": stringsRepeat("x", 200)},
			want: []string{stringsRepeat("x", 120)},
		},
		{
			name: "duplicate tokens deduped, order preserved",
			body: map[string]any{"code": "DUP", "error_code": "DUP", "reason": "unique"},
			want: []string{"DUP", "unique"},
		},
		{
			name: "non-object body yields nothing",
			body: "plain text",
			want: nil,
		},
		{
			name: "nil body yields nothing",
			body: nil,
			want: nil,
		},
		{
			name: "unrelated keys ignored",
			body: map[string]any{"riderId": "r1", "status": "ok"},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTokens(tc.body)
			assert.Equal(t, tc.want, got)
		})
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, s...)
	}
	return string(out[:n])
}

// --- contractHint --------------------------------------------------------

func TestContractHint(t *testing.T) {
	t.Run("documented status returns a hint", func(t *testing.T) {
		h := contractHint(noRiderStep(), allocateOp())
		require.NotNil(t, h)
		assert.Equal(t, KindContract, h.Kind)
		assert.Equal(t, "allocate", h.StepID)
		assert.Equal(t, "contract says 409: No eligible rider is currently online", h.Title)
		assert.Equal(t, float64(contractScore), h.Score)
	})

	t.Run("undocumented status returns nil", func(t *testing.T) {
		st := noRiderStep()
		st.Response = &domain.ResponseRecord{Status: 503}
		h := contractHint(st, allocateOp())
		assert.Nil(t, h)
	})

	t.Run("documented status without a description omits the trailing colon", func(t *testing.T) {
		op := domain.Operation{Responses: []domain.Response{{Status: "409"}}}
		h := contractHint(noRiderStep(), op)
		require.NotNil(t, h)
		assert.Equal(t, "contract says 409", h.Title)
	})
}

// --- dedupeHints ---------------------------------------------------------

func TestDedupeHints(t *testing.T) {
	in := []Hint{
		{Kind: KindDoc, Ref: Ref{Service: "s", Path: "p", Section: "h"}, Score: 1},
		{Kind: KindDoc, Ref: Ref{Service: "s", Path: "p", Section: "h"}, Score: 2}, // same target, higher score wins
		{Kind: KindMemory, Ref: Ref{MemoryID: "m1"}, Score: 5},
		{Kind: KindMemory, Ref: Ref{MemoryID: "m2"}, Score: 1},
	}
	out := dedupeHints(in)
	require.Len(t, out, 3)
	assert.Equal(t, float64(2), out[0].Score) // the higher-scoring doc hint survived, in first-seen position
	assert.Equal(t, "m1", out[1].Ref.MemoryID)
	assert.Equal(t, "m2", out[2].Ref.MemoryID)
}

// --- Run / RunStep ---------------------------------------------------------

func TestRun_NilRunOrEngine(t *testing.T) {
	assert.Nil(t, Run(context.Background(), &stubEngine{}, nil))
	assert.Nil(t, Run(context.Background(), nil, &domain.Run{}))
}

func TestRun_PassedRunProducesNoHintsAndNoSearches(t *testing.T) {
	eng := &stubEngine{}
	run := &domain.Run{Steps: []domain.StepResult{{StepID: "ok", Status: domain.StepPassed}}}
	hints := Run(context.Background(), eng, run)
	assert.Empty(t, hints)
	assert.Empty(t, eng.docsCalls)
	assert.Empty(t, eng.memCalls)
}

func TestRun_FailedStepWithoutResponseProducesNoHints(t *testing.T) {
	eng := &stubEngine{}
	run := &domain.Run{Steps: []domain.StepResult{{
		StepID: "boom", Status: domain.StepErrored,
		Error: &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "connection refused"},
	}}}
	hints := Run(context.Background(), eng, run)
	assert.Empty(t, hints)
	assert.Empty(t, eng.docsCalls)
}

func TestRun_FailedStepWithSubBoundaryResponseProducesNoHints(t *testing.T) {
	eng := &stubEngine{}
	run := &domain.Run{Steps: []domain.StepResult{{
		StepID: "notfailed", Status: domain.StepFailed,
		Response: &domain.ResponseRecord{Status: 399},
	}}}
	hints := Run(context.Background(), eng, run)
	assert.Empty(t, hints)
}

func TestRun_NoRiderAvailable_ContractAndDocAndMemoryHints(t *testing.T) {
	op := allocateOp()
	eng := &stubEngine{
		getOperation: func(ctx context.Context, id string) (*domain.Operation, error) {
			assert.Equal(t, "allocation-service.allocate", id)
			return &op, nil
		},
		searchDocs: func(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
			assert.Equal(t, "allocation-service", opts.Service)
			if query == "NO_RIDER_AVAILABLE" {
				return []domain.DocSearchResult{noRiderDoc()}, nil
			}
			return nil, nil
		},
		memSearch: func(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
			assert.Equal(t, "allocation-service", q.Service)
			if q.Text == "NO_RIDER_AVAILABLE" {
				return []domain.ScoredMemory{noRiderMemory()}, nil
			}
			return nil, nil
		},
	}
	run := &domain.Run{Steps: []domain.StepResult{noRiderStep()}}

	hints := Run(context.Background(), eng, run)
	require.Len(t, hints, 3)

	assert.Equal(t, KindContract, hints[0].Kind)
	assert.Equal(t, "contract says 409: No eligible rider is currently online", hints[0].Title)

	kinds := map[Kind]Hint{}
	for _, h := range hints[1:] {
		kinds[h.Kind] = h
	}
	doc, ok := kinds[KindDoc]
	require.True(t, ok)
	assert.Equal(t, "allocation-service/docs/allocation.md # No eligible rider", doc.Title)
	assert.Equal(t, "NO_RIDER_AVAILABLE", doc.MatchedOn)
	assert.Equal(t, Ref{Service: "allocation-service", Path: "docs/allocation.md", Section: "No eligible rider"}, doc.Ref)

	mem, ok := kinds[KindMemory]
	require.True(t, ok)
	assert.Equal(t, "mem_1: NO_RIDER_AVAILABLE clears once a rider comes back online.", mem.Title)
	assert.Equal(t, Ref{MemoryID: "mem_1"}, mem.Ref)
}

func TestRun_CatalogLookupErrorSkipsContractButKeepsSearching(t *testing.T) {
	eng := &stubEngine{
		getOperation: func(ctx context.Context, id string) (*domain.Operation, error) {
			return nil, errors.New("operation not found")
		},
		searchDocs: func(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
			assert.Equal(t, "", opts.Service) // unresolved operation: no service to scope by
			return []domain.DocSearchResult{noRiderDoc()}, nil
		},
	}
	run := &domain.Run{Steps: []domain.StepResult{noRiderStep()}}

	hints := Run(context.Background(), eng, run)
	require.Len(t, hints, 1)
	assert.Equal(t, KindDoc, hints[0].Kind)
}

func TestRun_SearchErrorsAreSkippedNotFatal(t *testing.T) {
	eng := &stubEngine{
		getOperation: func(ctx context.Context, id string) (*domain.Operation, error) {
			op := allocateOp()
			return &op, nil
		},
		searchDocs: func(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
			return nil, errors.New("search backend down")
		},
		memSearch: func(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
			return nil, errors.New("memory backend down")
		},
	}
	run := &domain.Run{Steps: []domain.StepResult{noRiderStep()}}

	hints := Run(context.Background(), eng, run)
	require.Len(t, hints, 1) // just the contract hint; search errors produced nothing but didn't panic/abort
	assert.Equal(t, KindContract, hints[0].Kind)
}

// TestRun_HintsCappedAtFivePerStep gives one step's searches ten distinct
// doc hits (all for the same single token) and checks only the top 5
// (by score) survive.
func TestRun_HintsCappedAtFivePerStep(t *testing.T) {
	st := domain.StepResult{
		StepID: "s1", Operation: "op1", Status: domain.StepFailed,
		Response: &domain.ResponseRecord{Status: 500, Body: map[string]any{"code": "SOME_ERROR"}},
	}
	var docs []domain.DocSearchResult
	for i := 0; i < 10; i++ {
		docs = append(docs, domain.DocSearchResult{
			Service: "svc", Path: "docs/x.md", Heading: "h", Score: float64(i),
		})
	}
	// Give each a distinct target so dedupe doesn't collapse them.
	for i := range docs {
		docs[i].Heading = "heading-" + string(rune('a'+i))
	}
	eng := &stubEngine{
		getOperation: func(ctx context.Context, id string) (*domain.Operation, error) {
			return nil, errors.New("no operation")
		},
		searchDocs: func(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
			return docs, nil
		},
	}
	run := &domain.Run{Steps: []domain.StepResult{st}}

	hints := Run(context.Background(), eng, run)
	require.Len(t, hints, maxHintsPerStep)
	// Highest score first.
	assert.Equal(t, float64(9), hints[0].Score)
	assert.Equal(t, float64(5), hints[len(hints)-1].Score)
}

// TestRun_SearchBudgetSharedAcrossSteps constructs two failing steps whose
// response bodies each yield 6 tokens (so, on its own, each step would spend
// 12 searches -- 6 tokens x docs+memories). Run's shared searchBudget (12)
// must cap the combined total, starving the second step.
func TestRun_SearchBudgetSharedAcrossSteps(t *testing.T) {
	sixTokenBody := map[string]any{
		"code": "TOK1", "error": "TOK2", "error_code": "TOK3",
		"errorCode": "TOK4", "type": "TOK5", "reason": "TOK6",
	}
	mk := func(id string) domain.StepResult {
		return domain.StepResult{
			StepID: id, Operation: "op", Status: domain.StepFailed,
			Response: &domain.ResponseRecord{Status: 500, Body: sixTokenBody},
		}
	}
	eng := &stubEngine{
		getOperation: func(ctx context.Context, id string) (*domain.Operation, error) {
			return nil, errors.New("no operation")
		},
	}
	run := &domain.Run{Steps: []domain.StepResult{mk("first"), mk("second")}}

	Run(context.Background(), eng, run)
	total := len(eng.docsCalls) + len(eng.memCalls)
	assert.LessOrEqual(t, total, searchBudget)
	assert.Equal(t, searchBudget, total, "the first step alone should exhaust the shared budget")
}

func TestRunStep_ReturnsOnlyTheNamedStep(t *testing.T) {
	op := allocateOp()
	eng := &stubEngine{
		getOperation: func(ctx context.Context, id string) (*domain.Operation, error) { return &op, nil },
		searchDocs: func(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
			return []domain.DocSearchResult{noRiderDoc()}, nil
		},
	}
	run := &domain.Run{Steps: []domain.StepResult{
		{StepID: "passed", Status: domain.StepPassed},
		noRiderStep(),
	}}

	hints := RunStep(context.Background(), eng, run, "allocate")
	require.NotEmpty(t, hints)
	for _, h := range hints {
		assert.Equal(t, "allocate", h.StepID)
	}

	assert.Nil(t, RunStep(context.Background(), eng, run, "passed"))
	assert.Nil(t, RunStep(context.Background(), eng, run, "no-such-step"))
}

func TestRunStep_NilRunOrEngine(t *testing.T) {
	assert.Nil(t, RunStep(context.Background(), &stubEngine{}, nil, "x"))
	assert.Nil(t, RunStep(context.Background(), nil, &domain.Run{}, "x"))
}

// Tier methods the diagnoser never calls; present so the stub still
// satisfies engine.MemoryAPI.
func (s stubMemories) Move(context.Context, string, string) (*domain.Memory, error) { return nil, nil }
func (s stubMemories) Commit(context.Context, string, string) (*domain.Memory, error) {
	return nil, nil
}
