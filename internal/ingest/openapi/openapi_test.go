package openapi

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func mustIngestFile(t *testing.T, path string, opts Options) *Result {
	t.Helper()
	res, err := IngestFile(path, opts)
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

func findOp(t *testing.T, res *Result, id string) domain.Operation {
	t.Helper()
	for _, op := range res.Operations {
		if op.ID == id {
			return op
		}
	}
	t.Fatalf("operation %s not found; have: %v", id, opIDs(res))
	return domain.Operation{}
}

func opIDs(res *Result) []string {
	ids := make([]string, 0, len(res.Operations))
	for _, op := range res.Operations {
		ids = append(ids, op.ID)
	}
	return ids
}

func findWarning(res *Result, code string) *domain.LintWarning {
	for i := range res.Warnings {
		if res.Warnings[i].Code == code {
			return &res.Warnings[i]
		}
	}
	return nil
}

func fieldsFor(res *Result, opID string) []domain.Field {
	var out []domain.Field
	for _, f := range res.Fields {
		if f.OperationID == opID {
			out = append(out, f)
		}
	}
	return out
}

func fieldByPath(fields []domain.Field, path string) *domain.Field {
	for i := range fields {
		if fields[i].Path == path {
			return &fields[i]
		}
	}
	return nil
}

// ---- basic smoke test over the petstore fixture ----

func TestIngest_Petstore(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "api/openapi.yaml", Concepts: []string{"pets"}})

	assert.Equal(t, "Petstore", res.Title)
	assert.Equal(t, "1.0.0", res.Version)
	assert.Contains(t, res.Description, "sample API")
	assert.Len(t, res.Operations, 5)
	assert.NotEmpty(t, res.Schemas)
	assert.NotEmpty(t, res.Fields)
	assert.NotEmpty(t, res.Aliases)

	for _, op := range res.Operations {
		assert.Equal(t, "petstore", op.ServiceID)
		assert.Equal(t, []string{"pets"}, op.Concepts)
		assert.NotEmpty(t, op.Hash)
		assert.Equal(t, "api/openapi.yaml", op.Source.File)
		assert.NotZero(t, op.Source.Line, "operation %s should have a source line", op.ID)
	}
}

// ---- operation ID synthesis + collisions ----

func TestOperationIDSynthesis(t *testing.T) {
	cases := []struct {
		method, path, want string
	}{
		{"GET", "/v1/allocations/stats", "get_v1_allocations_stats"},
		{"GET", "/v1/riders/{riderId}", "get_v1_riders_riderId"},
		{"POST", "/v1/orders", "post_v1_orders"},
	}
	for _, c := range cases {
		got := synthesizeOpID(c.method, c.path)
		assert.Equal(t, c.want, got, "%s %s", c.method, c.path)
	}
}

func TestIngest_SynthesizedOperationID(t *testing.T) {
	res := mustIngestFile(t, "testdata/minimal-3.1.yaml", Options{ServiceID: "fleet", File: "api/openapi.yaml"})

	op := findOp(t, res, "fleet.get_v1_health")
	assert.True(t, op.Synthesized)
	assert.Empty(t, op.RawOpID)

	w := findWarning(res, "SYNTHESIZED_OPERATION_ID")
	require.NotNil(t, w, "expected a SYNTHESIZED_OPERATION_ID warning")
	assert.NotNil(t, w.Source)
}

func TestDedupeID_Collisions(t *testing.T) {
	used := map[string]bool{}
	first, collided := dedupeID("svc.createThing", used)
	assert.Equal(t, "svc.createThing", first)
	assert.False(t, collided)

	second, collided := dedupeID("svc.createThing", used)
	assert.Equal(t, "svc.createThing_2", second)
	assert.True(t, collided)

	third, collided := dedupeID("svc.createThing", used)
	assert.Equal(t, "svc.createThing_3", third)
	assert.True(t, collided)
}

func TestIngest_DuplicateOperationIDWarning(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info:
  title: Dup
  version: 1.0.0
paths:
  /a:
    get:
      operationId: doThing
      responses: {"200": {description: OK}}
  /b:
    get:
      operationId: doThing
      responses: {"200": {description: OK}}
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	require.Len(t, res.Operations, 2)

	ids := opIDs(res)
	sort.Strings(ids)
	assert.Equal(t, []string{"svc.doThing", "svc.doThing_2"}, ids)

	w := findWarning(res, "DUPLICATE_OPERATION_ID")
	require.NotNil(t, w)
}

// ---- alias generation ----

func TestIngest_Aliases(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})
	require.Len(t, res.Aliases, len(res.Operations))

	found := false
	for _, a := range res.Aliases {
		if a.Method == "GET" && a.Path == "/v1/pets/{petId}" {
			found = true
			assert.Equal(t, "petstore.getPet", a.OperationID)
		}
	}
	assert.True(t, found, "expected an alias for GET /v1/pets/{petId}")
}

// ---- parameter merging ----

func TestIngest_ParamMerging(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})
	op := findOp(t, res, "petstore.listPets")

	byNameIn := map[paramKey]domain.Param{}
	for _, p := range op.Params {
		byNameIn[paramKey{p.Name, string(p.In)}] = p
	}

	// path-level X-Request-Id (optional) is overridden by the operation-level one
	// (required), and both share the same slot: only one param should be present.
	require.Contains(t, byNameIn, paramKey{"X-Request-Id", "header"})
	assert.True(t, byNameIn[paramKey{"X-Request-Id", "header"}].Required, "operation-level param should win over path-level")

	require.Contains(t, byNameIn, paramKey{"limit", "query"})
	assert.False(t, byNameIn[paramKey{"limit", "query"}].Required)

	count := 0
	for _, p := range op.Params {
		if p.Name == "X-Request-Id" {
			count++
		}
	}
	assert.Equal(t, 1, count, "merged params should not duplicate the overridden path-level param")
}

func TestIngest_PathParamAlwaysRequired(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})
	op := findOp(t, res, "petstore.getPet")
	for _, p := range op.Params {
		if p.In == domain.InPath {
			assert.True(t, p.Required)
		}
	}
}

// ---- body/media selection ----

func TestIngest_RequestBodyJSON(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})
	op := findOp(t, res, "petstore.createPet")
	require.NotNil(t, op.RequestBody)
	assert.Equal(t, "application/json", op.RequestBody.ContentType)
	assert.True(t, op.RequestBody.Required)
	require.NotNil(t, op.RequestBody.Schema)
	assert.Equal(t, domain.KindObject, op.RequestBody.Schema.Kind)
}

func TestIngest_NonJSONMediaTypeWarns(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /upload:
    post:
      operationId: upload
      requestBody:
        content:
          application/octet-stream:
            schema: {type: string, format: binary}
      responses: {"200": {description: OK}}
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	op := findOp(t, res, "svc.upload")
	assert.Equal(t, "application/octet-stream", op.RequestBody.ContentType)
	w := findWarning(res, "UNSUPPORTED_MEDIA_TYPE")
	assert.NotNil(t, w)
}

// ---- response ordering ----

func TestIngest_ResponseOrdering(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})
	op := findOp(t, res, "petstore.listPets")
	require.Len(t, op.Responses, 2)
	assert.Equal(t, "200", op.Responses[0].Status)
	assert.Equal(t, "default", op.Responses[1].Status)

	op2 := findOp(t, res, "petstore.createPet")
	require.Len(t, op2.Responses, 2)
	assert.Equal(t, "201", op2.Responses[0].Status)
	assert.Equal(t, "4XX", op2.Responses[1].Status)
}

func TestClassifyCode_Ordering(t *testing.T) {
	codes := []string{"default", "4XX", "201", "200", "2XX", "500"}
	sortCodes := append([]string{}, codes...)
	sort.SliceStable(sortCodes, func(i, j int) bool {
		ti, ni := classifyCode(sortCodes[i])
		tj, nj := classifyCode(sortCodes[j])
		if ti != tj {
			return ti < tj
		}
		return ni < nj
	})
	assert.Equal(t, []string{"200", "201", "500", "2XX", "4XX", "default"}, sortCodes)
}

func TestIngest_NoSuccessResponseWarns(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /only-errors:
    get:
      operationId: onlyErrors
      responses:
        "404": {description: Not found}
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	w := findWarning(res, "NO_SUCCESS_RESPONSE")
	assert.NotNil(t, w)
}

// ---- security resolution ----

func TestIngest_SecurityResolution(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})

	// listPets has no operation-level security -> inherits document-level ApiKeyAuth.
	op := findOp(t, res, "petstore.listPets")
	require.Len(t, op.Security, 1)
	assert.Equal(t, "ApiKeyAuth", op.Security[0].Scheme)
	assert.Equal(t, "apiKey", op.Security[0].Type)

	// deletePet explicitly opts out with `security: []`.
	del := findOp(t, res, "petstore.deletePet")
	assert.Empty(t, del.Security)
}

// ---- nullable / type-array (3.1) ----

func TestIngest_NullableTypeArray(t *testing.T) {
	res := mustIngestFile(t, "testdata/minimal-3.1.yaml", Options{ServiceID: "fleet", File: "openapi.yaml"})
	fields := fieldsFor(res, "fleet.getCategory")
	f := fieldByPath(fields, "response.200.body.nickname")
	require.NotNil(t, f)
	assert.Equal(t, "string|null", f.Type)
}

// ---- oneOf variants ----

func TestIngest_OneOfVariants(t *testing.T) {
	res := mustIngestFile(t, "testdata/minimal-3.1.yaml", Options{ServiceID: "fleet", File: "openapi.yaml"})
	op := findOp(t, res, "fleet.getVehicle")
	require.Len(t, op.Responses, 2)
	schema := op.Responses[0].Schema
	require.NotNil(t, schema)
	assert.Equal(t, domain.KindOneOf, schema.Kind)
	require.Len(t, schema.Variants, 2)
	names := []string{schema.Variants[0].Name, schema.Variants[1].Name}
	sort.Strings(names)
	assert.Equal(t, []string{"Bike", "Van"}, names)

	fields := fieldsFor(res, "fleet.getVehicle")
	f := fieldByPath(fields, "response.200.body")
	require.NotNil(t, f)
	assert.Equal(t, "oneOf(Bike|Van)", f.Type)
}

// ---- cycle -> KindRef ----

func TestIngest_SelfReferenceBecomesRef(t *testing.T) {
	res := mustIngestFile(t, "testdata/minimal-3.1.yaml", Options{ServiceID: "fleet", File: "openapi.yaml"})

	var category *domain.Schema
	for i := range res.Schemas {
		if res.Schemas[i].Name == "Category" {
			category = res.Schemas[i].Schema
		}
	}
	require.NotNil(t, category, "expected a Category named schema")

	parent := category.Properties["parent"]
	require.NotNil(t, parent)
	assert.Equal(t, domain.KindRef, parent.Kind)
	assert.Equal(t, "Category", parent.Name)
	assert.Equal(t, "fleet.Category", parent.Ref)

	w := findWarning(res, "CIRCULAR_REF")
	assert.NotNil(t, w)
}

// ---- field path flattening ----

func TestIngest_FieldFlattening(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})
	fields := fieldsFor(res, "petstore.listPets")

	limit := fieldByPath(fields, "request.query.limit")
	require.NotNil(t, limit)
	assert.Equal(t, "integer", limit.Type)
	assert.False(t, limit.Required)

	reqID := fieldByPath(fields, "request.header.X-Request-Id")
	require.NotNil(t, reqID)
	assert.True(t, reqID.Required)

	items := fieldByPath(fields, "response.200.body.items")
	require.NotNil(t, items)
	assert.Equal(t, "array", items.Type)
	assert.True(t, items.Required)

	itemObj := fieldByPath(fields, "response.200.body.items[]")
	require.NotNil(t, itemObj)
	assert.Equal(t, "object", itemObj.Type)

	riderID := fieldByPath(fields, "response.200.body.items[].id")
	require.NotNil(t, riderID)
	assert.Equal(t, "string", riderID.Type)
	assert.True(t, riderID.Required)

	nextPage := fieldByPath(fields, "response.200.body.nextPage")
	require.NotNil(t, nextPage)
	assert.False(t, nextPage.Required)
}

func TestIngest_NestedBodyFields(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})
	fields := fieldsFor(res, "petstore.getPet")

	owner := fieldByPath(fields, "response.200.body.owner")
	require.NotNil(t, owner)
	assert.Equal(t, "object", owner.Type)

	ownerName := fieldByPath(fields, "response.200.body.owner.name")
	require.NotNil(t, ownerName)
	assert.Equal(t, "string", ownerName.Type)
}

// ---- hash stability ----

func TestIngest_HashStability(t *testing.T) {
	src, err := os.ReadFile("testdata/petstore-3.0.yaml")
	require.NoError(t, err)

	res1, err := Ingest(src, Options{ServiceID: "petstore", File: "openapi.yaml"})
	require.NoError(t, err)
	res2, err := Ingest(src, Options{ServiceID: "petstore", File: "openapi.yaml"})
	require.NoError(t, err)

	h1 := map[string]string{}
	for _, op := range res1.Operations {
		h1[op.ID] = op.Hash
	}
	for _, op := range res2.Operations {
		assert.Equal(t, h1[op.ID], op.Hash, "hash for %s should be stable across runs", op.ID)
		assert.NotEmpty(t, op.Hash)
	}
}

func TestIngest_HashChangesWithDescription(t *testing.T) {
	base := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      summary: Get A
      description: original
      responses: {"200": {description: OK}}
`)
	changed := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      summary: Get A
      description: changed
      responses: {"200": {description: OK}}
`)
	res1, err := Ingest(base, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	res2, err := Ingest(changed, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)

	assert.NotEqual(t, res1.Operations[0].Hash, res2.Operations[0].Hash)
}

// ---- doc extraction ----

func TestIngest_DocExtraction(t *testing.T) {
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", Options{ServiceID: "petstore", File: "openapi.yaml"})

	var info, petsTag *domain.Doc
	for i := range res.Docs {
		switch res.Docs[i].Path {
		case "contract#info":
			info = &res.Docs[i]
		case "contract#tag:pets":
			petsTag = &res.Docs[i]
		}
	}
	require.NotNil(t, info)
	assert.Equal(t, "petstore/contract#info", info.ID)
	assert.Equal(t, domain.DocSourceContractInfo, info.Source)
	require.Len(t, info.Sections, 1)
	assert.Contains(t, info.Sections[0].Body, "sample API")

	require.NotNil(t, petsTag)
	assert.Equal(t, domain.DocSourceContractTag, petsTag.Source)
	assert.Equal(t, "pets", petsTag.Title)

	// The "internal" tag has no description and should not produce a doc.
	for _, d := range res.Docs {
		assert.NotEqual(t, "contract#tag:internal", d.Path)
	}
}

func TestIngest_CustomDocParser(t *testing.T) {
	called := 0
	opts := Options{
		ServiceID: "petstore",
		File:      "openapi.yaml",
		DocParser: func(serviceID, path, title, markdown string, source domain.DocSource) domain.Doc {
			called++
			return domain.Doc{ID: serviceID + "!" + path, ServiceID: serviceID, Path: path, Title: title, Source: source}
		},
	}
	res := mustIngestFile(t, "testdata/petstore-3.0.yaml", opts)
	assert.Greater(t, called, 0)
	assert.Equal(t, "petstore!contract#info", res.Docs[0].ID)
}

// ---- warnings ----

func TestIngest_MissingSummaryWarning(t *testing.T) {
	res := mustIngestFile(t, "testdata/minimal-3.1.yaml", Options{ServiceID: "fleet", File: "openapi.yaml"})
	_ = res
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses: {"200": {description: OK}}
`)
	res2, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	w := findWarning(res2, "MISSING_SUMMARY")
	assert.NotNil(t, w)
}

// ---- error cases ----

func TestIngest_SwaggerErrors(t *testing.T) {
	_, err := IngestFile("testdata/swagger-2.0.yaml", Options{ServiceID: "svc"})
	require.Error(t, err)
	assert.Equal(t, errs.ContractParse, errs.CodeOf(err))
}

func TestIngest_BrokenYAMLErrorsWithLine(t *testing.T) {
	_, err := IngestFile("testdata/broken.yaml", Options{ServiceID: "svc"})
	require.Error(t, err)
	assert.Equal(t, errs.ContractParse, errs.CodeOf(err))
	e := errs.As(err)
	require.NotNil(t, e.Source)
	assert.NotZero(t, e.Source.Line)
}

func TestIngest_MissingServiceID(t *testing.T) {
	_, err := Ingest([]byte(`openapi: 3.0.3`), Options{})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// ---- large generated spec (benchmark guard) ----

func TestIngest_LargeGeneratedSpec(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-spec benchmark guard in -short mode")
	}
	src := generateLargeSpec(t, 1000)

	start := time.Now()
	res, err := Ingest(src, Options{ServiceID: "bench", File: "openapi.yaml"})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Len(t, res.Operations, 1000)
	assert.Less(t, elapsed.Seconds(), 2.0, "ingesting 1000 operations took %s, want < 2s", elapsed)
}

// ---- fixtures (logistics) ----

func TestIngest_LogisticsFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "..", "fixtures", "logistics")
	services := []string{"order-service", "allocation-service", "rider-service"}

	var any bool
	for _, svc := range services {
		p := filepath.Join(root, svc, "api", "openapi.yaml")
		if _, err := os.Stat(p); err == nil {
			any = true
		}
	}
	if !any {
		t.Skip("fixtures/logistics/*/api/openapi.yaml not present yet")
	}

	sawSynthesized := false
	sawDeprecated := false

	for _, svc := range services {
		p := filepath.Join(root, svc, "api", "openapi.yaml")
		if _, err := os.Stat(p); err != nil {
			t.Logf("skipping %s: %v", svc, err)
			continue
		}
		res, err := IngestFile(p, Options{ServiceID: svc})
		require.NoError(t, err, "ingesting %s", svc)
		assert.Greater(t, len(res.Operations), 0, "%s should have operations", svc)

		if svc == "allocation-service" {
			for _, op := range res.Operations {
				if op.Synthesized {
					sawSynthesized = true
				}
			}
		}
		for _, op := range res.Operations {
			if op.Deprecated {
				sawDeprecated = true
			}
		}
	}

	assert.True(t, sawSynthesized, "expected allocation-service to have a synthesized operation id")
	assert.True(t, sawDeprecated, "expected at least one deprecated operation across the logistics fixtures")
}
