package mcp

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readResource reads uri and fails the test on a transport/handler error.
func readResource(t *testing.T, cs *sdkmcp.ClientSession, uri string) *sdkmcp.ReadResourceResult {
	t.Helper()
	res, err := cs.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: uri})
	require.NoError(t, err, "reading %s", uri)
	return res
}

// firstResourceText returns the text of the first resource content block.
func firstResourceText(res *sdkmcp.ReadResourceResult) string {
	if len(res.Contents) == 0 {
		return ""
	}
	return res.Contents[0].Text
}

func TestResource_Service(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := readResource(t, cs, "sapien://services/rider-service")
	require.Len(t, res.Contents, 1)
	assert.Equal(t, "application/json", res.Contents[0].MIMEType)
	assert.Contains(t, firstResourceText(res), "rider-service")
}

func TestResource_ServiceDoc(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := readResource(t, cs, "sapien://services/rider-service/docs/docs/allocation.md")
	require.Len(t, res.Contents, 1)
	assert.Equal(t, "text/markdown", res.Contents[0].MIMEType)
	assert.Contains(t, firstResourceText(res), "Allocation rules")
	assert.Contains(t, firstResourceText(res), "QCOM orders")
}

func TestResource_Operation(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := readResource(t, cs, "sapien://operations/rider-service.getRider")
	require.Len(t, res.Contents, 1)
	assert.Contains(t, firstResourceText(res), "getRider")
}

func TestResource_Schema(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := readResource(t, cs, "sapien://schemas/rider-service/Rider")
	require.Len(t, res.Contents, 1)
	assert.Contains(t, firstResourceText(res), "qcomSkill")
}

func TestResource_Flow(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := readResource(t, cs, "sapien://flows/rider-flow")
	require.Len(t, res.Contents, 1)
	assert.Equal(t, "text/markdown", res.Contents[0].MIMEType)
	text := firstResourceText(res)
	assert.Contains(t, text, "```yaml")
	assert.Contains(t, text, "rider-flow")
}

func TestResource_Memory(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := readResource(t, cs, "sapien://memories/mem_seed1")
	require.Len(t, res.Contents, 1)
	text := firstResourceText(res)
	assert.Contains(t, text, "id: mem_seed1")
	assert.Contains(t, text, "qcomSkill must be true")
}

func TestResource_Reference(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	cases := []struct {
		topic string
		want  string
		mime  string
	}{
		{"flow-dsl", "Flow DSL", "text/markdown"},
		{"memory", "Memory DSL", "text/markdown"},
		{"expressions", "Expressions", "text/markdown"},
		{"service", "Service package reference", "text/markdown"},
		{"flow.schema.json", `"$schema"`, "application/json"},
	}
	for _, tc := range cases {
		t.Run(tc.topic, func(t *testing.T) {
			res := readResource(t, cs, "sapien://reference/"+tc.topic)
			require.Len(t, res.Contents, 1)
			assert.Equal(t, tc.mime, res.Contents[0].MIMEType)
			assert.Contains(t, firstResourceText(res), tc.want)
		})
	}
}

func TestResource_Reference_UnknownTopic(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	_, err := cs.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: "sapien://reference/bogus"})
	assert.Error(t, err)
}

func TestResource_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	_, err := cs.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: "sapien://services/nope"})
	assert.Error(t, err)
}
