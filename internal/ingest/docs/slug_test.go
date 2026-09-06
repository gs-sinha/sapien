package docs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/growsimplee/sapien/internal/ingest/docs"
)

func TestSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "Overview", "overview"},
		{"multi word", "Rider Allocation Rules", "rider-allocation-rules"},
		{"punctuation", "QCOM Skill (v2)!", "qcom-skill-v2"},
		{"already lower with dashes", "already-slugged", "already-slugged"},
		{"mixed digits", "Section 42", "section-42"},
		{"leading/trailing punctuation trimmed", "  ## Foo ##  ", "foo"},
		{"underscores become separators", "foo_bar baz", "foo-bar-baz"},
		{"empty", "", ""},
		{"only punctuation", "!!!---***", ""},
		{"single word", "dispatch", "dispatch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, docs.Slug(c.in))
		})
	}
}

func TestSlug_Deterministic(t *testing.T) {
	in := "Rider Allocation & QCOM Matching!"
	assert.Equal(t, docs.Slug(in), docs.Slug(in))
}
