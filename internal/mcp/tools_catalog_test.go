package mcp

import (
	"context"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestTool_ListServices(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "list_services", map[string]any{})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[ListServicesOutput](t, res.StructuredContent)
	require.Len(t, out.Services, 1)
	assert.Equal(t, "rider-service", out.Services[0].Name)
	assert.Equal(t, 2, out.Services[0].OperationCount)
	assert.Contains(t, firstText(res), "rider-service")
}

func TestTool_GetService(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_service", map[string]any{"name": "rider-service"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[GetServiceOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service", out.Name)
	assert.Equal(t, []string{"team-rider"}, out.Owners)
	assert.Equal(t, 2, out.OperationCount)
	assert.Contains(t, out.Docs, "docs/allocation.md")
}

// TestTool_GetService_ReadsLine: get_service and list_services say where a
// service is read from on this machine, because that is what decides
// whether service-scoped memories, examples and flows can be written for it.
func TestTool_GetService_ReadsLine(t *testing.T) {
	t.Run("BoundLocalCheckout", func(t *testing.T) {
		cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
		eng.st.mu.Lock()
		eng.st.services[0].Binding = &domain.ServiceBinding{
			Mode:     domain.BindingLocal,
			Team:     &domain.Source{Kind: domain.SourceGit, URL: "git@github.com:org/rider.git", Ref: "main"},
			Local:    &domain.LocalCheckout{Path: "~/code/rider", Branch: "feat/x", Commit: "abc123", Dirty: 3},
			Writable: true,
		}
		eng.st.mu.Unlock()

		res := callTool(t, cs, "get_service", map[string]any{"name": "rider-service"})
		require.False(t, res.IsError, firstText(res))
		assert.Contains(t, firstText(res), "reads: local ~/code/rider (branch feat/x, 3 uncommitted)\n")
		out := decodeStructured[GetServiceOutput](t, res.StructuredContent)
		assert.Equal(t, domain.BindingLocal, out.Binding.Mode)
		assert.True(t, out.Binding.Writable)
		require.NotNil(t, out.Binding.Local)
		assert.Equal(t, "~/code/rider", out.Binding.Local.Path)

		list := callTool(t, cs, "list_services", map[string]any{})
		assert.Contains(t, firstText(list), "  reads: local ~/code/rider (branch feat/x, 3 uncommitted)\n")
		listOut := decodeStructured[ListServicesOutput](t, list.StructuredContent)
		require.Len(t, listOut.Services, 1)
		assert.Equal(t, domain.BindingLocal, listOut.Services[0].Binding.Mode)
	})

	t.Run("TeamGitSourceDerivedWhenBindingMissing", func(t *testing.T) {
		cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
		eng.st.mu.Lock()
		eng.st.services[0].Binding = nil
		eng.st.services[0].Source = domain.Source{Kind: domain.SourceGit, URL: "git@github.com:org/rider-service.git", Ref: "stage"}
		eng.st.mu.Unlock()

		res := callTool(t, cs, "get_service", map[string]any{"name": "rider-service"})
		require.False(t, res.IsError, firstText(res))
		assert.Contains(t, firstText(res), "reads: team git@github.com:org/rider-service.git @ stage (read-only; bind a checkout to contribute)\n")
		out := decodeStructured[GetServiceOutput](t, res.StructuredContent)
		assert.Equal(t, domain.BindingTeam, out.Binding.Mode)
		assert.False(t, out.Binding.Writable)
		require.NotNil(t, out.Binding.Team)
		assert.Equal(t, "git@github.com:org/rider-service.git", out.Binding.Team.URL)
	})

	t.Run("PlainLocalSourceDerivedWhenBindingMissing", func(t *testing.T) {
		cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
		eng.st.mu.Lock()
		eng.st.services[0].Binding = nil
		eng.st.services[0].Source = domain.Source{Kind: domain.SourceLocal, Path: "/srv/rider"}
		eng.st.mu.Unlock()

		res := callTool(t, cs, "get_service", map[string]any{"name": "rider-service"})
		require.False(t, res.IsError, firstText(res))
		assert.Contains(t, firstText(res), "reads: local /srv/rider\n")
		assert.True(t, decodeStructured[GetServiceOutput](t, res.StructuredContent).Binding.Writable)
	})
}

// TestTool_GetService_WarningsAndAcceptedWarnings checks that get_service
// surfaces both halves of a service's warning split (domain.Service.Warnings
// vs AcceptedWarnings): unaccepted warnings as-is, and accepted ones with
// the reason they were accepted. newFixtureEngine's rider-service has
// neither, so this builds its own minimal fixture directly rather than
// reusing newTestSession.
func TestTool_GetService_WarningsAndAcceptedWarnings(t *testing.T) {
	eng := newFakeEngine(domain.Workspace{Name: "test-workspace", Dir: "/workspace"})
	eng.st.services = []domain.Service{{
		ID: "billing-service", Name: "billing-service", Status: domain.SyncOK, OperationCount: 1,
		Warnings: []domain.LintWarning{{Code: "MISSING_SUMMARY", Message: "billing-service.charge has no summary"}},
		AcceptedWarnings: []domain.AcceptedLintWarning{{
			LintWarning: domain.LintWarning{Code: "UNSUPPORTED_MEDIA_TYPE", Message: "billing-service.charge response 200 has no application/json content"},
			Reason:      "Returns text/plain by design; the contract describes the wire faithfully.",
		}},
	}}

	srv := NewServer(Options{Engine: eng, Config: Config{Default: DefaultPermissions()}, Version: "test", Logger: silentLogger})
	c1, c2 := sdkmcp.NewInMemoryTransports()
	ctx := context.Background()
	_, err := srv.Connect(ctx, c1, nil)
	require.NoError(t, err)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "claude-code", Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, c2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	res := callTool(t, cs, "get_service", map[string]any{"name": "billing-service"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[GetServiceOutput](t, res.StructuredContent)
	require.Len(t, out.Warnings, 1)
	assert.Equal(t, "MISSING_SUMMARY", out.Warnings[0].Code)
	require.Len(t, out.AcceptedWarnings, 1)
	assert.Equal(t, "UNSUPPORTED_MEDIA_TYPE", out.AcceptedWarnings[0].Code)
	assert.Equal(t, "Returns text/plain by design; the contract describes the wire faithfully.", out.AcceptedWarnings[0].Reason)

	text := firstText(res)
	assert.Contains(t, text, "warning [MISSING_SUMMARY] billing-service.charge has no summary")
	assert.Contains(t, text, "1 warnings accepted (reviewed)")
}

func TestTool_GetService_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_service", map[string]any{"name": "nope"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_SERVICE_NOT_FOUND")
}

func TestTool_SearchAPIs(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "search_apis", map[string]any{"query": "rider"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[SearchAPIsOutput](t, res.StructuredContent)
	assert.NotEmpty(t, out.Results)
	found := false
	for _, r := range out.Results {
		if r.ID == "rider-service.getRider" {
			found = true
			assert.Equal(t, "GET", r.Method)
		}
	}
	assert.True(t, found)
}

func TestTool_SearchAPIs_MethodFilter(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "search_apis", map[string]any{"query": "rider", "method": "POST"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[SearchAPIsOutput](t, res.StructuredContent)
	for _, r := range out.Results {
		assert.Equal(t, "POST", r.Method)
	}
}

func TestTool_GetAPI_DetailLevels(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	summary := callTool(t, cs, "get_api", map[string]any{"id": "rider-service.getRider"})
	require.False(t, summary.IsError, firstText(summary))
	summaryOut := decodeStructured[GetAPIOutput](t, summary.StructuredContent)
	assert.Equal(t, "GET", summaryOut.Method)
	assert.Empty(t, summaryOut.Fields)
	assert.Empty(t, summaryOut.Schemas)

	fields := callTool(t, cs, "get_api", map[string]any{"id": "rider-service.getRider", "detail": "fields"})
	require.False(t, fields.IsError, firstText(fields))
	fieldsOut := decodeStructured[GetAPIOutput](t, fields.StructuredContent)
	assert.NotEmpty(t, fieldsOut.Fields)
	assert.Empty(t, fieldsOut.Schemas)

	full := callTool(t, cs, "get_api", map[string]any{"id": "rider-service.getRider", "detail": "full"})
	require.False(t, full.IsError, firstText(full))
	fullOut := decodeStructured[GetAPIOutput](t, full.StructuredContent)
	assert.NotEmpty(t, fullOut.Fields)
	assert.NotEmpty(t, fullOut.Schemas)

	// Progressive disclosure: each level's marshaled size should not shrink.
	summarySize := len(mustMarshal(t, summaryOut))
	fieldsSize := len(mustMarshal(t, fieldsOut))
	fullSize := len(mustMarshal(t, fullOut))
	assert.Less(t, summarySize, fieldsSize)
	assert.Less(t, fieldsSize, fullSize)
}

// TestTool_GetAPI_RequestExample pins the payload get_api hands back at every
// detail level. Agents were reading the schema here and then assembling a body
// in a throwaway script; a ready-to-send example, labelled with how much to
// trust it, is the thing that makes that unnecessary.
func TestTool_GetAPI_RequestExample(t *testing.T) {
	cs, eng := newTestSessionAndEngine(t, Config{Default: DefaultPermissions()}, "claude-code")
	saved := eng.st.examples

	// Nothing saved: synthesized from the schema, and it says how to do better.
	eng.st.examples = nil
	res := callTool(t, cs, "get_api", map[string]any{"id": "rider-service.createRider"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetAPIOutput](t, res.StructuredContent)
	require.NotNil(t, out.RequestExample, "even the summary level should answer what a call looks like")
	assert.Equal(t, domain.RequestExampleSynthesized, out.RequestExample.Source)
	assert.Equal(t, map[string]any{"name": "<name>"}, out.RequestExample.Body)
	assert.Contains(t, firstText(res), "request example (schema)")
	assert.Contains(t, firstText(res), `"name"`)
	assert.Contains(t, firstText(res), "create_example(run_id)", "an unproven payload should say how to prove one")

	// A hand-written saved example beats the schema, and is labelled as unproven.
	eng.st.examples = saved
	res = callTool(t, cs, "get_api", map[string]any{"id": "rider-service.createRider"})
	out = decodeStructured[GetAPIOutput](t, res.StructuredContent)
	require.NotNil(t, out.RequestExample)
	assert.Equal(t, domain.RequestExampleSaved, out.RequestExample.Source)
	assert.Contains(t, out.RequestExample.Note, "not yet confirmed")

	// A verified one outranks it, and then there is nothing left to prove.
	verified := make([]domain.SavedExample, len(saved))
	copy(verified, saved)
	for i := range verified {
		if verified[i].Operation != "rider-service.createRider" {
			continue
		}
		verified[i].ID = "create-rider"
		verified[i].Verified = &domain.ExampleVerified{Env: "staging", At: time.Now()}
	}
	eng.st.examples = verified
	res = callTool(t, cs, "get_api", map[string]any{"id": "rider-service.createRider", "detail": "fields"})
	require.False(t, res.IsError, firstText(res))
	out = decodeStructured[GetAPIOutput](t, res.StructuredContent)
	require.NotNil(t, out.RequestExample)
	assert.Equal(t, domain.RequestExampleVerified, out.RequestExample.Source)
	assert.Equal(t, "create-rider", out.RequestExample.SourceID)
	assert.Contains(t, firstText(res), `request example (verified "create-rider")`)
	assert.NotContains(t, firstText(res), "create_example(run_id)")
	assert.NotEmpty(t, out.Examples, "the saved-example summaries still come from the same fetch")
}

func TestTool_GetAPI_ByMethodPath(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_api", map[string]any{"id": "GET /v1/riders/{riderId}"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetAPIOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service.getRider", out.ID)
}

func TestTool_GetDSLReference(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_dsl_reference", map[string]any{"topic": "memory"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetDSLReferenceOutput](t, res.StructuredContent)
	assert.Equal(t, "memory", out.Topic)
	assert.Contains(t, out.Text, "Memory DSL")
}

func TestTool_GetDSLReference_DefaultsToFlow(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_dsl_reference", map[string]any{})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetDSLReferenceOutput](t, res.StructuredContent)
	assert.Equal(t, "flow", out.Topic)
}

func TestTool_SearchDocs(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "search_docs", map[string]any{"query": "qcomSkill"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[SearchDocsOutput](t, res.StructuredContent)
	require.NotEmpty(t, out.Results)
	assert.Equal(t, "rider-service", out.Results[0].Service)
}

func TestTool_GetDoc(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_doc", map[string]any{"service": "rider-service", "path": "docs/allocation.md"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetDocOutput](t, res.StructuredContent)
	assert.Contains(t, out.Markdown, "Allocation rules")

	sectioned := callTool(t, cs, "get_doc", map[string]any{
		"service": "rider-service", "path": "docs/allocation.md", "section": "Allocation rules",
	})
	require.False(t, sectioned.IsError, firstText(sectioned))
	sectionOut := decodeStructured[GetDocOutput](t, sectioned.StructuredContent)
	assert.Contains(t, sectionOut.Markdown, "QCOM orders")
	assert.NotContains(t, sectionOut.Markdown, "# Allocation") // just the section body, not the doc title
}

func TestTool_GetSchema(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_schema", map[string]any{"service": "rider-service", "name": "Rider"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetSchemaOutput](t, res.StructuredContent)
	assert.Equal(t, []string{"rider-service.getRider"}, out.UsedBy)

	var found bool
	for _, f := range out.Fields {
		if f.Path == "qcomSkill" {
			found = true
			assert.Equal(t, "boolean", f.Type)
		}
	}
	assert.True(t, found)
}

// TestTool_GetAPI_NoRequestExampleWhenThereIsNothingToShow: a GET with no
// parameters has no payload to hand anyone, and "request example: {}" on every
// such operation is noise, not help.
func TestTool_GetAPI_NoRequestExampleWhenThereIsNothingToShow(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	res := callTool(t, cs, "get_api", map[string]any{"id": "rider-service.bigResponse"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[GetAPIOutput](t, res.StructuredContent)
	assert.Nil(t, out.RequestExample)
	assert.NotContains(t, firstText(res), "request example")
}
