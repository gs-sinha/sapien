package openapi

import (
	"fmt"

	"github.com/growsimplee/sapien/internal/domain"
)

// flattenOperationFields flattens every param, the request body, and every response
// body of op into Field rows, per PLAN.md §5's field path convention:
// request.path.<name>, request.query.<name>, request.header.<name>,
// request.body.<a>.<b>, arrays as <a>[] then <a>[].<b>, response.<status>.body.<a>...
func flattenOperationFields(opID string, op *domain.Operation) []domain.Field {
	var out []domain.Field
	emit := func(f domain.Field) {
		f.OperationID = opID
		out = append(out, f)
	}

	for _, p := range op.Params {
		path := fmt.Sprintf("request.%s.%s", p.In, p.Name)
		if p.Schema == nil {
			emit(domain.Field{Path: path, Type: string(domain.KindAny), Required: p.Required, Description: p.Description})
			continue
		}
		emitSchema(path, p.Schema, p.Required, emit)
	}

	if op.RequestBody != nil && op.RequestBody.Schema != nil {
		flattenRoot("request.body", op.RequestBody.Schema, emit)
	}

	for _, r := range op.Responses {
		if r.Schema == nil {
			continue
		}
		flattenRoot(fmt.Sprintf("response.%s.body", r.Status), r.Schema, emit)
	}

	return out
}

// emitSchema emits a Field row for path describing schema itself, then recurses into
// object properties (as path.<name>) and array items (as path[], then path[].<name>).
// oneOf/anyOf/allOf and ref/scalar schemas are terminal: their compact type string
// (e.g. "oneOf(Bike|Van)") is the whole story.
func emitSchema(path string, schema *domain.Schema, required bool, emit func(domain.Field)) {
	if schema == nil {
		return
	}
	emit(domain.Field{
		Path:        path,
		Type:        schemaTypeString(schema),
		Description: schema.Description,
		Required:    required,
		Enum:        schema.Enum,
		Format:      schema.Format,
	})

	switch schema.Kind {
	case domain.KindObject:
		for _, name := range schema.PropertyOrder {
			prop := schema.Properties[name]
			if prop == nil {
				continue
			}
			emitSchema(path+"."+name, prop, containsStr(schema.Required, name), emit)
		}
	case domain.KindArray:
		if schema.Items != nil {
			emitSchema(path+"[]", schema.Items, false, emit)
		}
	}
}

// flattenRoot flattens a request/response body root schema. Unlike emitSchema, it
// does not emit a row for basePath itself when the root is an object or array: there
// is no bare "request.body" field, only its properties/items.
func flattenRoot(basePath string, schema *domain.Schema, emit func(domain.Field)) {
	if schema == nil {
		return
	}
	switch schema.Kind {
	case domain.KindObject:
		for _, name := range schema.PropertyOrder {
			prop := schema.Properties[name]
			if prop == nil {
				continue
			}
			emitSchema(basePath+"."+name, prop, containsStr(schema.Required, name), emit)
		}
	case domain.KindArray:
		if schema.Items != nil {
			emitSchema(basePath+"[]", schema.Items, false, emit)
		}
	default:
		emitSchema(basePath, schema, false, emit)
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
