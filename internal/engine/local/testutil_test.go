package local

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/workspace"

	"github.com/gs-sinha/sapien/fixtures/logistics/mock"
)

// mockEnv bundles the running fixture mock servers and the *Local opened
// against them, wired through an "environments/test.yaml" the caller's
// workspace gets (PLAN §7): the three logistics services' base URLs, no
// auth (the mocks default to RequireAuth: false).
type mockEnv struct {
	l     *Local
	world *mock.World
}

// setupEngineWithMock builds a workspace (copies of the three logistics
// fixtures, per setupWorkspace), starts the fixture mock servers backed by
// a fresh World with no timeline delay, writes "environments/test.yaml"
// pointing at them, and opens a *Local against the result using an
// in-memory secret store (so tests never touch the OS keychain, per the
// Options.Secrets hook).
func setupEngineWithMock(t *testing.T) mockEnv {
	t.Helper()
	ws, _ := setupWorkspace(t)

	world := mock.NewWorld()
	world.SetTimelineDelay(0)
	orderURL, allocURL, riderURL, _ := mock.StartAll(t, mock.Options{World: world})

	testEnv := &domain.Environment{
		Version: 1,
		Name:    "test",
		Services: map[string]domain.ServiceEnv{
			"order-service":      {BaseURL: orderURL},
			"allocation-service": {BaseURL: allocURL},
			"rider-service":      {BaseURL: riderURL},
		},
	}
	require.NoError(t, workspace.SaveEnvironment(ws, testEnv))

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	return mockEnv{l: l, world: world}
}

// successFlowYAML is the PRD §48 primary success scenario as a *.flow.yaml
// source: create a QCOM order, allocate a rider, fetch that rider and
// assert it's online and QCOM-eligible.
const successFlowYAML = `version: 1
id: qcom-allocation
name: QCOM allocation smoke test
description: Create a QCOM order, allocate a rider, verify it is online and QCOM-eligible.
tags: [allocation, smoke]

inputs:
  customerId: { type: string, default: "cust_123" }

steps:
  - id: create
    call: order-service.createOrder
    body:
      customerId: "${inputs.customerId}"
      type: QCOM
      pickup: { lat: 12.9716, lng: 77.5946 }
      drop: { lat: 12.9352, lng: 77.6146 }
    extract:
      orderId: body.orderId
    assert:
      - status == 201

  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract:
      riderId: body.riderId
    assert:
      - status == 201

  - id: rider
    call: rider-service.getRider
    input: { riderId: "${steps.allocate.out.riderId}" }
    assert:
      - status == 200
      - body.online == true
      - { path: body.qcomSkill, eq: true, message: "QCOM riders must have qcomSkill" }
`
