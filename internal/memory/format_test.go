package memory_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/memory"
)

// planExample is PLAN.md §10's worked example, byte for byte (the memory
// file body under the heading "## 10. Memory domain model").
const planExample = `---
id: mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8
type: semantic
scope: workspace
subject:
  operation: rider-service.getRider
  field: response.200.body.qcomSkill
tags: [qcom, allocation]
source: { kind: user }
created: 2026-09-05T10:20:00Z
updated: 2026-09-05T10:20:00Z
status: active
---
qcomSkill indicates whether the rider is eligible for quick-commerce (QCOM) orders.
It does not indicate the rider is online or available.

Implications: QCOM allocation should only select riders with qcomSkill=true.
`

func TestParse_PLANExample(t *testing.T) {
	m, err := memory.Parse([]byte(planExample))
	require.NoError(t, err)

	assert.Equal(t, "mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8", m.ID)
	assert.Equal(t, domain.MemorySemantic, m.Type)
	assert.Equal(t, domain.ScopeWorkspace, m.Scope)
	assert.Equal(t, "rider-service.getRider", m.Subject.Operation)
	assert.Equal(t, "response.200.body.qcomSkill", m.Subject.Field)
	assert.Equal(t, []string{"qcom", "allocation"}, m.Tags)
	assert.Equal(t, "user", m.Source.Kind)
	assert.Equal(t, domain.MemoryActive, m.Status)
	assert.Equal(t, time.Date(2026, 9, 5, 10, 20, 0, 0, time.UTC), m.Created.UTC())
	assert.Equal(t, time.Date(2026, 9, 5, 10, 20, 0, 0, time.UTC), m.Updated.UTC())
	assert.Contains(t, m.Text, "qcomSkill indicates whether the rider is eligible")
	assert.Contains(t, m.Text, "Implications: QCOM allocation should only select riders with qcomSkill=true.")
	// Body must be trimmed (no leading/trailing blank lines).
	assert.False(t, m.Text[0] == '\n')
	assert.False(t, m.Text[len(m.Text)-1] == '\n')
}

// TestFormatParseRoundTrip checks that Format(m), reparsed, yields a memory
// with the same semantic content as m (field order/flow-vs-block style is an
// internal Format detail, not something Parse needs to reproduce byte for
// byte).
func TestFormatParseRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		m    domain.Memory
	}{
		{
			name: "full",
			m: domain.Memory{
				ID:    "mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8",
				Type:  domain.MemorySemantic,
				Scope: domain.ScopeWorkspace,
				Subject: domain.Subject{
					Operation: "rider-service.getRider",
					Field:     "response.200.body.qcomSkill",
				},
				Tags:    []string{"qcom", "allocation"},
				Source:  domain.MemorySource{Kind: "user"},
				Status:  domain.MemoryActive,
				Created: time.Date(2026, 9, 5, 10, 20, 0, 0, time.UTC),
				Updated: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC),
				Resolved: &domain.ResolvedSubject{
					Method:        "GET",
					Path:          "/v1/riders/{riderId}",
					OperationHash: "abc123",
					ResolvedAt:    time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC),
				},
				Text: "qcomSkill indicates eligibility for QCOM orders.",
			},
		},
		{
			name: "minimal (only id, scope, created, body required)",
			m: domain.Memory{
				ID:      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3V9",
				Scope:   domain.ScopePersonal,
				Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Text:    "a personal note",
			},
		},
		{
			name: "unresolved subject",
			m: domain.Memory{
				ID:       "mem_01J8Z5K3W2RQ4X7M9NPQR2T3VA",
				Scope:    domain.ScopeService,
				Subject:  domain.Subject{Service: "rider-service", Concept: "dispatch"},
				Source:   domain.MemorySource{Kind: "agent", Client: "claude-code"},
				Status:   domain.MemoryDisputed,
				Created:  time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC),
				Updated:  time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC),
				Resolved: &domain.ResolvedSubject{Unresolved: true, ResolvedAt: time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)},
				Text:     "dispatch behaves oddly near midnight UTC.",
			},
		},
		{
			name: "error subject",
			m: domain.Memory{
				ID:      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3VB",
				Scope:   domain.ScopeWorkspace,
				Subject: domain.Subject{Error: &domain.ErrorRef{Operation: "order-service.createOrder", Status: 409, Code: "DUPLICATE"}},
				Source:  domain.MemorySource{Kind: "run", RunID: "run_1", StepID: "create"},
				Created: time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC),
				Text:    "409 DUPLICATE happens on retried creates.",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := memory.Format(tc.m)
			got, err := memory.Parse(data)
			require.NoError(t, err, "Format output:\n%s", data)

			assert.Equal(t, tc.m.ID, got.ID)
			assert.Equal(t, tc.m.Scope, got.Scope)
			assert.Equal(t, tc.m.Subject, got.Subject)
			assert.Equal(t, tc.m.Tags, got.Tags)
			assert.Equal(t, tc.m.Source, got.Source)
			assert.Equal(t, tc.m.Text, got.Text)
			assert.True(t, tc.m.Created.Equal(got.Created), "created: want %v got %v", tc.m.Created, got.Created)
			if tc.m.Updated.IsZero() {
				assert.True(t, got.Updated.IsZero())
			} else {
				assert.True(t, tc.m.Updated.Equal(got.Updated), "updated: want %v got %v", tc.m.Updated, got.Updated)
			}
			if tc.m.Resolved == nil {
				assert.Nil(t, got.Resolved)
			} else {
				require.NotNil(t, got.Resolved)
				assert.Equal(t, tc.m.Resolved.Method, got.Resolved.Method)
				assert.Equal(t, tc.m.Resolved.Path, got.Resolved.Path)
				assert.Equal(t, tc.m.Resolved.OperationHash, got.Resolved.OperationHash)
				assert.Equal(t, tc.m.Resolved.Unresolved, got.Resolved.Unresolved)
				assert.True(t, tc.m.Resolved.ResolvedAt.Equal(got.Resolved.ResolvedAt))
			}

			// Defaults must apply the same way whether or not they were
			// explicit in the source memory.
			wantType := tc.m.Type
			if wantType == "" {
				wantType = domain.MemoryNote
			}
			assert.Equal(t, wantType, got.Type)
			wantStatus := tc.m.Status
			if wantStatus == "" {
				wantStatus = domain.MemoryActive
			}
			assert.Equal(t, wantStatus, got.Status)
		})
	}
}

func TestFormat_OmitsEmptyFields(t *testing.T) {
	m := domain.Memory{
		ID:      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8",
		Scope:   domain.ScopePersonal,
		Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Text:    "no subject, no tags, no source, no updated, no resolved",
	}
	out := string(memory.Format(m))
	assert.NotContains(t, out, "subject:")
	assert.NotContains(t, out, "tags:")
	assert.NotContains(t, out, "source:")
	assert.NotContains(t, out, "updated:")
	assert.NotContains(t, out, "resolved:")
}

func TestFormat_FieldOrderMatchesPlan(t *testing.T) {
	m := domain.Memory{
		ID:      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8",
		Type:    domain.MemorySemantic,
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "rider-service.getRider", Field: "response.200.body.qcomSkill"},
		Tags:    []string{"qcom", "allocation"},
		Source:  domain.MemorySource{Kind: "user"},
		Status:  domain.MemoryActive,
		Created: time.Date(2026, 9, 5, 10, 20, 0, 0, time.UTC),
		Updated: time.Date(2026, 9, 5, 10, 20, 0, 0, time.UTC),
		Text:    "body",
	}
	out := string(memory.Format(m))

	order := []string{"id:", "type:", "scope:", "subject:", "tags:", "source:", "created:", "updated:", "status:"}
	last := -1
	for _, key := range order {
		idx := indexOf(out, key)
		require.Greater(t, idx, -1, "expected %q in formatted output:\n%s", key, out)
		require.Greater(t, idx, last, "expected %q to come after the previous key in:\n%s", key, out)
		last = idx
	}
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestParse_MissingScope(t *testing.T) {
	src := "---\nid: mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8\ncreated: 2026-09-05T10:20:00Z\n---\nbody\n"
	_, err := memory.Parse([]byte(src))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "scope")
}

func TestParse_InvalidFrontMatter_LineNumbers(t *testing.T) {
	// Front matter only (line numbers are relative to the front-matter block,
	// so "type" is on line 2 of the block: line 1 is "id:", line 2 is "type:").
	src := "---\nid: mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8\ntype: not-a-real-type\nscope: workspace\ncreated: 2026-09-05T10:20:00Z\n---\nbody\n"
	_, err := memory.Parse([]byte(src))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "line 2")
}

func TestParse_UnknownKey(t *testing.T) {
	src := "---\nid: mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8\nscope: workspace\ncreated: 2026-09-05T10:20:00Z\npriority: high\n---\nbody\n"
	_, err := memory.Parse([]byte(src))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "priority")
}

func TestParse_MissingOpeningDelimiter(t *testing.T) {
	_, err := memory.Parse([]byte("id: mem_x\nscope: workspace\n"))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestParse_MissingClosingDelimiter(t *testing.T) {
	_, err := memory.Parse([]byte("---\nid: mem_x\nscope: workspace\n"))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestParse_DefaultsTypeAndStatus(t *testing.T) {
	src := "---\nid: mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8\nscope: workspace\ncreated: 2026-09-05T10:20:00Z\n---\nbody text\n"
	m, err := memory.Parse([]byte(src))
	require.NoError(t, err)
	assert.Equal(t, domain.MemoryNote, m.Type)
	assert.Equal(t, domain.MemoryActive, m.Status)
}

func TestFileName(t *testing.T) {
	assert.Equal(t, "01J8Z5K3W2RQ4X7M9NPQR2T3V8.md", memory.FileName("mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8"))
}

func TestReadFileWriteFile(t *testing.T) {
	dir := t.TempDir()
	m := domain.Memory{
		ID:       "mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8",
		Scope:    domain.ScopeWorkspace,
		Created:  time.Date(2026, 9, 5, 10, 20, 0, 0, time.UTC),
		Text:     "hello world",
		FilePath: filepath.Join(dir, "memories", memory.FileName("mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8")),
	}
	require.NoError(t, memory.WriteFile(m))

	// mkdir -p happened.
	_, statErr := os.Stat(filepath.Dir(m.FilePath))
	require.NoError(t, statErr)

	got, err := memory.ReadFile(m.FilePath)
	require.NoError(t, err)
	assert.Equal(t, m.ID, got.ID)
	assert.Equal(t, m.Text, got.Text)
	assert.Equal(t, m.FilePath, got.FilePath)
	assert.NotEmpty(t, got.Hash)

	// Hash is stable across re-reads of unchanged content.
	got2, err := memory.ReadFile(m.FilePath)
	require.NoError(t, err)
	assert.Equal(t, got.Hash, got2.Hash)
}

func TestWriteFile_RequiresFilePath(t *testing.T) {
	err := memory.WriteFile(domain.Memory{ID: "mem_x"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestReadFile_MissingFile(t *testing.T) {
	_, err := memory.ReadFile(filepath.Join(t.TempDir(), "does-not-exist.md"))
	require.Error(t, err)
}
