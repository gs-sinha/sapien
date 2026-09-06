package catalog_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/catalog"
	"github.com/growsimplee/sapien/internal/domain"
)

func TestApply_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()

	riderChange, orderChange := applyBaseFixtures(t, c)

	assert.Equal(t, "rider-service", riderChange.Service)
	assert.ElementsMatch(t, []string{"rider-service.getRider", "rider-service.listRiders", "rider-service.getStatus"}, riderChange.Added)
	assert.Empty(t, riderChange.Changed)
	assert.Empty(t, riderChange.Removed)

	assert.Equal(t, "order-service", orderChange.Service)
	assert.ElementsMatch(t, []string{"order-service.getOrder", "order-service.createOrder", "order-service.getStatus"}, orderChange.Added)

	// Services.
	svc, err := c.GetService(ctx, "rider-service")
	require.NoError(t, err)
	assert.Equal(t, domain.SyncOK, svc.Status)
	assert.Empty(t, svc.Error)
	assert.False(t, svc.LastIndexed.IsZero())
	assert.Equal(t, 3, svc.OperationCount)
	assert.Equal(t, []string{"api/openapi.yaml"}, svc.ContractFiles)
	assert.Equal(t, "Manages riders.", svc.Description)

	services, err := c.ListServices(ctx)
	require.NoError(t, err)
	require.Len(t, services, 2)
	assert.Equal(t, "order-service", services[0].ID) // alphabetical
	assert.Equal(t, "rider-service", services[1].ID)

	// Operations.
	ops, err := c.ListOperations(ctx, "")
	require.NoError(t, err)
	require.Len(t, ops, 6)

	riderOps, err := c.ListOperations(ctx, "rider-service")
	require.NoError(t, err)
	require.Len(t, riderOps, 3)

	op, err := c.GetOperation(ctx, "rider-service.getRider")
	require.NoError(t, err)
	assert.Equal(t, "Get a rider", op.Summary)
	assert.Equal(t, "GET", op.HTTP.Method)

	// Fields.
	fields, err := c.Fields(ctx, "rider-service.getRider")
	require.NoError(t, err)
	require.Len(t, fields, 3)

	// Schemas.
	schemas, err := c.ListSchemas(ctx, "order-service")
	require.NoError(t, err)
	require.Len(t, schemas, 2)
	schema, err := c.GetSchema(ctx, "order-service", "Order")
	require.NoError(t, err)
	require.NotNil(t, schema)
	assert.Equal(t, "Order", schema.Name)

	missingSchema, err := c.GetSchema(ctx, "order-service", "NoSuchSchema")
	require.NoError(t, err)
	assert.Nil(t, missingSchema)

	// Docs.
	docs, err := c.ListDocs(ctx, "")
	require.NoError(t, err)
	require.Len(t, docs, 2)

	doc, err := c.GetDoc(ctx, "rider-service", "docs/allocation.md")
	require.NoError(t, err)
	require.Len(t, doc.Sections, 2)
	assert.Equal(t, "Overview", doc.Sections[0].Heading)
	require.Len(t, doc.Sections[0].Refs, 1)
	assert.Equal(t, domain.RefOperation, doc.Sections[0].Refs[0].Kind)
	assert.Equal(t, "rider-service.getRider", doc.Sections[0].Refs[0].Value)

	sec, parentDoc, err := c.GetDocSection(ctx, "rider-service/docs/allocation.md#overview")
	require.NoError(t, err)
	require.NotNil(t, sec)
	require.NotNil(t, parentDoc)
	assert.Equal(t, "rider-service", parentDoc.ServiceID)
	assert.Equal(t, "Riders are matched via the getRider operation.", sec.Body)

	// Flows.
	flows, err := c.ListFlows(ctx, "service", "rider-service")
	require.NoError(t, err)
	require.Len(t, flows, 1)
	assert.Equal(t, "Onboard Rider", flows[0].Name)

	allFlows, err := c.ListFlows(ctx, "", "")
	require.NoError(t, err)
	assert.Len(t, allFlows, 2)

	// Stats.
	stats, err := c.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, catalog.Stats{Services: 2, Operations: 6, Fields: 10, Docs: 2, Sections: 3, Flows: 2}, stats)
}

func TestApply_FTSRowsConsistent(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	var opsCount, ftsCount, trigramCount, docsFTSCount, sectionsCount int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations`).Scan(&opsCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_fts`).Scan(&ftsCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_trigram`).Scan(&trigramCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM doc_sections`).Scan(&sectionsCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM docs_fts`).Scan(&docsFTSCount))
	assert.Equal(t, opsCount, ftsCount)
	assert.Equal(t, opsCount, trigramCount)
	assert.Equal(t, sectionsCount, docsFTSCount)

	// Exact FTS column contract for one operation (PLAN §16; see catalog.go doc comment).
	var service, opID, pathTokens, summary, description, tags, paramNames, fieldNames string
	require.NoError(t, db.SQL().QueryRowContext(ctx, `
		SELECT service, op_id, path_tokens, summary, description, tags, param_names, field_names
		FROM operations_fts WHERE id = 'rider-service.getRider'
	`).Scan(&service, &opID, &pathTokens, &summary, &description, &tags, &paramNames, &fieldNames))

	assert.Equal(t, "rider-service", service)
	assert.Equal(t, "rider service get rider rider-service.getRider getRider", opID)
	assert.Equal(t, "v1 riders riderid rider id /v1/riders/{riderId}", pathTokens)
	assert.Equal(t, "Get a rider", summary)
	assert.Equal(t, "Fetch a single rider by ID.", description)
	assert.Equal(t, "riders allocation", tags)
	assert.Equal(t, "riderid rider id", paramNames)
	assert.Equal(t, "riderid rider id name", fieldNames)

	var trigOpID, trigPath string
	require.NoError(t, db.SQL().QueryRowContext(ctx, `
		SELECT op_id, path FROM operations_trigram WHERE id = 'rider-service.getRider'
	`).Scan(&trigOpID, &trigPath))
	assert.Equal(t, "rider-service.getrider", trigOpID)
	assert.Equal(t, "/v1/riders/{riderid}", trigPath)

	var docService, docTitle, docHeading, docBody string
	require.NoError(t, db.SQL().QueryRowContext(ctx, `
		SELECT service, title, heading, body FROM docs_fts WHERE section_id = 'rider-service/docs/allocation.md#overview'
	`).Scan(&docService, &docTitle, &docHeading, &docBody))
	assert.Equal(t, "rider-service", docService)
	assert.Equal(t, "Rider Allocation", docTitle)
	assert.Equal(t, "Overview", docHeading)
	assert.True(t, strings.Contains(docBody, "getRider"))
}

func TestApply_DiffAddChangeRemove(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	snap := riderServiceSnapshot()
	// getRider: unchanged (same hash).
	// listRiders: changed (new hash/summary).
	// getStatus: removed (omitted).
	// createRider: added.
	var newOps []domain.Operation
	for _, op := range snap.Operations {
		switch op.ID {
		case "rider-service.getStatus":
			continue // removed
		case "rider-service.listRiders":
			op.Summary = "List all riders (updated)"
			op.Hash = "h-list-riders-2"
		}
		newOps = append(newOps, op)
	}
	newOps = append(newOps, domain.Operation{
		ID: "rider-service.createRider", ServiceID: "rider-service", Protocol: domain.ProtocolHTTP,
		HTTP: &domain.HTTPBinding{Method: "POST", Path: "/v1/riders"}, RawOpID: "createRider",
		Summary: "Create a rider", Hash: "h-create-rider-1",
	})
	snap.Operations = newOps
	snap.Aliases = append(snap.Aliases, domain.Alias{Method: "POST", Path: "/v1/riders", OperationID: "rider-service.createRider"})

	change, err := c.Apply(ctx, snap)
	require.NoError(t, err)
	assert.Equal(t, []string{"rider-service.createRider"}, change.Added)
	assert.Equal(t, []string{"rider-service.getStatus"}, change.Removed)
	assert.Equal(t, []string{"rider-service.listRiders"}, change.Changed)

	// order-service must be untouched.
	orderOps, err := c.ListOperations(ctx, "order-service")
	require.NoError(t, err)
	assert.Len(t, orderOps, 3)

	riderOps, err := c.ListOperations(ctx, "rider-service")
	require.NoError(t, err)
	require.Len(t, riderOps, 3)

	updated, err := c.GetOperation(ctx, "rider-service.listRiders")
	require.NoError(t, err)
	assert.Equal(t, "List all riders (updated)", updated.Summary)

	_, err = c.GetOperation(ctx, "rider-service.getStatus")
	require.Error(t, err)

	// FTS rows stay consistent with operations after the diff.
	var opsCount, ftsCount, trigramCount int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations`).Scan(&opsCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_fts`).Scan(&ftsCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_trigram`).Scan(&trigramCount))
	assert.Equal(t, opsCount, ftsCount)
	assert.Equal(t, opsCount, trigramCount)
	assert.Equal(t, 6, opsCount) // 3 order-service (untouched) + 3 rider-service (get/list/create)

	// The removed op's own alias row is gone (its exact path/method pair
	// no longer resolves to it; "GET /v1/riders/status" still resolves, via
	// templated matching, to getRider's "/v1/riders/{riderId}" alias, which
	// is correct: a template's wildcard segment matches any value).
	var aliasCount int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM operation_aliases WHERE operation_id = 'rider-service.getStatus'
	`).Scan(&aliasCount))
	assert.Zero(t, aliasCount)

	_, err = c.ResolveOperation(ctx, "rider-service.getStatus")
	require.Error(t, err)
}

func TestRemoveService_Cascades(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	require.NoError(t, c.RemoveService(ctx, "rider-service"))

	_, err := c.GetService(ctx, "rider-service")
	require.Error(t, err)

	ops, err := c.ListOperations(ctx, "rider-service")
	require.NoError(t, err)
	assert.Empty(t, ops)

	docs, err := c.ListDocs(ctx, "rider-service")
	require.NoError(t, err)
	assert.Empty(t, docs)

	flows, err := c.ListFlows(ctx, "service", "rider-service")
	require.NoError(t, err)
	assert.Empty(t, flows)

	// order-service survives untouched.
	orderOps, err := c.ListOperations(ctx, "order-service")
	require.NoError(t, err)
	assert.Len(t, orderOps, 3)

	// No orphaned FTS rows.
	var ftsCount, trigramCount, docsFTSCount int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_fts WHERE id LIKE 'rider-service.%'`).Scan(&ftsCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_trigram WHERE id LIKE 'rider-service.%'`).Scan(&trigramCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM docs_fts WHERE service = 'rider-service'`).Scan(&docsFTSCount))
	assert.Zero(t, ftsCount)
	assert.Zero(t, trigramCount)
	assert.Zero(t, docsFTSCount)

	var opsRemaining, docsRemaining, flowsRemaining int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations`).Scan(&opsRemaining))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM docs`).Scan(&docsRemaining))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM flows`).Scan(&flowsRemaining))
	assert.Equal(t, 3, opsRemaining)
	assert.Equal(t, 1, docsRemaining)
	assert.Equal(t, 1, flowsRemaining)
}

func TestMarkServiceError_KeepsOperations(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	svcBefore, err := c.GetService(ctx, "rider-service")
	require.NoError(t, err)

	require.NoError(t, c.MarkServiceError(ctx, *svcBefore, "contract parse error: unexpected token"))

	svcAfter, err := c.GetService(ctx, "rider-service")
	require.NoError(t, err)
	assert.Equal(t, domain.SyncError, svcAfter.Status)
	assert.Equal(t, "contract parse error: unexpected token", svcAfter.Error)
	assert.Equal(t, svcBefore.LastIndexed.Unix(), svcAfter.LastIndexed.Unix(), "last good index time preserved")

	ops, err := c.ListOperations(ctx, "rider-service")
	require.NoError(t, err)
	assert.Len(t, ops, 3, "the last good catalog is kept")
}

func TestMarkServiceError_CreatesMissingService(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()

	svc := domain.Service{ID: "new-service", Name: "new-service"}
	require.NoError(t, c.MarkServiceError(ctx, svc, "could not fetch contract"))

	got, err := c.GetService(ctx, "new-service")
	require.NoError(t, err)
	assert.Equal(t, domain.SyncError, got.Status)
	assert.Equal(t, "could not fetch contract", got.Error)
	assert.Equal(t, 0, got.OperationCount)
}
