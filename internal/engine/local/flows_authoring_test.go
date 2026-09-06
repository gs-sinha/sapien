package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// TestFlows_CreatePath_NestedSubdirIsIndexed is the regression test for
// docs/feedback/2026-09-05-41-step-flow-session.md item 5: a flow saved at
// a path with subdirectories, still inside <ws>/flows, must be found by
// List (the existing reindex now walks any depth).
func TestFlows_CreatePath_NestedSubdirIsIndexed(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	src := "version: 1\nid: nested-flow\nsteps:\n  - id: create\n    call: order-service.createOrder\n    body: { customerId: c, type: QCOM, pickup: {lat: 1, lng: 1}, drop: {lat: 1, lng: 1} }\n"
	f, err := l.Flows().Create(ctx, src, "sub/dir/x.flow.yaml")
	require.NoError(t, err)
	assert.Equal(t, "nested-flow", f.ID)
	wantPath := filepath.Join(ws.Dir, domain.FlowsDir, "sub", "dir", "x.flow.yaml")
	assert.Equal(t, wantPath, f.Path)
	assert.FileExists(t, wantPath)

	list, err := l.Flows().List(ctx, "")
	require.NoError(t, err)
	found := false
	for _, fs := range list {
		if fs.ID == "nested-flow" {
			found = true
			assert.Equal(t, wantPath, fs.Path)
		}
	}
	assert.True(t, found, "nested-flow must be indexed by List; got %+v", list)

	got, err := l.Flows().Get(ctx, "nested-flow")
	require.NoError(t, err)
	assert.Equal(t, "nested-flow", got.ID)
}

// TestFlows_CreatePath_RejectsAbsolute is the regression test for item 5's
// underlying bug: an explicit path used to be interpreted relative to the
// workspace root (or the process cwd), not the flows directory.
func TestFlows_CreatePath_RejectsAbsolute(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	abs := filepath.Join(ws.Dir, "outside.flow.yaml")
	src := "version: 1\nid: abs-flow\nsteps:\n  - id: create\n    call: order-service.createOrder\n    body: { customerId: c, type: QCOM, pickup: {lat: 1, lng: 1}, drop: {lat: 1, lng: 1} }\n"
	_, err = l.Flows().Create(ctx, src, abs)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Message, "absolute")
	assert.NotEmpty(t, errs.As(err).Hint)
	_, statErr := os.Stat(abs)
	assert.True(t, os.IsNotExist(statErr), "must not have written outside the flows directory")
}

func TestFlows_CreatePath_RejectsDotDot(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	src := "version: 1\nid: escape-flow\nsteps:\n  - id: create\n    call: order-service.createOrder\n    body: { customerId: c, type: QCOM, pickup: {lat: 1, lng: 1}, drop: {lat: 1, lng: 1} }\n"
	_, err = l.Flows().Create(ctx, src, "../escape.flow.yaml")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Message, "..")

	_, err = l.Flows().Create(ctx, src, "sub/../../escape.flow.yaml")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestFlows_CreatePath_RejectsWrongSuffix(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	src := "version: 1\nid: wrong-suffix\nsteps:\n  - id: create\n    call: order-service.createOrder\n    body: { customerId: c, type: QCOM, pickup: {lat: 1, lng: 1}, drop: {lat: 1, lng: 1} }\n"
	_, err = l.Flows().Create(ctx, src, "wrong-suffix.yaml")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Message, domain.FlowFileSuffix)
}

func TestFlows_CreatePath_RejectsEmptyAfterTrim(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	src := "version: 1\nid: x\nsteps:\n  - id: create\n    call: order-service.createOrder\n    body: { customerId: c, type: QCOM, pickup: {lat: 1, lng: 1}, drop: {lat: 1, lng: 1} }\n"
	_, err = l.Flows().Create(ctx, src, "   ")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}
