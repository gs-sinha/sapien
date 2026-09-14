package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// --- bind_service --------------------------------------------------------

func TestTool_BindService(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "bind_service", map[string]any{"service": "rider-service", "path": "/home/dev/code/rider-service"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[BindServiceOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service", out.Service.Name)
	require.NotNil(t, out.Service.Binding)
	assert.Equal(t, domain.BindingLocal, out.Service.Binding.Mode)
	require.NotNil(t, out.Service.Binding.Local)
	assert.Equal(t, "/home/dev/code/rider-service", out.Service.Binding.Local.Path)
	assert.True(t, out.Service.Binding.Writable)

	text := firstText(res)
	assert.Contains(t, text, "bound rider-service")
	assert.Contains(t, text, "reads: local /home/dev/code/rider-service")
}

func TestTool_BindService_InferredFromCheckout(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	// Bind once by name so the fake has a candidate at this path, then bind
	// again with no service name: BindWith("", path, ...) must infer
	// rider-service from it.
	first := callTool(t, cs, "bind_service", map[string]any{"service": "rider-service", "path": "/home/dev/code/rider-service"})
	require.False(t, first.IsError, firstText(first))

	res := callTool(t, cs, "bind_service", map[string]any{"path": "/home/dev/code/rider-service"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[BindServiceOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service", out.Service.Name)
}

func TestTool_BindService_RequiresAbsolutePath(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "bind_service", map[string]any{"service": "rider-service", "path": "./rider-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "not absolute")
}

func TestTool_BindService_EmptyPathIsInvalid(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "bind_service", map[string]any{"service": "rider-service", "path": "   "})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_INVALID")
}

func TestTool_BindService_UnknownServiceIsNotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "bind_service", map[string]any{"service": "no-such-service", "path": "/home/dev/code/x"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_SERVICE_NOT_FOUND")
}

func TestTool_BindService_PermissionDenied(t *testing.T) {
	cfg := Config{Default: DefaultPermissions()}
	cfg.Default.WriteServices = false
	cs := newTestSession(t, cfg, "codex")
	res := callTool(t, cs, "bind_service", map[string]any{"service": "rider-service", "path": "/home/dev/code/rider-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
	assert.Contains(t, firstText(res), "write_services")
}

// --- unbind_service -------------------------------------------------------

func TestTool_UnbindService(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	bound := callTool(t, cs, "bind_service", map[string]any{"service": "rider-service", "path": "/home/dev/code/rider-service"})
	require.False(t, bound.IsError, firstText(bound))

	res := callTool(t, cs, "unbind_service", map[string]any{"service": "rider-service"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[UnbindServiceOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service", out.Service.Name)

	text := firstText(res)
	assert.Contains(t, text, "unbound rider-service")
	assert.Contains(t, text, "reads:")
}

func TestTool_UnbindService_RequiresService(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "unbind_service", map[string]any{"service": "   "})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_INVALID")
}

func TestTool_UnbindService_UnknownServiceIsNotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "unbind_service", map[string]any{"service": "no-such-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_SERVICE_NOT_FOUND")
}

func TestTool_UnbindService_PermissionDenied(t *testing.T) {
	cfg := Config{Default: DefaultPermissions()}
	cfg.Default.WriteServices = false
	cs := newTestSession(t, cfg, "codex")
	res := callTool(t, cs, "unbind_service", map[string]any{"service": "rider-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_PERMISSION_DENIED")
	assert.Contains(t, firstText(res), "write_services")
}

// --- find_checkouts --------------------------------------------------------

func TestTool_FindCheckouts(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "find_checkouts", map[string]any{"service": "rider-service"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[FindCheckoutsOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service", out.Service)
	assert.Contains(t, firstText(res), "rider-service currently reads:")
}

func TestTool_FindCheckouts_RequiresService(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "find_checkouts", map[string]any{"service": "   "})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_INVALID")
}

func TestTool_FindCheckouts_UnknownServiceIsNotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "find_checkouts", map[string]any{"service": "no-such-service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_SERVICE_NOT_FOUND")
}

// TestTool_FindCheckouts_ReadPermissionOnly proves find_checkouts is
// gated like get_service (classReadContracts), not write_services: a
// client with write_services denied can still call it.
func TestTool_FindCheckouts_ReadPermissionOnly(t *testing.T) {
	cfg := Config{Default: DefaultPermissions()}
	cfg.Default.WriteServices = false
	cs := newTestSession(t, cfg, "codex")
	res := callTool(t, cs, "find_checkouts", map[string]any{"service": "rider-service"})
	require.False(t, res.IsError, firstText(res))
}
