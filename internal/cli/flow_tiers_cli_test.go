package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive `flow create --scope`, `flow promote`, `flow rescope`,
// `flow list` and `flow show` against the enginetest fake (setupFakeEngine),
// so they prove what the CLI sends to the engine and how it reports the
// answer -- not the on-disk tier layout, which the local engine's own tests
// cover.

func TestFlowCreate_DefaultsToLocalTier(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	file := writeTemp(t, "auth-demo.flow.yaml", authoringFlow)

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "saved flow auth-demo")
	assert.Contains(t, stdout, "[local]")
	assert.Contains(t, stdout, "sapien flow promote auth-demo", "a local flow is told how to become shared")

	args := lastCall(fake, "Flows.CreateIn").Args.(map[string]string)
	assert.Equal(t, "local", args["owner_kind"])
	assert.Empty(t, args["owner_id"])
}

func TestFlowCreate_ScopeFlags(t *testing.T) {
	dir, fake := setupFakeEngine(t)

	t.Run("Workspace", func(t *testing.T) {
		file := writeTemp(t, "ws.flow.yaml", "version: 1\nid: ws-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n")
		stdout, stderr, code := run(t, "--workspace", dir, "flow", "create", file, "--scope", "workspace", "--json")
		require.Equal(t, 0, code, "stderr: %s", stderr)
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &got))
		assert.Equal(t, "workspace", got["tier"])
		assert.Equal(t, "workspace", lastCall(fake, "Flows.CreateIn").Args.(map[string]string)["owner_kind"])
	})

	t.Run("Service", func(t *testing.T) {
		file := writeTemp(t, "svc.flow.yaml", "version: 1\nid: svc-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n")
		stdout, stderr, code := run(t, "--workspace", dir, "flow", "create", file, "--scope", "service", "--service", "order-service", "--json")
		require.Equal(t, 0, code, "stderr: %s", stderr)
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &got))
		assert.Equal(t, "service", got["tier"])
		assert.Equal(t, "order-service", got["service"])
		args := lastCall(fake, "Flows.CreateIn").Args.(map[string]string)
		assert.Equal(t, "service", args["owner_kind"])
		assert.Equal(t, "order-service", args["owner_id"])
	})

	t.Run("ServiceWithoutName", func(t *testing.T) {
		file := writeTemp(t, "x.flow.yaml", "version: 1\nid: x\nsteps: []\n")
		_, stderr, code := run(t, "--workspace", dir, "flow", "create", file, "--scope", "service")
		assert.NotEqual(t, 0, code)
		assert.Contains(t, stderr, "--service")
	})

	t.Run("UnknownScope", func(t *testing.T) {
		file := writeTemp(t, "x.flow.yaml", "version: 1\nid: x\nsteps: []\n")
		_, stderr, code := run(t, "--workspace", dir, "flow", "create", file, "--scope", "global")
		assert.NotEqual(t, 0, code)
		assert.Contains(t, stderr, `unknown scope "global"`)
	})

	t.Run("ServiceNameWithOtherScope", func(t *testing.T) {
		file := writeTemp(t, "x.flow.yaml", "version: 1\nid: x\nsteps: []\n")
		_, stderr, code := run(t, "--workspace", dir, "flow", "create", file, "--scope", "local", "--service", "order-service")
		assert.NotEqual(t, 0, code)
		assert.Contains(t, stderr, "--service applies to --scope service only")
	})
}

func TestFlowPromote_ClimbsTheLadder(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	file := writeTemp(t, "auth-demo.flow.yaml", authoringFlow)
	_, stderr, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// local -> workspace needs no flags.
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "promote", "auth-demo")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "team tier")
	args := lastCall(fake, "Flows.Rescope").Args.(map[string]string)
	assert.Equal(t, "auth-demo", args["id"])
	assert.Equal(t, "workspace", args["owner_kind"])

	// workspace -> service needs the service named.
	_, stderr, code = run(t, "--workspace", dir, "flow", "promote", "auth-demo")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "--service")

	stdout, stderr, code = run(t, "--workspace", dir, "flow", "promote", "auth-demo", "--service", "order-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "service", got["tier"])
	assert.Equal(t, "order-service", got["service"])
	args = lastCall(fake, "Flows.Rescope").Args.(map[string]string)
	assert.Equal(t, "service", args["owner_kind"])
	assert.Equal(t, "order-service", args["owner_id"])

	// service is the top.
	_, stderr, code = run(t, "--workspace", dir, "flow", "promote", "auth-demo", "--service", "order-service")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "nothing above it")
}

func TestFlowPromote_ExplicitTargetAndRefusals(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	file := writeTemp(t, "auth-demo.flow.yaml", authoringFlow)
	_, stderr, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// local -> service straight away, when the service is named.
	_, stderr, code = run(t, "--workspace", dir, "flow", "promote", "auth-demo", "--to", "service", "--service", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, "service", lastCall(fake, "Flows.Rescope").Args.(map[string]string)["owner_kind"])

	// Down is not a promotion.
	_, stderr, code = run(t, "--workspace", dir, "flow", "promote", "auth-demo", "--to", "workspace")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "promote only moves up")
	assert.Contains(t, stderr, "sapien flow rescope")

	_, stderr, code = run(t, "--workspace", dir, "flow", "promote", "auth-demo", "--to", "local")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "bottom of the ladder")

	// The seeded workspace flow: --to workspace is where it already is.
	_, stderr, code = run(t, "--workspace", dir, "flow", "promote", "create-order-flow", "--to", "workspace")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "already in the team tier")
}

func TestFlowRescope_MovesInAnyDirection(t *testing.T) {
	dir, fake := setupFakeEngine(t)

	// The seeded flow is workspace-tier; take it down to local.
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "rescope", "create-order-flow", "--scope", "local", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "create-order-flow", got["id"])
	assert.Equal(t, "local", got["tier"])
	assert.Contains(t, got, "old_path")
	assert.Contains(t, got, "new_path")
	args := lastCall(fake, "Flows.Rescope").Args.(map[string]string)
	assert.Equal(t, "local", args["owner_kind"])

	// Same tier again is refused before the engine is asked.
	before := len(fake.Calls)
	_, stderr, code = run(t, "--workspace", dir, "flow", "rescope", "create-order-flow", "--scope", "local")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "already in the local tier")
	for _, c := range fake.Calls[before:] {
		assert.NotEqual(t, "Flows.Rescope", c.Method)
	}

	_, stderr, code = run(t, "--workspace", dir, "flow", "rescope", "create-order-flow")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "--scope")

	_, stderr, code = run(t, "--workspace", dir, "flow", "rescope", "no-such-flow", "--scope", "workspace")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "E_FLOW_NOT_FOUND")
}

func TestFlowList_ShowsTierColumn(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	file := writeTemp(t, "auth-demo.flow.yaml", authoringFlow)
	_, stderr, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	svc := writeTemp(t, "svc.flow.yaml", "version: 1\nid: svc-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n")
	_, stderr, code = run(t, "--workspace", dir, "flow", "create", svc, "--scope", "service", "--service", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "TIER")
	assert.NotContains(t, stdout, "OWNER")
	lines := map[string]string{}
	for _, line := range splitLines(stdout) {
		if len(line) == 0 {
			continue
		}
		lines[firstField(line)] = line
	}
	assert.Contains(t, lines["create-order-flow"], "team")
	assert.Contains(t, lines["auth-demo"], "local")
	assert.Contains(t, lines["svc-flow"], "service:order-service")
}

func TestFlowShow_PrintsTierAsYAMLComment(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "show", "create-order-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.True(t, len(stdout) > 0 && stdout[0] == '#', "the tier line must be a YAML comment so the output stays a valid flow file")
	assert.Contains(t, splitLines(stdout)[0], "# tier: team")
	assert.Contains(t, stdout, "id: create-order-flow")
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func firstField(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' || line[i] == '\t' {
			return line[:i]
		}
	}
	return line
}
