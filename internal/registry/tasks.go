package registry

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
)

var nonTaskID = regexp.MustCompile(`[^a-z0-9]+`)

// normalizeTasks expands the concise task syntax, qualifies bare operation
// IDs, and reports structural mistakes without preventing the rest of a
// service from being indexed.
func normalizeTasks(service string, authored []domain.Task, operations []domain.Operation) ([]domain.Task, domain.TaskCoverage, []domain.LintWarning) {
	known := make(map[string]bool, len(operations))
	for _, op := range operations {
		known[op.ID] = true
	}

	out := make([]domain.Task, 0, len(authored))
	seen := map[string]bool{}
	warnings := []domain.LintWarning{}
	coverage := domain.TaskCoverage{Tasks: len(authored)}

	for i, raw := range authored {
		t := raw
		if strings.TrimSpace(t.Phrase) != "" {
			for _, phrase := range strings.Split(t.Phrase, ",") {
				if phrase = strings.TrimSpace(phrase); phrase != "" {
					t.Phrases = append(t.Phrases, phrase)
				}
			}
		}
		t.Phrase = ""
		if strings.TrimSpace(t.Operation) != "" {
			t.Targets = append(t.Targets, domain.TaskTarget{Operation: t.Operation})
		}
		t.Operation = ""
		if t.ID == "" {
			seed := "task"
			if len(t.Phrases) > 0 {
				seed = t.Phrases[0]
			}
			t.ID = strings.Trim(nonTaskID.ReplaceAllString(strings.ToLower(seed), "-"), "-")
			if t.ID == "" {
				t.ID = fmt.Sprintf("task-%d", i+1)
			}
		}
		if seen[t.ID] {
			warnings = append(warnings, domain.LintWarning{Code: "DUPLICATE_TASK_ID", Message: fmt.Sprintf("task id %q is declared more than once", t.ID), Source: taskSource(t.ID)})
		}
		seen[t.ID] = true

		for j := range t.Targets {
			t.Targets[j].Operation = qualifyOperation(service, t.Targets[j].Operation)
			if !known[t.Targets[j].Operation] {
				warnings = append(warnings, domain.LintWarning{Code: "STALE_TASK_TARGET", Message: fmt.Sprintf("task %q targets unknown operation %q", t.ID, t.Targets[j].Operation), Source: taskSource(t.ID)})
			}
		}
		if len(t.Targets) > 1 {
			for _, target := range t.Targets {
				if strings.TrimSpace(target.When) == "" {
					warnings = append(warnings, domain.LintWarning{Code: "AMBIGUOUS_TASK", Message: fmt.Sprintf("task %q has multiple operations; give every target a when condition", t.ID), Source: taskSource(t.ID)})
					break
				}
			}
		}
		for j := range t.Tests {
			coverage.Assertions++
			if t.Tests[j].TopK == 0 {
				t.Tests[j].TopK = 3
			}
			for k := range t.Tests[j].ExpectAny {
				t.Tests[j].ExpectAny[k] = qualifyOperation(service, t.Tests[j].ExpectAny[k])
				if !known[t.Tests[j].ExpectAny[k]] {
					warnings = append(warnings, domain.LintWarning{Code: "STALE_TASK_TARGET", Message: fmt.Sprintf("task %q test expects unknown operation %q", t.ID, t.Tests[j].ExpectAny[k]), Source: taskSource(t.ID)})
				}
			}
		}
		out = append(out, t)
	}
	return out, coverage, warnings
}

func qualifyOperation(service, operation string) string {
	operation = strings.TrimSpace(operation)
	if operation == "" || strings.Contains(operation, ".") {
		return operation
	}
	return service + "." + operation
}

func taskSource(id string) *domain.SourceLoc {
	return &domain.SourceLoc{File: "service.yaml", Pointer: "/tasks/" + id}
}
