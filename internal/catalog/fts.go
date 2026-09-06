package catalog

import (
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// operationsFTSRow is the row shape for operations_fts. See the catalog
// package doc comment for the exact column contract.
type operationsFTSRow struct {
	ID          string
	Service     string
	OpID        string
	PathTokens  string
	Summary     string
	Description string
	Tags        string
	ParamNames  string
	FieldNames  string
}

// httpPath returns op's HTTP path, or "" for a non-HTTP (future protocol) operation.
func httpPath(op domain.Operation) string {
	if op.HTTP == nil {
		return ""
	}
	return op.HTTP.Path
}

// httpMethod returns op's HTTP method, or "" for a non-HTTP operation.
func httpMethod(op domain.Operation) string {
	if op.HTTP == nil {
		return ""
	}
	return op.HTTP.Method
}

// leafName returns a field path's leaf name: its last "."-segment with any
// trailing "[]" stripped. "request.body.customer.id" -> "id";
// "response.200.body.items[].riderId" -> "riderId";
// "response.200.body.items[]" -> "items".
func leafName(fieldPath string) string {
	segs := strings.Split(fieldPath, ".")
	last := segs[len(segs)-1]
	return strings.TrimSuffix(last, "[]")
}

func buildOperationsFTSRow(serviceName string, op domain.Operation, fields []domain.Field) operationsFTSRow {
	path := httpPath(op)

	opIDTokens := textutil.SplitIdent(op.ID)
	opIDTokens = append(opIDTokens, op.ID, op.RawOpID)

	pathTokens := textutil.PathTokens(path)
	pathTokens = append(pathTokens, path)

	tagsAndConcepts := make([]string, 0, len(op.Tags)+len(op.Concepts))
	tagsAndConcepts = append(tagsAndConcepts, op.Tags...)
	tagsAndConcepts = append(tagsAndConcepts, op.Concepts...)

	paramNames := make([]string, 0, len(op.Params))
	for _, p := range op.Params {
		paramNames = append(paramNames, p.Name)
	}

	leaves := make([]string, 0, len(fields))
	for _, f := range fields {
		leaves = append(leaves, leafName(f.Path))
	}

	return operationsFTSRow{
		ID:          op.ID,
		Service:     serviceName,
		OpID:        textutil.Join(opIDTokens),
		PathTokens:  textutil.Join(pathTokens),
		Summary:     op.Summary,
		Description: op.Description,
		Tags:        textutil.Join(textutil.Tokens(strings.Join(tagsAndConcepts, " "))),
		ParamNames:  textutil.Join(textutil.Tokens(strings.Join(paramNames, " "))),
		FieldNames:  textutil.Join(textutil.Tokens(strings.Join(leaves, " "))),
	}
}

// operationsTrigramRow is the row shape for operations_trigram.
type operationsTrigramRow struct {
	ID   string
	OpID string
	Path string
}

func buildOperationsTrigramRow(op domain.Operation) operationsTrigramRow {
	return operationsTrigramRow{
		ID:   op.ID,
		OpID: strings.ToLower(op.ID),
		Path: strings.ToLower(httpPath(op)),
	}
}
