package cli_test

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
)

func portOf(t *testing.T, ln net.Listener) int {
	t.Helper()
	_, p, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	n, err := strconv.Atoi(p)
	require.NoError(t, err)
	return n
}

func TestListenLoopback_BindsTheRequestedPort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	want := portOf(t, probe)
	require.NoError(t, probe.Close())

	ln, err := cli.ListenLoopback(want, true)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	assert.Equal(t, want, portOf(t, ln))
}

// The stable origin is a convenience, not a requirement: when something
// else already holds the default port, the daemon still starts.
func TestListenLoopback_FallsBackToEphemeralWhenDefaultPortIsTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = held.Close() }()
	taken := portOf(t, held)

	ln, err := cli.ListenLoopback(taken, true)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	got := portOf(t, ln)
	assert.NotEqual(t, taken, got)
	assert.NotZero(t, got)
}

// An explicitly requested port is honoured exactly: a caller that pinned
// one is told it could not have it rather than silently served another.
func TestListenLoopback_ExplicitPortDoesNotFallBack(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = held.Close() }()

	_, err = cli.ListenLoopback(portOf(t, held), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binding 127.0.0.1:")
}

// Port 0 means "any port" and is already its own fallback.
func TestListenLoopback_ZeroPicksAFreePort(t *testing.T) {
	ln, err := cli.ListenLoopback(0, true)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	assert.NotZero(t, portOf(t, ln))
}
