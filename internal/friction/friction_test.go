package friction_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/friction"
)

func TestCategory_Constants(t *testing.T) {
	assert.Equal(t, friction.Category("bug"), friction.CategoryBug)
	assert.Equal(t, friction.Category("idea"), friction.CategoryIdea)
	assert.Equal(t, friction.Category("docs"), friction.CategoryDocs)
	assert.Equal(t, friction.Category("missing"), friction.CategoryMissing)
}
