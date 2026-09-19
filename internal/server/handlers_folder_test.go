package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// These tests drive PLAN §34f item 3's folder-move routes: POST
// /v1/memories/{id}/move and POST /v1/examples/{id}/move now carry an
// optional folder alongside the existing tier (handlers_tiers_test.go
// covers tier), and POST /v1/flows/{id}/move is new -- folder only, since a
// flow's tier move already has its own route (/rescope).

// --- POST /v1/memories/{id}/move with folder -----------------------------

func TestMemoryMove_ForwardsFolder(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	mem, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "move me by folder",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/"+mem.ID+"/move", "test-token",
		[]byte(`{"folder":"incidents/2026-09"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.Memory
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, mem.ID, got.ID)
	assert.Equal(t, "incidents/2026-09", got.Folder)

	call := recordedCall(t, fake, "Memories.MoveFolder")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "MoveFolder args: %#v", call.Args)
	assert.Equal(t, mem.ID, args["id"])
	assert.Equal(t, "incidents/2026-09", args["folder"])
}

// An explicit "" folder (the key present, empty value) means "move to the
// root" and must still call MoveFolder, not be treated as "no folder
// given" the way an absent key is.
func TestMemoryMove_ExplicitEmptyFolderMovesToRoot(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	mem, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "already foldered",
		Folder: "a/b",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/"+mem.ID+"/move", "test-token",
		[]byte(`{"folder":""}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.Memory
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "", got.Folder)
	assert.True(t, hasRecordedCall(fake, "Memories.MoveFolder"))
}

// Neither tier nor folder present is still refused, exactly as a blank
// tier alone always was (handlers_tiers_test.go's
// TestMemoryMove_BlankTierIsInvalid).
func TestMemoryMove_NeitherTierNorFolderIsInvalid(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	mem, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "nothing given",
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/memories/"+mem.ID+"/move", "test-token", []byte(`{}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
	assert.False(t, hasRecordedCall(fake, "Memories.MoveFolder"))
	assert.False(t, hasRecordedCall(fake, "Memories.Move"))
}

// --- POST /v1/examples/{id}/move with folder -----------------------------

func TestExampleMove_ForwardsFolder(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	ex, err := fake.Examples().Create(t.Context(), domain.SavedExample{
		ID: "move-by-folder", Operation: "order-service.createOrder", Scope: domain.ExampleScopeWorkspace,
	})
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/examples/"+ex.ID+"/move", "test-token",
		[]byte(`{"folder":"checkout"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.SavedExample
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "checkout", got.Folder)

	call := recordedCall(t, fake, "Examples.MoveFolder")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "MoveFolder args: %#v", call.Args)
	assert.Equal(t, ex.ID, args["id"])
	assert.Equal(t, "checkout", args["folder"])
}

// --- POST /v1/flows/{id}/move ---------------------------------------------

func TestFlowMove_ForwardsFolder(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	fl, err := fake.Flows().Create(t.Context(), tierFlowYAML, "")
	require.NoError(t, err)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/"+fl.ID+"/move", "test-token",
		[]byte(`{"folder":"promotions"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.Flow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, fl.ID, got.ID)
	assert.Equal(t, "promotions", got.Folder)

	call := recordedCall(t, fake, "Flows.Move")
	args, ok := call.Args.(map[string]string)
	require.True(t, ok, "Move args: %#v", call.Args)
	assert.Equal(t, fl.ID, args["id"])
	assert.Equal(t, "promotions", args["folder"])
}

func TestFlowMove_UnknownFlowIsNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows/no-such-flow/move", "test-token", []byte(`{"folder":"x"}`))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.FlowNotFound, decodeErrBody(t, resp).Code)
}

// --- GET list endpoints' ?folder= filter ----------------------------------

func TestFlowsList_FolderFilter(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	root, err := fake.Flows().Create(t.Context(), tierFlowYAML, "")
	require.NoError(t, err)
	_, err = fake.Flows().Move(t.Context(), root.ID, "a")
	require.NoError(t, err)

	resp := doReq(t, ts, http.MethodGet, "/v1/flows?folder=a", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got []domain.FlowSummary
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].Folder)
}

func TestMemoriesList_FolderFilter(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	_, err := fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "in root",
	})
	require.NoError(t, err)
	_, err = fake.Memories().Create(t.Context(), domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "in a folder", Folder: "a",
	})
	require.NoError(t, err)

	resp := doReq(t, ts, http.MethodGet, "/v1/memories?folder=a", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got []domain.Memory
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].Folder)
}

func TestExamplesList_FolderFilter(t *testing.T) {
	fake, ts := newTestServer(t, nil)
	_, err := fake.Examples().Create(t.Context(), domain.SavedExample{ID: "root-ex", Operation: "order-service.createOrder"})
	require.NoError(t, err)
	_, err = fake.Examples().Create(t.Context(), domain.SavedExample{ID: "foldered-ex", Operation: "order-service.createOrder", Folder: "a"})
	require.NoError(t, err)

	resp := doReq(t, ts, http.MethodGet, "/v1/examples?folder=a", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got []domain.SavedExample
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].Folder)
}

// --- POST /v1/flows with folder --------------------------------------------

func TestFlowCreate_WithFolder(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReqBodyReal(t, ts, http.MethodPost, "/v1/flows", "test-token",
		[]byte(`{"yaml":`+jsonString(tierFlowYAML)+`,"owner_kind":"workspace","folder":"promotions"}`))
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got domain.Flow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "promotions", got.Folder)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
