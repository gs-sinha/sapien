package openapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestComponentName(t *testing.T) {
	assert.Equal(t, "", componentName(""))
	assert.Equal(t, "", componentName("Foo"))
	assert.Equal(t, "Foo", componentName("#/components/schemas/Foo"))
	assert.Equal(t, "", componentName("#/components/schemas/"))
}

func TestNodeToValue_Nil(t *testing.T) {
	assert.Nil(t, nodeToValue(nil))
}

func TestIngest_MaxDepthCap(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  level0:
                    type: object
                    properties:
                      level1:
                        type: object
                        properties:
                          level2:
                            type: string
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml", MaxDepth: 3})
	require.NoError(t, err)
	op := findOp(t, res, "svc.getA")
	root := op.Responses[0].Schema
	require.NotNil(t, root)
	level0 := root.Properties["level0"]
	require.NotNil(t, level0)
	assert.Equal(t, domain.KindObject, level0.Kind)
	level1 := level0.Properties["level1"]
	require.NotNil(t, level1)
	assert.Equal(t, domain.KindAny, level1.Kind, "nesting beyond MaxDepth should fall back to KindAny")
	assert.Nil(t, level1.Properties, "a depth-capped schema should not have been expanded further")
}

func TestIngest_AdditionalPropertiesSchema(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                additionalProperties:
                  type: string
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	op := findOp(t, res, "svc.getA")
	schema := op.Responses[0].Schema
	require.NotNil(t, schema.AdditionalProperties)
	assert.Equal(t, domain.KindString, schema.AdditionalProperties.Kind)
}

func TestIngest_SchemaFlags(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:
                    type: string
                    readOnly: true
                  secret:
                    type: string
                    writeOnly: true
                    deprecated: true
                  status:
                    type: string
                    default: active
                  kind:
                    type: string
                    enum: [a, b]
                    example: a
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	op := findOp(t, res, "svc.getA")
	schema := op.Responses[0].Schema

	assert.True(t, schema.Properties["id"].ReadOnly)
	assert.True(t, schema.Properties["secret"].WriteOnly)
	assert.True(t, schema.Properties["secret"].Deprecated)
	assert.Equal(t, "active", schema.Properties["status"].Default)
	assert.Equal(t, []any{"a", "b"}, schema.Properties["kind"].Enum)
	assert.Equal(t, "a", schema.Properties["kind"].Example)
}

func TestIngest_AllOfAndAnonymousAnyOf(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                anyOf:
                  - type: string
                  - type: integer
    post:
      operationId: postA
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - type: object
                  properties:
                    a: {type: string}
                - type: object
                  properties:
                    b: {type: string}
      responses:
        "200": {description: OK}
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)

	get := findOp(t, res, "svc.getA")
	anySchema := get.Responses[0].Schema
	require.NotNil(t, anySchema)
	assert.Equal(t, domain.KindAnyOf, anySchema.Kind)
	require.Len(t, anySchema.Variants, 2)
	assert.Equal(t, domain.KindString, anySchema.Variants[0].Kind)
	assert.Equal(t, "", anySchema.Variants[0].Name)

	fields := fieldsFor(res, "svc.getA")
	f := fieldByPath(fields, "response.200.body")
	require.NotNil(t, f)
	assert.Equal(t, "anyOf(string|integer)", f.Type)

	post := findOp(t, res, "svc.postA")
	allSchema := post.RequestBody.Schema
	require.NotNil(t, allSchema)
	assert.Equal(t, domain.KindAllOf, allSchema.Kind)
	require.Len(t, allSchema.Variants, 2)
}

func TestIngest_ResponseOnlyDefault(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        default:
          description: fallback
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	op := findOp(t, res, "svc.getA")
	require.Len(t, op.Responses, 1)
	assert.Equal(t, "default", op.Responses[0].Status)
	w := findWarning(res, "NO_SUCCESS_RESPONSE")
	assert.NotNil(t, w)
}

func TestIngest_ScalarResponseBody(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: string
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	fields := fieldsFor(res, "svc.getA")
	f := fieldByPath(fields, "response.200.body")
	require.NotNil(t, f)
	assert.Equal(t, "string", f.Type)
}

func TestIngest_ArrayRootResponseBody(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  type: string
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	fields := fieldsFor(res, "svc.getA")
	f := fieldByPath(fields, "response.200.body[]")
	require.NotNil(t, f)
	assert.Equal(t, "string", f.Type)
}

func TestIngest_ResponseHeaderSchema(t *testing.T) {
	src := []byte(`
openapi: 3.0.3
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          headers:
            X-Rate-Limit:
              schema:
                type: integer
          content:
            application/json:
              schema:
                type: object
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	op := findOp(t, res, "svc.getA")
	require.NotNil(t, op.Responses[0].Headers)
	h := op.Responses[0].Headers["X-Rate-Limit"]
	require.NotNil(t, h)
	assert.Equal(t, domain.KindInteger, h.Kind)
}

func TestIngest_UnknownSchemaType(t *testing.T) {
	src := []byte(`
openapi: 3.1.0
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  weird:
                    type: fancypants
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	op := findOp(t, res, "svc.getA")
	weird := op.Responses[0].Schema.Properties["weird"]
	require.NotNil(t, weird)
	assert.Equal(t, domain.KindAny, weird.Kind)
}

func TestIngest_InfiniteCircularRefWarnsFromBuildError(t *testing.T) {
	src := []byte(`
openapi: 3.1.0
info: {title: X, version: "1.0"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Node"
components:
  schemas:
    Node:
      type: object
      required: [id, parent]
      properties:
        id:
          type: string
        parent:
          $ref: "#/components/schemas/Node"
`)
	res, err := Ingest(src, Options{ServiceID: "svc", File: "openapi.yaml"})
	require.NoError(t, err)
	w := findWarning(res, "CIRCULAR_REF")
	require.NotNil(t, w)
}
