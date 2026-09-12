package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/example"
)

// Coverage lint codes. They answer the question a service's own contract
// cannot: can an agent in another repo, reading only what Sapien serves,
// work out when to call this operation and what a call looks like? A
// contract that lints clean can still be undocumented and unexampled, which
// is exactly the feedback onboarded services kept getting.
const (
	// CodeUndocumentedOperation: no api/docs section references the operation.
	CodeUndocumentedOperation = "UNDOCUMENTED_OPERATION"
	// CodeNoNarrativeDocs: the package has no api/docs/*.md at all, so every
	// operation is undocumented. Reported once instead of once per operation.
	CodeNoNarrativeDocs = "NO_NARRATIVE_DOCS"
	// CodeMissingRequestExample: the operation takes a request body and
	// nothing anywhere shows one.
	CodeMissingRequestExample = "MISSING_REQUEST_EXAMPLE"
	// CodeNoConcepts: service.yaml declares no concepts, so searches by
	// domain term miss the service.
	CodeNoConcepts = "NO_CONCEPTS"
)

// maxPerCodeWarnings caps how many operations a per-operation coverage code
// names before it collapses into one "and N more" warning. A 200-operation
// service that has just been onboarded would otherwise answer add_service
// with 200 warnings and bury the ones that need a decision.
const maxPerCodeWarnings = 20

// coverage measures documentation and example coverage for a built service
// and returns the lint warnings that go with it. docs are the service's
// final docs (contract-derived ones included; only file-sourced ones count
// as narrative documentation) and examplesDir is the package's examples/
// directory, or "" when it has none.
//
// Warnings are produced before acceptance is applied, so a service can accept
// any of them in service.yaml with a reason like any other warning -- an
// internal endpoint nobody outside the team calls is a legitimate thing to
// leave undocumented, as long as somebody says so on the record.
func coverage(ops []domain.Operation, docs []domain.Doc, meta domain.ServiceMetadata, examplesDir string) (domain.DocCoverage, []domain.LintWarning) {
	documented, sections := documentedOperations(docs)
	exampled := operationsWithSavedExample(examplesDir)

	cov := domain.DocCoverage{Operations: len(ops), DocSections: sections}
	var warnings []domain.LintWarning
	var undocumented, unexampled []domain.Operation

	for _, op := range ops {
		if op.Deprecated {
			// A retired endpoint needs neither prose nor a payload; what it
			// needs is `deprecated: true`, which it has.
			cov.Operations--
			cov.Deprecated++
			continue
		}
		if tmpl := pathTemplate(op); documented[op.ID] || (tmpl != "" && documented[tmpl]) {
			cov.Documented++
		} else {
			undocumented = append(undocumented, op)
		}
		if op.RequestBody == nil {
			continue
		}
		cov.NeedExample++
		if hasContractExample(op) || exampled[op.ID] {
			cov.WithExample++
		} else {
			unexampled = append(unexampled, op)
		}
	}

	switch {
	case sections == 0 && len(ops) > 0:
		// Every operation is undocumented for one reason; say it once.
		warnings = append(warnings, domain.LintWarning{
			Code:    CodeNoNarrativeDocs,
			Message: fmt.Sprintf("no api/docs/*.md: %d operations are indexed with their contract only, so a caller can find them but cannot find out when to use them or what happens if they do", len(ops)),
			Source:  &domain.SourceLoc{File: "api/docs"},
		})
	default:
		warnings = append(warnings, perOperationWarnings(undocumented, CodeUndocumentedOperation,
			func(op domain.Operation) string {
				return fmt.Sprintf("no api/docs section mentions %s (%s); an agent reading the docs will not learn when to call it", op.ID, pathOf(op))
			},
			func(n int) string {
				return fmt.Sprintf("%d more operations are not mentioned in any api/docs section", n)
			})...)
	}

	warnings = append(warnings, perOperationWarnings(unexampled, CodeMissingRequestExample,
		func(op domain.Operation) string {
			return fmt.Sprintf("%s (%s) takes a request body with no example; add `example:` under its requestBody content in the contract, or save a verified one from a real call", op.ID, pathOf(op))
		},
		func(n int) string {
			return fmt.Sprintf("%d more operations take a request body with no example anywhere", n)
		})...)

	if len(meta.Concepts) == 0 {
		warnings = append(warnings, domain.LintWarning{
			Code:    CodeNoConcepts,
			Message: "service.yaml declares no `concepts:`; a caller who searches by domain term rather than endpoint name will not find this service",
			Source:  &domain.SourceLoc{File: serviceMetadataFile},
		})
	}

	return cov, warnings
}

// perOperationWarnings names up to maxPerCodeWarnings operations individually
// and collapses the rest into one aggregate warning, so the list stays
// actionable on a service that has not been documented at all.
func perOperationWarnings(ops []domain.Operation, code string, message func(domain.Operation) string, aggregate func(int) string) []domain.LintWarning {
	out := make([]domain.LintWarning, 0, len(ops))
	for i, op := range ops {
		if i == maxPerCodeWarnings {
			out = append(out, domain.LintWarning{Code: code, Message: aggregate(len(ops) - i)})
			break
		}
		w := domain.LintWarning{Code: code, Message: message(op)}
		if op.Source.File != "" {
			src := op.Source
			w.Source = &src
		}
		out = append(out, w)
	}
	return out
}

// documentedOperations returns the set of operation ids and path templates
// that the service's narrative docs reference, plus the number of narrative
// sections. Only file-sourced docs count: a contract's own tag and info
// descriptions are the contract restating itself, and counting them would
// make every service fully documented by construction.
func documentedOperations(docs []domain.Doc) (map[string]bool, int) {
	refs := map[string]bool{}
	sections := 0
	for _, doc := range docs {
		if doc.Source != domain.DocSourceFile {
			continue
		}
		for _, sec := range doc.Sections {
			sections++
			for _, ref := range sec.Refs {
				// A path mention without a method ("`/v1/orders`") documents
				// the endpoint without saying which operation; credit every
				// operation on that path rather than none of them.
				if ref.Kind == domain.RefOperation || ref.Kind == domain.RefPath {
					refs[ref.Value] = true
				}
			}
		}
	}
	return refs, sections
}

// operationsWithSavedExample reads the operation id out of every
// <id>.example.yaml in the package's examples directory. A file that does not
// parse is skipped: it is the example store's job to report that, not
// coverage's.
func operationsWithSavedExample(examplesDir string) map[string]bool {
	out := map[string]bool{}
	if examplesDir == "" {
		return out
	}
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		return out
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if filepath.Ext(name) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(examplesDir, name))
		if err != nil {
			continue
		}
		ex, err := example.Parse(data, filepath.Join(examplesDir, name))
		if err != nil || ex == nil {
			continue
		}
		out[ex.Operation] = true
	}
	return out
}

func hasContractExample(op domain.Operation) bool {
	if op.RequestBody == nil {
		return false
	}
	for _, ex := range op.RequestBody.Examples {
		if ex.Value != nil {
			return true
		}
	}
	// A contract can also pin the payload on the schema itself
	// (`schema: {example: {...}}`), which is just as good to a caller.
	return op.RequestBody.Schema != nil && op.RequestBody.Schema.Example != nil
}

// pathOf labels an operation for a human reading a warning.
func pathOf(op domain.Operation) string {
	if op.HTTP == nil {
		return op.ID
	}
	return op.HTTP.Method + " " + op.HTTP.Path
}

// pathTemplate is the key a bare path mention in a doc ("`/v1/orders/{id}`",
// with no method) is indexed under: docs.ExtractRefs resolves those to
// RefPath carrying the template alone, so a method-qualified key would never
// match one.
func pathTemplate(op domain.Operation) string {
	if op.HTTP == nil {
		return ""
	}
	return op.HTTP.Path
}
