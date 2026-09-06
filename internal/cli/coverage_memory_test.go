package cli_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

// --- memory add: default scope/type fall back (mem.Scope == "" / mem.Type
// == "") when explicitly passed as empty strings, overriding the flags'
// own "workspace"/"note" defaults. ---

func TestMemoryAdd_EmptyScopeAndTypeFallBack(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "a memory with empty scope/type flags",
		"--scope", "", "--type", "", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	id, _ := got["id"].(string)
	require.NotEmpty(t, id)

	mem, err := fake.Memories().Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, domain.ScopeWorkspace, mem.Scope)
	assert.Equal(t, domain.MemoryNote, mem.Type)
}

// --- memory add --field without --op or --schema: the flag's help text
// says the field "needs --op or --schema", but nothing in the CLI enforces
// that; the memory is created regardless. ---

func TestMemoryAdd_FieldWithoutOpOrSchema_Succeeds(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "a field-scoped memory with no op/schema",
		"--field", "body.orderId")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "mem_")
}

func TestMemoryAdd_AllFlags(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "--env", "local", "memory", "add", "fully specified memory",
		"--op", "order-service.createOrder", "--field", "body.customerId", "--service", "order-service",
		"--schema", "order-service.Order", "--flow", "create-order-flow", "--concept", "orders",
		"--scope", "personal", "--type", "invariant", "--tag", "a", "--tag", "b", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	id, _ := got["id"].(string)
	require.NotEmpty(t, id)

	mem, err := fake.Memories().Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, domain.ScopePersonal, mem.Scope)
	assert.Equal(t, domain.MemoryInvariant, mem.Type)
	assert.Equal(t, "local", mem.Subject.Environment)
	assert.ElementsMatch(t, []string{"a", "b"}, mem.Tags)
}

// --- memory list/search: filters ---

func TestMemoryList_Filters(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "list",
		"--op", "order-service.createOrder", "--service", "order-service", "--scope", "workspace",
		"--type", "gotcha", "--limit", "5", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var mems []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &mems))
	require.Len(t, mems, 1)
}

func TestMemorySearch_Filters(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "search", "customerId",
		"--op", "order-service.createOrder", "--service", "order-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var results []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &results))
	assert.NotEmpty(t, results)
}

// --- memory promote: human-mode printPromotionTarget rendering ---

func TestMemoryPromote_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	mems, err := fake.Memories().List(context.Background(), domain.MemoryQuery{})
	require.NoError(t, err)
	require.NotEmpty(t, mems)
	id := mems[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "promote", id)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "kind: openapi")
	assert.Contains(t, stdout, "location:")
	assert.Contains(t, stdout, "pointer: /paths")
	assert.Contains(t, stdout, "suggested:")
}

// --- memory show: writeSubjectYAML's every Subject-field branch, via
// memories constructed directly through the Fake's public Create API. ---

func TestMemoryShow_SubjectVariants(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		subject domain.Subject
		want    string
	}{
		{"field", domain.Subject{Field: "body.orderId"}, "field: body.orderId"},
		{"schema", domain.Subject{Schema: "order-service.Order"}, "schema: order-service.Order"},
		{"flow", domain.Subject{Flow: "create-order-flow", Step: "create"}, "step: create"},
		{"run", domain.Subject{Run: "run_abc"}, "run: run_abc"},
		{"environment", domain.Subject{Environment: "staging"}, "environment: staging"},
		{"concept", domain.Subject{Concept: "orders"}, "concept: orders"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, err := fake.Memories().Create(ctx, domain.Memory{
				Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
				Subject: tc.subject, Source: domain.MemorySource{Kind: "user"},
				Status: domain.MemoryActive, Text: "subject variant memory: " + tc.name,
			})
			require.NoError(t, err)

			stdout, stderr, code := run(t, "--workspace", dir, "memory", "show", created.ID)
			require.Equal(t, 0, code, "stderr: %s", stderr)
			assert.Contains(t, stdout, tc.want)
		})
	}
}

// TestMemoryShow_EmptySubject exercises the case where no subject field is
// set at all (Subject.IsZero() true), so writeSubjectYAML's caller skips
// emitting a "subject:" block entirely.
func TestMemoryShow_EmptySubject(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	created, err := fake.Memories().Create(context.Background(), domain.Memory{
		Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
		Source: domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Text: "memory with no subject at all",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "show", created.ID)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "subject:")
}

// --- subjectString (memory list/search table rendering): every Subject
// priority branch, via memories with only one Subject field set. ---

func TestMemoryList_SubjectStringVariants(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	variants := []struct {
		subject domain.Subject
		want    string
	}{
		{domain.Subject{Field: "field_only_marker"}, "field_only_marker"},
		{domain.Subject{Schema: "schema_only_marker"}, "schema_only_marker"},
		{domain.Subject{Service: "service_only_marker"}, "service_only_marker"},
		{domain.Subject{Concept: "concept_only_marker"}, "concept_only_marker"},
		{domain.Subject{Run: "run_only_marker"}, "run_only_marker"},
		{domain.Subject{}, ""}, // no subject at all: subjectString returns ""
	}

	for _, v := range variants {
		_, err := fake.Memories().Create(ctx, domain.Memory{
			Type: domain.MemoryNote, Scope: domain.ScopeWorkspace,
			Subject: v.subject, Source: domain.MemorySource{Kind: "user"},
			Status: domain.MemoryActive, Text: "variant text",
		})
		require.NoError(t, err)
	}

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	for _, v := range variants {
		if v.want != "" {
			assert.Contains(t, stdout, v.want)
		}
	}
}
