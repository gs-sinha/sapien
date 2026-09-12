package registry_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/registry"
)

// coverageOpenAPI has two operations that take a body (one with a contract
// example, one without), one that takes none, and one deprecated, so a single
// build exercises every coverage branch.
const coverageOpenAPI = `
openapi: 3.0.3
info: { title: Order Service, version: "1.0" }
paths:
  /v1/orders:
    post:
      operationId: createOrder
      summary: Create an order
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties: { customerId: { type: string } }
            example: { customerId: cust_1 }
      responses: { "201": { description: created } }
  /v1/orders/{id}/cancel:
    post:
      operationId: cancelOrder
      summary: Cancel an order
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties: { reason: { type: string } }
      responses: { "200": { description: ok } }
  /v1/orders/{id}:
    get:
      operationId: getOrder
      summary: Get an order
      responses: { "200": { description: ok } }
  /v1/legacy:
    get:
      operationId: legacyList
      summary: Old listing
      deprecated: true
      responses: { "200": { description: ok } }
`

func buildCoverage(t *testing.T, files map[string]string) domain.Service {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		writeFile(t, filepath.Join(root, rel), content)
	}
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	ref := domain.ServiceRef{Name: "order-service", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	snap, err := registry.NewBuilder(ws).Build(context.Background(), ref)
	require.NoError(t, err)
	return snap.Service
}

func TestCoverage_CountsDocsAndExamplesAndNamesWhatIsMissing(t *testing.T) {
	svc := buildCoverage(t, map[string]string{
		"api/openapi.yaml": coverageOpenAPI,
		"api/service.yaml": "version: 1\nname: order-service\nconcepts: [ordering]\n",
		// Documents createOrder by operation id and getOrder by bare path.
		"api/docs/orders.md": "# Orders\n\n## Creating an order\n\n`order-service.createOrder` starts the order. Read it back with `/v1/orders/{id}`.\n",
	})

	require.NotNil(t, svc.Coverage)
	cov := *svc.Coverage
	assert.Equal(t, 3, cov.Operations, "a deprecated operation is not counted: it needs `deprecated: true`, not prose")
	assert.Equal(t, 1, cov.Deprecated, "and it says so, since the operation count beside it includes them")
	assert.Equal(t, 2, cov.Documented)
	assert.Equal(t, 2, cov.NeedExample)
	assert.Equal(t, 1, cov.WithExample)
	assert.Equal(t, 2, cov.DocSections, "the H1 and the ## section")

	undocumented := warningsWithCode(svc.Warnings, registry.CodeUndocumentedOperation)
	require.Len(t, undocumented, 1)
	assert.Contains(t, undocumented[0].Message, "order-service.cancelOrder")

	unexampled := warningsWithCode(svc.Warnings, registry.CodeMissingRequestExample)
	require.Len(t, unexampled, 1)
	assert.Contains(t, unexampled[0].Message, "order-service.cancelOrder")
	assert.Contains(t, unexampled[0].Message, "example:")

	assert.Empty(t, warningsWithCode(svc.Warnings, registry.CodeNoConcepts))
	assert.Empty(t, warningsWithCode(svc.Warnings, registry.CodeNoNarrativeDocs))
}

func TestCoverage_SavedExampleFileCountsAsAnExample(t *testing.T) {
	svc := buildCoverage(t, map[string]string{
		"api/openapi.yaml":                 coverageOpenAPI,
		"api/service.yaml":                 "version: 1\nname: order-service\nconcepts: [ordering]\n",
		"api/docs/orders.md":               "## Orders\n\n`order-service.createOrder`, `order-service.cancelOrder`, `GET /v1/orders/{id}`.\n",
		"api/examples/cancel.example.yaml": "version: 1\noperation: order-service.cancelOrder\nbody: { reason: customer_changed_mind }\n",
	})

	assert.Equal(t, 2, svc.Coverage.WithExample)
	assert.Equal(t, 3, svc.Coverage.Documented)
	assert.Empty(t, warningsWithCode(svc.Warnings, registry.CodeMissingRequestExample))
	assert.Empty(t, warningsWithCode(svc.Warnings, registry.CodeUndocumentedOperation))
}

func TestCoverage_NoDocsIsOneWarningNotOnePerOperation(t *testing.T) {
	svc := buildCoverage(t, map[string]string{
		"api/openapi.yaml": coverageOpenAPI,
		"api/service.yaml": "version: 1\nname: order-service\n",
	})

	assert.Len(t, warningsWithCode(svc.Warnings, registry.CodeNoNarrativeDocs), 1)
	assert.Empty(t, warningsWithCode(svc.Warnings, registry.CodeUndocumentedOperation),
		"a service with no docs at all has one cause, not one warning per operation")
	assert.Len(t, warningsWithCode(svc.Warnings, registry.CodeNoConcepts), 1)
	assert.Equal(t, 0, svc.Coverage.Documented)
}

func TestCoverage_ContractDerivedDocsDoNotCountAsDocumentation(t *testing.T) {
	// The contract's own tag/info descriptions become docs too. If they
	// counted, every service would be fully documented by construction.
	svc := buildCoverage(t, map[string]string{
		"api/openapi.yaml": `
openapi: 3.0.3
info:
  title: Order Service
  version: "1.0"
  description: Orders, cancellations, everything about ` + "`POST /v1/orders`" + `.
tags:
  - name: orders
    description: Operations on ` + "`POST /v1/orders`" + `.
paths:
  /v1/orders:
    post:
      operationId: createOrder
      summary: Create an order
      tags: [orders]
      responses: { "201": { description: created } }
`,
		"api/service.yaml": "version: 1\nname: order-service\nconcepts: [ordering]\n",
	})

	assert.Equal(t, 0, svc.Coverage.Documented)
	assert.Equal(t, 0, svc.Coverage.DocSections)
	assert.Len(t, warningsWithCode(svc.Warnings, registry.CodeNoNarrativeDocs), 1)
}

func TestCoverage_WarningsAreAcceptableLikeAnyOther(t *testing.T) {
	svc := buildCoverage(t, map[string]string{
		"api/openapi.yaml": coverageOpenAPI,
		"api/service.yaml": `version: 1
name: order-service
concepts: [ordering]
accepted_warnings:
  - code: UNDOCUMENTED_OPERATION
    match: cancelOrder
    reason: "Internal endpoint, called only by the ops console; documented in the ops runbook."
`,
		"api/docs/orders.md": "## Orders\n\n`order-service.createOrder`, `GET /v1/orders/{id}`.\n",
	})

	assert.Empty(t, warningsWithCode(svc.Warnings, registry.CodeUndocumentedOperation))
	require.Len(t, svc.AcceptedWarnings, 1)
	assert.Equal(t, registry.CodeUndocumentedOperation, svc.AcceptedWarnings[0].Code)
	assert.Contains(t, svc.AcceptedWarnings[0].Reason, "ops console")
}
