package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// --- example list ---

func TestExampleList_Human_Columns(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "verified-one", Operation: "order-service.createOrder",
		Description: "a verified example", Scope: domain.ExampleScopeWorkspace,
		Verified: &domain.ExampleVerified{Env: "local", RunID: "run_1", At: time.Now()},
	})
	require.NoError(t, err)

	_, err = fake.Examples().Create(ctx, domain.SavedExample{
		ID: "drafted-one", Operation: "rider-service.getRider",
		Description: "a hand-written example", Scope: domain.ExampleScopeService,
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "ID")
	assert.Contains(t, stdout, "OPERATION")
	assert.Contains(t, stdout, "SCOPE")
	assert.Contains(t, stdout, "VERIFIED")
	assert.Contains(t, stdout, "UPDATED")
	assert.Contains(t, stdout, "DESCRIPTION")
	assert.Contains(t, stdout, "verified-one")
	assert.Contains(t, stdout, "local")
	assert.Contains(t, stdout, "drafted-one")
	assert.Contains(t, stdout, "a hand-written example")
}

func TestExampleList_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "ex-1", Operation: "order-service.createOrder", Scope: domain.ExampleScopeWorkspace,
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "ex-1", got[0]["id"])
}

func TestExampleList_Filters(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "order-ex", Operation: "order-service.createOrder", Tags: []string{"happy-path"},
	})
	require.NoError(t, err)
	_, err = fake.Examples().Create(ctx, domain.SavedExample{
		ID: "rider-ex", Operation: "rider-service.getRider", Tags: []string{"qcom"},
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "list", "--operation", "order-service.createOrder", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "order-ex", got[0]["id"])

	stdout, stderr, code = run(t, "--workspace", dir, "example", "list", "--tag", "qcom", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "rider-ex", got[0]["id"])

	stdout, stderr, code = run(t, "--workspace", dir, "example", "list", "--service", "rider-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "rider-ex", got[0]["id"])

	stdout, stderr, code = run(t, "--workspace", dir, "example", "list", "--text", "order-ex", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "order-ex", got[0]["id"])
}

func TestExampleList_NoVerified_ShowsDash(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "unverified-ex", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "unverified-ex")
}

// --- example show ---

func TestExampleShow_Human_YAML(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "show-me", Operation: "order-service.createOrder",
		Description: "shows up as yaml", Body: map[string]any{"customerId": "cust_1"},
		Tags: []string{"qcom"},
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "show", "show-me")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "id: show-me")
	assert.Contains(t, stdout, "operation: order-service.createOrder")
	assert.Contains(t, stdout, "description: shows up as yaml")
	assert.Contains(t, stdout, "customerId: cust_1")
	assert.Contains(t, stdout, "qcom")
}

func TestExampleShow_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "show-json", Operation: "order-service.createOrder",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "show", "show-json", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "show-json", got["id"])
	assert.Equal(t, "order-service.createOrder", got["operation"])
}

func TestExampleShow_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "example", "show", "nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_EXAMPLE_NOT_FOUND", got["code"])
}

// --- example add ---

func TestExampleAdd_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder",
		"--id", "my-example", "-p", "customerId=cust_1")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "created my-example")
	assert.Contains(t, stdout, "use it: `sapien call --example my-example`, or in a flow step: `example: my-example`")
	assert.Contains(t, stdout, "is not a git repo, so workspace examples live only on this machine")
}

func TestExampleAdd_GitRepo_NoNote(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	stdout, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder", "--id", "my-example")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "is not a git repo")
}

func TestExampleAdd_ServiceScope_NoGitNote(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder",
		"--id", "svc-example", "--scope", "service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "is not a git repo")
}

func TestExampleAdd_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder",
		"--id", "json-example", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "json-example", got["id"])
	assert.Nil(t, got["verified"])
}

func TestExampleAdd_DefaultID(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "created createorder")

	got, err := fake.Examples().Get(context.Background(), "createorder")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", got.Operation)
}

func TestExampleAdd_RecordsFullRequest(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder",
		"--id", "full-example", "--description", "full request", "--tag", "qcom", "--tag", "happy-path",
		"-p", "customerId=cust_1", "--body", `{"type":"QCOM"}`, "-H", "X-Trace: abc")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "created full-example")

	call := lastCall(fake, "Examples.Create")
	ex, ok := call.Args.(domain.SavedExample)
	require.True(t, ok)
	assert.Equal(t, "full-example", ex.ID)
	assert.Equal(t, "order-service.createOrder", ex.Operation)
	assert.Equal(t, "full request", ex.Description)
	assert.ElementsMatch(t, []string{"qcom", "happy-path"}, ex.Tags)
	assert.Equal(t, map[string]any{"customerId": "cust_1"}, ex.Input)
	assert.Equal(t, map[string]any{"type": "QCOM"}, ex.Body)
	assert.Equal(t, map[string]string{"X-Trace": "abc"}, ex.Headers)
	assert.Nil(t, ex.Verified)
}

func TestExampleAdd_InvalidParam(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder", "-p", "bad")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "E_INVALID")
}

func TestExampleAdd_Conflict(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	_, err := fake.Examples().Create(ctx, domain.SavedExample{ID: "dup", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder", "--id", "dup", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_CONFLICT", got["code"])
}

// --- example rm ---

func TestExampleRm_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	_, err := fake.Examples().Create(ctx, domain.SavedExample{ID: "to-remove", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "rm", "to-remove")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "removed to-remove")

	_, err = fake.Examples().Get(ctx, "to-remove")
	assert.Error(t, err)
}

func TestExampleRm_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	_, err := fake.Examples().Create(ctx, domain.SavedExample{ID: "to-remove-json", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "rm", "to-remove-json", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "to-remove-json", got["removed"])
}

func TestExampleRm_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "example", "rm", "nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_EXAMPLE_NOT_FOUND", got["code"])
}

// --- example rescope ---

func TestExampleRescope_ToService(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "rescope-me", Operation: "order-service.createOrder", Scope: domain.ExampleScopeWorkspace,
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "rescope", "rescope-me", "--scope", "service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "rescope-me", got["id"])

	updated, err := fake.Examples().Get(ctx, "rescope-me")
	require.NoError(t, err)
	assert.Equal(t, domain.ExampleScopeService, updated.Scope)
}

func TestExampleRescope_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	_, err := fake.Examples().Create(ctx, domain.SavedExample{
		ID: "rescope-human", Operation: "order-service.createOrder", Scope: domain.ExampleScopeWorkspace,
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "rescope", "rescope-human", "--scope", "workspace")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "->")
}

func TestExampleRescope_MissingScope(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	_, err := fake.Examples().Create(ctx, domain.SavedExample{ID: "no-scope-flag", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "example", "rescope", "no-scope-flag", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
	assert.NotEmpty(t, got["hint"])
}

func TestExampleRescope_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "example", "rescope", "nope", "--scope", "service", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_EXAMPLE_NOT_FOUND", got["code"])
}

// --- run save-example ---

func TestRunSaveExample_JSON(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	runs, err := fake.Runs().List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	runID := runs[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "run", "save-example", runID,
		"--name", "from-run", "--step", "create", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "from-run", got["id"])
	verified, ok := got["verified"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "create", verified["step"])
	assert.Equal(t, runID, verified["run"])

	created, err := fake.Examples().Get(ctx, "from-run")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", created.Operation)
}

func TestRunSaveExample_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	runs, err := fake.Runs().List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	runID := runs[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "run", "save-example", runID,
		"--name", "from-run-human", "--step", "create")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "saved example from-run-human")
	// PLAN §34b build item 4 scopes the "use it:" hint to `example add` and
	// `call --save-example`; `run save-example` prints only the
	// confirmation line.
	assert.NotContains(t, stdout, "use it:")
}

func TestRunSaveExample_MissingName(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	runs, err := fake.Runs().List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, runs)

	_, stderr, code := run(t, "--workspace", dir, "run", "save-example", runs[0].ID, "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
	assert.NotEmpty(t, got["hint"])
}

func TestRunSaveExample_RunNotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "run", "save-example", "run_nope", "--name", "whatever", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_RUN_NOT_FOUND", got["code"])
}

func TestRunSaveExample_ScopeDescriptionTags(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()
	runs, err := fake.Runs().List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, runs)

	_, stderr, code := run(t, "--workspace", dir, "run", "save-example", runs[0].ID,
		"--name", "from-run-tagged", "--step", "create", "--scope", "service",
		"--description", "captured from a run", "--tag", "qcom")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	call := lastCall(fake, "Examples.FromRun")
	req, ok := call.Args.(engine.ExampleFromRun)
	require.True(t, ok)
	assert.Equal(t, "create", req.StepID)
	assert.Equal(t, domain.ExampleScope("service"), req.Scope)
	assert.Equal(t, "captured from a run", req.Description)
	assert.Equal(t, []string{"qcom"}, req.Tags)
}
