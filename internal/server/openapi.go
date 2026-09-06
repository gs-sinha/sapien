package server

import (
	"encoding/json"
	"strings"
)

// openAPIPath renders a chi route pattern as an OpenAPI 3.1 path template.
// Every pattern in routeTable uses plain {param} segments except the doc
// wildcard, "/v1/docs/{service}/*", whose trailing "*" (chi's catch-all
// syntax, not valid in OpenAPI) becomes "{path}".
func openAPIPath(pattern string) string {
	if strings.HasSuffix(pattern, "/*") {
		return strings.TrimSuffix(pattern, "*") + "{path}"
	}
	return pattern
}

// looseSchema is deliberately untyped: PLAN §22 asks only for "paths +
// operationIds", with schemas loose enough for Sapien to index itself.
var looseSchema = map[string]any{"type": "object"}

// buildOpenAPI takes routes explicitly (rather than reading the routeTable
// package var) so that Server.handleOpenAPI, reachable from routeTable's own
// initializer via the "getOpenAPI" entry's handler closure, does not itself
// reference routeTable -- which would make routeTable depend on itself and
// fail to compile ("initialization cycle for routeTable").
func buildOpenAPI(routes []routeDef) []byte {
	paths := map[string]map[string]any{}
	for _, rt := range routes {
		p := openAPIPath(rt.Pattern)
		methods, ok := paths[p]
		if !ok {
			methods = map[string]any{}
			paths[p] = methods
		}
		methods[strings.ToLower(rt.Method)] = map[string]any{
			"operationId": rt.OperationID,
			"summary":     rt.Summary,
			"responses": map[string]any{
				"200": map[string]any{
					"description": "OK",
					"content": map[string]any{
						"application/json": map[string]any{"schema": looseSchema},
					},
				},
			},
		}
	}

	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Sapien local engine API",
			"version": "1",
		},
		"paths": paths,
	}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		// routeTable and this function are both static; this can't fail.
		panic(err)
	}
	return b
}
