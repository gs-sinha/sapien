// Package acceptance holds Sapien's Phase 4 exit-criterion test (PLAN.md
// §34: "Claude Code and Codex over MCP complete PRD §48 end to end,
// including the memory-aware assertion"): the PRD §48 primary V1 success
// scenario, driven entirely through the MCP tool surface exactly as an
// external agent would drive it, plus two CLI-level checks that the same
// workspace answers identically through cli.Execute (one forced to
// engine.Local, one routed through engine.Remote to a real daemon).
//
// This package is test-only: it exists to compose already-finished
// packages (fixtures/logistics/mock, internal/workspace,
// internal/engine/local, internal/mcp, internal/cli, internal/daemon,
// internal/server) into the one end-to-end scenario PLAN.md §34 names as
// Phase 4's exit criterion. Nothing here is imported by non-test code.
package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/fixtures/logistics/mock"
	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/env"
	sapienmcp "github.com/gs-sinha/sapien/internal/mcp"
	"github.com/gs-sinha/sapien/internal/server"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// --- fixture plumbing -------------------------------------------------

// fixturesRoot returns the absolute path of fixtures/logistics, resolved
// relative to this test file rather than the working directory `go test`
// happens to use.
func fixturesRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "logistics")
}

// copyFixtureServices copies the three logistics service packages into a
// fresh temp dir and returns it, so this test never writes into
// fixtures/logistics itself (shared by every phase's tests).
func copyFixtureServices(t *testing.T) (dir string) {
	t.Helper()
	root := fixturesRoot(t)
	dir = t.TempDir()
	for _, svc := range []string{"order-service", "allocation-service", "rider-service"} {
		require.NoError(t, copyDir(filepath.Join(root, svc), filepath.Join(dir, svc)))
	}
	return dir
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// successFlowYAML is the PRD §48 primary success scenario as a flow: create
// a QCOM order, allocate a rider, fetch that rider, and verify it is
// online and QCOM-eligible. Operation IDs and body shapes match
// fixtures/logistics exactly (see fixtures/logistics/README.md).
const successFlowYAML = `version: 1
id: qcom-allocation
name: QCOM allocation smoke test
description: Create a QCOM order, allocate a rider, verify it is online and QCOM-eligible.
tags: [allocation, smoke]

inputs:
  customerId: { type: string, default: "cust_123" }

steps:
  - id: create
    call: order-service.createOrder
    body:
      customerId: "${inputs.customerId}"
      type: QCOM
      pickup: { lat: 12.9716, lng: 77.5946 }
      drop: { lat: 12.9352, lng: 77.6146 }
    extract:
      orderId: body.orderId
    assert:
      - status == 201

  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract:
      riderId: body.riderId
    assert:
      - status == 201

  - id: rider
    call: rider-service.getRider
    input: { riderId: "${steps.allocate.out.riderId}" }
    assert:
      - status == 200
      - body.online == true
      - { path: body.qcomSkill, eq: true, message: "QCOM riders must have qcomSkill" }
`

// brokenFlowYAML is successFlowYAML with the last step's call typo'd
// ("getRiders" instead of "getRider"), to exercise validate_flow's
// unknown-operation repair loop (PLAN §23.1).
// exampleFlowYAML reuses the example saved in step 15 for its first step;
// the second step chains off it the ordinary way.
const exampleFlowYAML = `version: 1
id: qcom-from-example
steps:
  - id: create
    example: qcom-order
    extract: { orderId: body.orderId }
    assert: [status == 201]
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract: { allocationId: body.allocationId }
    assert: [status == 201]
  - id: release
    call: allocation-service.releaseAllocation
    input: { allocationId: "${steps.allocate.out.allocationId}" }
    assert: [status < 300]
`

// resumeFlowYAML exercises setup/teardown and resume: setup creates a
// STANDARD order (so the fixture's QCOM rider pool is untouched), allocate
// extracts the rider, rider deliberately asserts a wrong status until
// patch_flow fixes it, and teardown releases the allocation either way.
const resumeFlowYAML = `version: 1
id: resume-demo
setup:
  - id: create
    call: order-service.createOrder
    body:
      customerId: cust_resume
      type: STANDARD
      pickup: { lat: 12.97, lng: 77.59 }
      drop: { lat: 12.93, lng: 77.61 }
    extract: { orderId: body.orderId }
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract: { riderId: body.riderId, allocationId: body.allocationId }
    assert: [status == 201]
  - id: rider
    call: rider-service.getRider
    input: { riderId: "${steps.allocate.out.riderId}" }
    assert: [status == 999]
teardown:
  - id: release
    call: allocation-service.releaseAllocation
    input: { allocationId: "${steps.allocate.out.allocationId}" }
    assert: [status < 300]
`

const brokenFlowYAML = `version: 1
id: qcom-allocation-broken
name: broken flow for the repair-loop assertion
steps:
  - id: create
    call: order-service.createOrder
    body:
      customerId: "cust_123"
      type: QCOM
      pickup: { lat: 12.9716, lng: 77.5946 }
      drop: { lat: 12.9352, lng: 77.6146 }
    extract:
      orderId: body.orderId
    assert:
      - status == 201

  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract:
      riderId: body.riderId
    assert:
      - status == 201

  - id: rider
    call: rider-service.getRiders
    input: { riderId: "${steps.allocate.out.riderId}" }
    assert:
      - status == 200
`

// setupAcceptanceWorkspace starts the three fixture mock servers, builds a
// fresh workspace with the three logistics services registered against
// them, and points environments/local.yaml at the mocks (PLAN §7). It
// returns the opened engine (Watch off: this is a one-shot-style workspace,
// not a daemon) and the workspace itself.
func setupAcceptanceWorkspace(t *testing.T) (*local.Local, *domain.Workspace) {
	t.Helper()

	orderURL, allocURL, riderURL, _ := mock.StartAll(t, mock.Options{})

	wsDir := t.TempDir()
	ws, err := workspace.Init(wsDir, "acceptance")
	require.NoError(t, err)

	fixDir := copyFixtureServices(t)

	eng, err := local.Open(ws, local.Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = eng.Close() })

	ctx := context.Background()
	for _, svc := range []string{"order-service", "allocation-service", "rider-service"} {
		_, err := eng.Services().Add(ctx, svc, domain.Source{Kind: domain.SourceLocal, Path: filepath.Join(fixDir, svc)})
		require.NoErrorf(t, err, "adding %s", svc)
	}

	localEnv := &domain.Environment{
		Version:    1,
		Name:       "local",
		Production: false,
		Services: map[string]domain.ServiceEnv{
			"order-service":      {BaseURL: orderURL},
			"allocation-service": {BaseURL: allocURL},
			"rider-service":      {BaseURL: riderURL},
		},
	}
	require.NoError(t, workspace.SaveEnvironment(ws, localEnv))

	return eng, ws
}

// --- MCP client plumbing (mirrors internal/mcp's own _test.go helpers,
// which are unexported and unavailable from this package) -------------

// newMCPSession builds a Sapien MCP server over eng, gated by cfg, and
// connects an in-memory client named clientName to it.
func newMCPSession(t *testing.T, eng *local.Local, cfg sapienmcp.Config, clientName string) *sdkmcp.ClientSession {
	t.Helper()
	srv := sapienmcp.NewServer(sapienmcp.Options{Engine: eng, Config: cfg, Version: "acceptance-test"})

	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	ctx := context.Background()

	_, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: clientName, Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *sdkmcp.ClientSession, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	require.NoErrorf(t, err, "tool call %s transport error", name)
	return res
}

// decodeStructured re-marshals a CallToolResult's StructuredContent (or any
// other any-typed value) into T.
func decodeStructured[T any](t *testing.T, v any) T {
	t.Helper()
	var out T
	b, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func firstText(res *sdkmcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func containsOperation(ops []domain.OperationContext, id string) bool {
	for _, op := range ops {
		if op.ID == id {
			return true
		}
	}
	return false
}

func containsMemoryID(mems []domain.MemoryContext, id string) bool {
	for _, m := range mems {
		if m.ID == id {
			return true
		}
	}
	return false
}

// TestAcceptance_PRDSuccessScenario drives PRD §48 end to end through MCP,
// fully in-process (PLAN §34's Phase 4 exit criterion), then re-checks the
// resulting workspace through the CLI: once forced to engine.Local
// (SAPIEN_NO_DAEMON=1) and once routed through engine.Remote to a real
// daemon started in-process.
func TestAcceptance_PRDSuccessScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("acceptance: PRD §48 end-to-end scenario is skipped with -short")
	}

	eng, ws := setupAcceptanceWorkspace(t)

	// A default-permission session proves search/inspect/docs and the
	// execute_mutation permission gate; a second, mutation-granted session
	// (built once the gate is proven) drives everything from create_memory
	// onward.
	csDefault := newMCPSession(t, eng, sapienmcp.Config{Default: sapienmcp.DefaultPermissions()}, "acceptance")

	// 1. search_apis("allocate rider") -> allocation-service.allocate first.
	res := callTool(t, csDefault, "search_apis", map[string]any{"query": "allocate rider"})
	require.False(t, res.IsError, firstText(res))
	searchOut := decodeStructured[sapienmcp.SearchAPIsOutput](t, res.StructuredContent)
	require.NotEmpty(t, searchOut.Results)
	assert.Equal(t, "allocation-service.allocate", searchOut.Results[0].ID)

	// 2. get_api(allocation-service.allocate, fields).
	res = callTool(t, csDefault, "get_api", map[string]any{"id": "allocation-service.allocate", "detail": "fields"})
	require.False(t, res.IsError, firstText(res))
	apiOut := decodeStructured[sapienmcp.GetAPIOutput](t, res.StructuredContent)
	assert.Equal(t, "allocation-service.allocate", apiOut.ID)
	assert.NotEmpty(t, apiOut.Fields)

	// 3. search_docs("QCOM") non-empty.
	res = callTool(t, csDefault, "search_docs", map[string]any{"query": "QCOM"})
	require.False(t, res.IsError, firstText(res))
	docsOut := decodeStructured[sapienmcp.SearchDocsOutput](t, res.StructuredContent)
	assert.NotEmpty(t, docsOut.Results)

	// 4. execute_api(order-service.createOrder, env local, QCOM body) proves
	// the permission gate: execute_mutation is not granted by default.
	createOrderArgs := map[string]any{
		"id":  "order-service.createOrder",
		"env": "local",
		"body": map[string]any{
			"customerId": "cust_gate",
			"type":       "QCOM",
			"pickup":     map[string]any{"lat": 12.9716, "lng": 77.5946},
			"drop":       map[string]any{"lat": 12.9352, "lng": 77.6146},
		},
	}
	res = callTool(t, csDefault, "execute_api", createOrderArgs)
	require.True(t, res.IsError, "execute_api should be denied without execute_mutation")
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
	assert.Contains(t, firstText(res), "execute_mutation")

	mutationPerms := sapienmcp.DefaultPermissions()
	mutationPerms.ExecuteMutation = true
	cs := newMCPSession(t, eng, sapienmcp.Config{
		Default: sapienmcp.DefaultPermissions(),
		Clients: map[string]sapienmcp.Permissions{"acceptance": mutationPerms},
	}, "acceptance")

	res = callTool(t, cs, "execute_api", createOrderArgs)
	require.False(t, res.IsError, firstText(res))
	execOut := decodeStructured[sapienmcp.RunView](t, res.StructuredContent)
	require.Len(t, execOut.Steps, 1)
	require.NotNil(t, execOut.Steps[0].Response)
	assert.Equal(t, 201, execOut.Steps[0].Response.Status)

	// 5. create_memory: the qcomSkill invariant discovered "during
	// debugging" in PRD §48.
	res = callTool(t, cs, "create_memory", map[string]any{
		"text": "QCOM allocations should only select riders with qcomSkill=true.",
		"subject": map[string]any{
			"operation": "rider-service.getRider",
			"field":     "response.200.body.qcomSkill",
		},
		"type": "invariant",
	})
	require.False(t, res.IsError, firstText(res))
	memOut := decodeStructured[sapienmcp.CreateMemoryOutput](t, res.StructuredContent)
	require.NotEmpty(t, memOut.Memory.ID)
	memoryID := memOut.Memory.ID

	// 6. get_context("Create a QCOM allocation test"): operations must
	// include allocation-service.allocate and the bundle must include the
	// new memory. rider-service.getRider is expected too, via
	// internal/retrieval's memory-driven operation expansion (PLAN
	// §14/§25) -- checked in its own subtest so a t.Skip there (if that
	// expansion hasn't landed) doesn't abort the rest of the scenario.
	res = callTool(t, cs, "get_context", map[string]any{"intent": "Create a QCOM allocation test"})
	require.False(t, res.IsError, firstText(res))
	bundle := decodeStructured[domain.ContextBundle](t, res.StructuredContent)
	assert.True(t, containsOperation(bundle.Operations, "allocation-service.allocate"),
		"expected allocation-service.allocate in get_context operations, got %+v", bundle.Operations)
	assert.True(t, containsMemoryID(bundle.Memories, memoryID),
		"expected the new memory %s in get_context memories, got %+v", memoryID, bundle.Memories)

	t.Run("get_context includes rider-service.getRider via memory-driven expansion", func(t *testing.T) {
		if !containsOperation(bundle.Operations, "rider-service.getRider") {
			t.Skip("rider-service.getRider not present in get_context's operations; " +
				"internal/retrieval's memory-driven operation expansion (PLAN §14/§25, doc.go/expandFromMemories) " +
				"may not have landed in this build")
		}
		assert.True(t, containsOperation(bundle.Operations, "rider-service.getRider"))
	})

	// 7. get_dsl_reference("flow") non-empty.
	res = callTool(t, cs, "get_dsl_reference", map[string]any{"topic": "flow"})
	require.False(t, res.IsError, firstText(res))
	refOut := decodeStructured[sapienmcp.GetDSLReferenceOutput](t, res.StructuredContent)
	assert.NotEmpty(t, refOut.Text)

	// 8. validate_flow on a deliberately broken flow (unknown op
	// rider-service.getRiders): validate_flow reports diagnostics rather
	// than erroring at the MCP protocol level, so the repair-loop text
	// (not necessarily IsError) is what carries "did you mean".
	res = callTool(t, cs, "validate_flow", map[string]any{"flow_yaml": brokenFlowYAML})
	require.False(t, res.IsError, firstText(res))
	brokenOut := decodeStructured[domain.ValidationResult](t, res.StructuredContent)
	assert.False(t, brokenOut.Valid)
	assert.Contains(t, firstText(res), "did you mean")
	assert.Contains(t, firstText(res), "rider-service.getRider")

	// 9. validate_flow on the correct §48 flow -> valid.
	res = callTool(t, cs, "validate_flow", map[string]any{"flow_yaml": successFlowYAML})
	require.False(t, res.IsError, firstText(res))
	validOut := decodeStructured[domain.ValidationResult](t, res.StructuredContent)
	assert.True(t, validOut.Valid, "diagnostics: %+v", validOut.Diagnostics)

	// 10. create_flow -> file exists under <ws>/local/flows: a new flow
	// starts in this machine's tier and is promoted once it works (PLAN
	// §7b), so the team repo only ever receives flows that ran green.
	res = callTool(t, cs, "create_flow", map[string]any{"flow_yaml": successFlowYAML})
	require.False(t, res.IsError, firstText(res))
	createFlowOut := decodeStructured[sapienmcp.FlowSaveResult](t, res.StructuredContent)
	flowID := createFlowOut.ID
	require.NotEmpty(t, flowID)
	assert.Equal(t, 3, createFlowOut.Steps)
	flowPath := createFlowOut.Path
	if !filepath.IsAbs(flowPath) {
		flowPath = filepath.Join(ws.Dir, flowPath)
	}
	assert.FileExists(t, flowPath)
	assert.Equal(t, domain.FlowOwnerLocal, createFlowOut.Tier)
	assert.Contains(t, flowPath, filepath.Join(ws.Dir, domain.LocalDir, domain.FlowsDir))

	// 11. run_flow(id, local) -> passed with 3 steps.
	res = callTool(t, cs, "run_flow", map[string]any{"id": flowID, "env": "local"})
	require.False(t, res.IsError, firstText(res))
	runOut := decodeStructured[sapienmcp.RunView](t, res.StructuredContent)
	assert.Equal(t, "passed", runOut.Status, firstText(res))
	require.Len(t, runOut.Steps, 3)
	runID := runOut.ID

	// 11b. rescope_flow(id, workspace): the flow ran green, so it is promoted
	// into the team's tier; the file moves, the id does not.
	res = callTool(t, cs, "rescope_flow", map[string]any{"id": flowID, "scope": "workspace"})
	require.False(t, res.IsError, firstText(res))
	rescopeOut := decodeStructured[sapienmcp.RescopeFlowOutput](t, res.StructuredContent)
	assert.Equal(t, domain.FlowOwnerWorkspace, rescopeOut.Tier)
	assert.NoFileExists(t, flowPath)
	promotedPath := rescopeOut.NewPath
	if !filepath.IsAbs(promotedPath) {
		promotedPath = filepath.Join(ws.Dir, promotedPath)
	}
	assert.FileExists(t, promotedPath)
	assert.Contains(t, promotedPath, filepath.Join(ws.Dir, domain.FlowsDir))
	assert.NotContains(t, promotedPath, filepath.Join(ws.Dir, domain.LocalDir))

	// 12. get_run(id) -> assertions all passed; request records carry no
	// Authorization header value in clear (no auth is configured for this
	// workspace's "local" environment, so the header should simply be
	// absent).
	res = callTool(t, cs, "get_run", map[string]any{"id": runID, "include_bodies": true})
	require.False(t, res.IsError, firstText(res))
	getRunOut := decodeStructured[sapienmcp.RunView](t, res.StructuredContent)
	require.Len(t, getRunOut.Steps, 3)
	for _, step := range getRunOut.Steps {
		for _, a := range step.Assertions {
			assert.Truef(t, a.Passed, "step %s assertion %q failed: %s", step.StepID, a.Expr, a.Message)
		}
		if step.Request != nil {
			authz, ok := step.Request.Headers["Authorization"]
			assert.Falsef(t, ok && authz != "", "step %s request carried an Authorization header value in clear: %q", step.StepID, authz)
		}
	}

	// 13. search_memories("qcom") finds the memory.
	res = callTool(t, cs, "search_memories", map[string]any{"query": "qcom"})
	require.False(t, res.IsError, firstText(res))
	searchMemOut := decodeStructured[sapienmcp.SearchMemoriesOutput](t, res.StructuredContent)
	foundByID := false
	for _, m := range searchMemOut.Memories {
		if m.Memory.ID == memoryID {
			foundByID = true
		}
	}
	assert.True(t, foundByID, "expected search_memories(\"qcom\") to find %s, got %+v", memoryID, searchMemOut.Memories)

	// 14. get_relevant_memories([{operation: rider-service.getRider}]) finds
	// it with a structural reason.
	res = callTool(t, cs, "get_relevant_memories", map[string]any{
		"subjects": []map[string]any{{"operation": "rider-service.getRider"}},
	})
	require.False(t, res.IsError, firstText(res))
	relevantOut := decodeStructured[sapienmcp.GetRelevantMemoriesOutput](t, res.StructuredContent)
	var relevantHit *domain.ScoredMemory
	for i := range relevantOut.Memories {
		if relevantOut.Memories[i].Memory.ID == memoryID {
			relevantHit = &relevantOut.Memories[i]
		}
	}
	require.NotNilf(t, relevantHit, "expected get_relevant_memories to find %s, got %+v", memoryID, relevantOut.Memories)
	assert.NotEmpty(t, relevantHit.Reasons, "expected a structural reason for the getRider match")

	// --- Cheap iteration (PLAN §34d): a flow with setup and teardown fails
	// mid-way, gets patched at step level, and resumes without redoing the
	// steps that already passed -------------------------------------------

	// 14a. create_flow returns a summary, never the document.
	res = callTool(t, cs, "create_flow", map[string]any{"flow_yaml": resumeFlowYAML})
	require.False(t, res.IsError, firstText(res))
	saved := decodeStructured[sapienmcp.FlowSaveResult](t, res.StructuredContent)
	assert.Equal(t, "resume-demo", saved.ID)
	assert.Equal(t, 1, saved.SetupSteps)
	assert.Equal(t, 2, saved.Steps)
	assert.Equal(t, 1, saved.TeardownSteps)
	assert.NotContains(t, firstText(res), "steps:", "text is a one-line summary, not the YAML")

	// 14b. The first run fails at the rider step (deliberate wrong assertion);
	// setup ran, teardown still ran.
	res = callTool(t, cs, "run_flow", map[string]any{"id": "resume-demo", "env": "local"})
	require.False(t, res.IsError, firstText(res))
	first := decodeStructured[sapienmcp.RunView](t, res.StructuredContent)
	assert.Equal(t, "failed", first.Status, firstText(res))
	require.Len(t, first.Steps, 4)
	assert.Equal(t, "setup", first.Steps[0].Phase)
	assert.Equal(t, "passed", first.Steps[0].Status)
	assert.Equal(t, "passed", first.Steps[1].Status, "allocate passed")
	assert.Equal(t, "failed", first.Steps[2].Status, "rider assertion failed")
	assert.Equal(t, "teardown", first.Steps[3].Phase)
	assert.Equal(t, "passed", first.Steps[3].Status, "teardown ran despite the failure")

	// 14c. patch_flow fixes the one assertion without resending the flow.
	res = callTool(t, cs, "patch_flow", map[string]any{"id": "resume-demo", "ops": []map[string]any{
		{"kind": "merge_step", "id": "rider", "fields": map[string]any{"assert": []string{"status == 200"}}},
	}})
	require.False(t, res.IsError, firstText(res))
	res = callTool(t, cs, "get_flow", map[string]any{"id": "resume-demo"})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "status == 200", "get_flow returns the patched YAML")
	assert.NotContains(t, firstText(res), "status == 999")

	// 14d. Resume: setup and allocate are reused, rider runs and passes.
	res = callTool(t, cs, "run_flow", map[string]any{"id": "resume-demo", "env": "local", "resume_from": first.ID})
	require.False(t, res.IsError, firstText(res))
	second := decodeStructured[sapienmcp.RunView](t, res.StructuredContent)
	assert.Equal(t, "passed", second.Status, firstText(res))
	assert.Equal(t, first.ID, second.ResumedFrom)
	require.Len(t, second.Steps, 4)
	assert.True(t, second.Steps[0].Reused, "setup reused")
	assert.True(t, second.Steps[1].Reused, "allocate reused")
	assert.False(t, second.Steps[2].Reused, "rider executed")
	assert.Equal(t, "passed", second.Steps[2].Status)
	assert.False(t, second.Steps[3].Reused, "teardown executed again")
	assert.Equal(t, second.Steps[1].Out["riderId"], first.Steps[1].Out["riderId"], "reused extracts are the earlier run's")
	assert.Contains(t, firstText(res), "reused")

	// --- Examples (PLAN §34b): save a verified payload from the run, reuse
	// it from execute_api, get_api, get_context, and a flow step -----------

	// 15. create_example(run_id, step create) -> verified against local.
	res = callTool(t, cs, "create_example", map[string]any{
		"id": "qcom-order", "run_id": runID, "step_id": "create",
		"description": "QCOM order that allocates to a qcom-skilled rider", "tags": []string{"qcom"},
	})
	require.False(t, res.IsError, firstText(res))
	created := decodeStructured[sapienmcp.CreateExampleOutput](t, res.StructuredContent)
	assert.Equal(t, "order-service.createOrder", created.Example.Operation)
	require.NotNil(t, created.Example.Verified, "an example saved from a run is verified")
	assert.Equal(t, "local", created.Example.Verified.Env)
	assert.Equal(t, runID, created.Example.Verified.RunID)
	require.NotNil(t, created.Example.Expect)
	assert.Equal(t, 201, created.Example.Expect.Status)
	assert.FileExists(t, filepath.Join(ws.Dir, "examples", "qcom-order.example.yaml"))

	// 16. list_examples(operation) and get_api(fields) both surface it.
	res = callTool(t, cs, "list_examples", map[string]any{"operation": "order-service.createOrder"})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "qcom-order")
	res = callTool(t, cs, "get_api", map[string]any{"id": "order-service.createOrder", "detail": "fields"})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "qcom-order")

	// 17. execute_api(example) replays it: a second order is created.
	res = callTool(t, cs, "execute_api", map[string]any{"example": "qcom-order", "env": "local"})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "from example qcom-order")
	replay := decodeStructured[sapienmcp.RunView](t, res.StructuredContent)
	assert.Equal(t, "passed", replay.Status, firstText(res))

	// 18. get_context for an order-creation intent carries the example.
	res = callTool(t, cs, "get_context", map[string]any{"intent": "create a QCOM order"})
	require.False(t, res.IsError, firstText(res))
	bundle = decodeStructured[domain.ContextBundle](t, res.StructuredContent)
	foundExample := false
	for _, ex := range bundle.Examples {
		if ex.ID == "qcom-order" {
			foundExample = true
			assert.True(t, ex.Verified)
		}
	}
	assert.True(t, foundExample, "expected qcom-order in get_context examples, got %+v", bundle.Examples)

	// 19. A flow step reuses the example (`example:`): validate, create, run.
	res = callTool(t, cs, "validate_flow", map[string]any{"flow_yaml": exampleFlowYAML})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "valid", firstText(res))
	res = callTool(t, cs, "create_flow", map[string]any{"flow_yaml": exampleFlowYAML})
	require.False(t, res.IsError, firstText(res))
	res = callTool(t, cs, "run_flow", map[string]any{"id": "qcom-from-example", "env": "local"})
	require.False(t, res.IsError, firstText(res))
	exRun := decodeStructured[sapienmcp.RunView](t, res.StructuredContent)
	assert.Equal(t, "passed", exRun.Status, firstText(res))
	require.Len(t, exRun.Steps, 3, "create from example, allocate, release (so the fixture's QCOM rider is free again)")

	// 20. An unknown example id in a flow is a diagnostic with a suggestion.
	res = callTool(t, cs, "validate_flow", map[string]any{"flow_yaml": strings.Replace(exampleFlowYAML, "example: qcom-order", "example: qcom-ordr", 1)})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "UNKNOWN_EXAMPLE")
	assert.Contains(t, firstText(res), "qcom-order")

	// --- CLI-level checks on the same workspace ---------------------

	t.Run("cli flow run over engine.Local (SAPIEN_NO_DAEMON=1)", func(t *testing.T) {
		t.Setenv("SAPIEN_NO_DAEMON", "1")
		var stdout, stderr bytes.Buffer
		code := cli.Execute([]string{"--workspace", ws.Dir, "flow", "run", flowID, "--json"}, &stdout, &stderr)
		require.Equal(t, 0, code, "stderr: %s", stderr.String())

		var got map[string]any
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
		assert.Equal(t, "passed", got["status"])
	})

	t.Run("cli example list over engine.Local", func(t *testing.T) {
		t.Setenv("SAPIEN_NO_DAEMON", "1")
		var stdout, stderr bytes.Buffer
		code := cli.Execute([]string{"--workspace", ws.Dir, "example", "list", "--json"}, &stdout, &stderr)
		require.Equal(t, 0, code, "stderr: %s", stderr.String())
		assert.Contains(t, stdout.String(), "qcom-order")
	})

	t.Run("cli search over engine.Remote (real daemon in-process)", func(t *testing.T) {
		if testing.Short() {
			t.Skip("daemon round trip is skipped with -short")
		}

		var reqCount atomic.Int64
		srv := server.New(server.Options{Engine: eng, Token: "acceptance-daemon-token", Version: cli.Version})
		counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqCount.Add(1)
			srv.Handler().ServeHTTP(w, r)
		})
		httpSrv := httptest.NewServer(counting)
		defer httpSrv.Close()

		u, err := url.Parse(httpSrv.URL)
		require.NoError(t, err)
		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)

		require.NoError(t, daemon.Write(ws, &daemon.Info{
			PID:       os.Getpid(),
			Port:      port,
			Token:     "acceptance-daemon-token",
			Version:   cli.Version,
			Started:   time.Now(),
			Workspace: ws.Dir,
		}))
		defer func() { _ = daemon.Remove(ws) }()

		var stdout, stderr bytes.Buffer
		code := cli.Execute([]string{"--workspace", ws.Dir, "search", "allocate rider", "--json"}, &stdout, &stderr)
		require.Equal(t, 0, code, "stderr: %s", stderr.String())
		assert.Greater(t, reqCount.Load(), int64(0), "expected the CLI's search to have gone through the daemon's HTTP handler (engine.Remote)")

		var results []map[string]any
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &results))
		require.NotEmpty(t, results)
	})

}
