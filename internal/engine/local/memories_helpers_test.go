package local

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestMemoryTypeHeading(t *testing.T) {
	assert.Equal(t, "Notes", memoryTypeHeading(""))
	assert.Equal(t, "Behavioral", memoryTypeHeading(domain.MemoryBehavioral))
	assert.Equal(t, "Gotcha", memoryTypeHeading(domain.MemoryGotcha))
}

func TestSlugFirstWords(t *testing.T) {
	assert.Equal(t, "memory", slugFirstWords("", 5))
	assert.Equal(t, "memory", slugFirstWords("!!!", 5))
	assert.Equal(t, "allocation-always-prefers-the-first", slugFirstWords("Allocation always prefers the first eligible rider", 5))
	assert.Equal(t, "qcomskill-means-eligible", slugFirstWords("qcomSkill means Eligible!!", 5))
}

func TestDescriptionBlock(t *testing.T) {
	assert.Equal(t, "description: \"\"\n", descriptionBlock("   "))
	block := descriptionBlock("line one\nline two")
	assert.Contains(t, block, "description: >\n")
	assert.Contains(t, block, "  line one\n")
	assert.Contains(t, block, "  line two\n")
}

func TestFieldPointerSuffix(t *testing.T) {
	base := "/paths/~1v1~1riders~1{riderId}/get"
	assert.Equal(t, base+"/properties/qcomSkill", fieldPointerSuffix(base, "response.200.body.qcomSkill"))
	assert.Equal(t, base+"/properties/items/properties/riderId", fieldPointerSuffix(base, "response.200.body.items[].riderId"))
	assert.Equal(t, base+"/properties/customerId", fieldPointerSuffix(base, "request.body.customerId"))
	// Unrecognized shapes leave the pointer unmodified (PLAN §26: "leave the
	// operation pointer if unsure").
	assert.Equal(t, base, fieldPointerSuffix(base, "request.query.limit"))
	assert.Equal(t, base, fieldPointerSuffix(base, "status"))
}

func TestResolveSubjectService(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	assert.Equal(t, "order-service", resolveSubjectService(ctx, l, domain.Subject{Service: "order-service"}))
	assert.Equal(t, "allocation-service", resolveSubjectService(ctx, l, domain.Subject{Schema: "allocation-service.AllocateRequest"}))
	assert.Equal(t, "", resolveSubjectService(ctx, l, domain.Subject{}))

	// A workspace-owned flow doesn't resolve to any service.
	_, err = l.Flows().Create(ctx, validFlowYAML, "")
	require.NoError(t, err)
	assert.Equal(t, "", resolveSubjectService(ctx, l, domain.Subject{Flow: "order-allocation"}))

	// A service-owned flow resolves to its owning service.
	assert.Equal(t, "allocation-service", resolveSubjectService(ctx, l, domain.Subject{Flow: "smoke"}))
}

func TestWarnOnSecrets_Detected(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	// A bearer-token-shaped string in the body should be detected (and
	// merely logged; ScanSecrets never blocks the write, PLAN §20/§28).
	mem := domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "order-service"},
		Text:    "Auth header looks like: Bearer sk_live_ABCDEFGHIJ1234567890",
	}
	created, err := l.Memories().Create(ctx, mem)
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
}
