package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
)

func TestExamplesRoutes_StaticRoutesNotSwallowedByIDParam(t *testing.T) {
	_, ts := newTestServer(t, nil)

	// GET /v1/examples/for-operations must hit handleExamplesForOperations,
	// not be captured as GET /v1/examples/{id} with id="for-operations" --
	// which would 404 with errs.ExampleNotFound instead of 200 with a list.
	resp := doReq(t, ts, http.MethodGet, "/v1/examples/for-operations?op=order-service.createOrder", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list []domain.SavedExample
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))

	// POST /v1/examples/reindex must hit handleExamplesReindex, not be
	// treated as PUT/POST against an example named "reindex".
	resp2 := doReq(t, ts, http.MethodPost, "/v1/examples/reindex", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNoContent, resp2.StatusCode)

	// POST /v1/examples/from-run must hit handleExampleFromRun. A
	// nonexistent run id proves the request reached FromRun's own
	// RunNotFound path rather than, say, Create's id/operation validation.
	fromRunBody, err := json.Marshal(engine.ExampleFromRun{RunID: "run_does_not_exist", ID: "x"})
	require.NoError(t, err)
	resp3 := doReqBodyReal(t, ts, http.MethodPost, "/v1/examples/from-run", "test-token", fromRunBody)
	assert.Equal(t, http.StatusNotFound, resp3.StatusCode)
	assert.Equal(t, errs.RunNotFound, decodeErrBody(t, resp3).Code)
}

func TestExamplesCRUD_HTTP(t *testing.T) {
	_, ts := newTestServer(t, nil)

	ex := domain.SavedExample{
		ID:        "create-qcom-order",
		Operation: "order-service.createOrder",
		Body:      map[string]any{"customerId": "cust_1"},
		Tags:      []string{"qcom"},
	}
	body, err := json.Marshal(ex)
	require.NoError(t, err)

	createResp := doReqBodyReal(t, ts, http.MethodPost, "/v1/examples", "test-token", body)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created domain.SavedExample
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))
	assert.Equal(t, "create-qcom-order", created.ID)

	getResp := doReq(t, ts, http.MethodGet, "/v1/examples/create-qcom-order", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, getResp.StatusCode)

	listResp := doReq(t, ts, http.MethodGet, "/v1/examples?operation=order-service.createOrder", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var list []domain.SavedExample
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	assert.Len(t, list, 1)

	created.Description = "updated"
	updateBody, err := json.Marshal(created)
	require.NoError(t, err)
	updateResp := doReqBodyReal(t, ts, http.MethodPut, "/v1/examples/create-qcom-order", "test-token", updateBody)
	require.Equal(t, http.StatusOK, updateResp.StatusCode)
	var updated domain.SavedExample
	require.NoError(t, json.NewDecoder(updateResp.Body).Decode(&updated))
	assert.Equal(t, "updated", updated.Description)

	deleteResp := doReq(t, ts, http.MethodDelete, "/v1/examples/create-qcom-order", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNoContent, deleteResp.StatusCode)

	getAfterDelete := doReq(t, ts, http.MethodGet, "/v1/examples/create-qcom-order", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, getAfterDelete.StatusCode)
	assert.Equal(t, errs.ExampleNotFound, decodeErrBody(t, getAfterDelete).Code)
}

func TestExampleUpdateIDMismatchIsInvalid(t *testing.T) {
	_, ts := newTestServer(t, nil)
	body, err := json.Marshal(domain.SavedExample{ID: "some-other-id", Operation: "order-service.createOrder"})
	require.NoError(t, err)
	resp := doReqBodyReal(t, ts, http.MethodPut, "/v1/examples/target-id", "test-token", body)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, errs.Invalid, decodeErrBody(t, resp).Code)
}

func TestExamplesForOperations_RepeatedOpParam(t *testing.T) {
	_, ts := newTestServer(t, nil)

	body, err := json.Marshal(domain.SavedExample{ID: "ex-a", Operation: "order-service.createOrder"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, doReqBodyReal(t, ts, http.MethodPost, "/v1/examples", "test-token", body).StatusCode)

	body2, err := json.Marshal(domain.SavedExample{ID: "ex-b", Operation: "order-service.getOrder"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, doReqBodyReal(t, ts, http.MethodPost, "/v1/examples", "test-token", body2).StatusCode)

	resp := doReq(t, ts, http.MethodGet, "/v1/examples/for-operations?op=order-service.createOrder&op=order-service.getOrder", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list []domain.SavedExample
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	assert.Len(t, list, 2)
}

func TestExampleDeleteNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodDelete, "/v1/examples/does-not-exist", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, errs.ExampleNotFound, decodeErrBody(t, resp).Code)
}

// Malformed-body coverage for the example routes lives in
// TestMalformedBodyIsInvalid (handlers_extra_test.go), which enumerates
// every JSON-body route across the whole package.
