package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/memory"
)

// newTestStore builds a Store wired to a temp workspace dir and a temp
// "rider-service" package dir, over a fresh in-memory DB, with the given
// Resolver (may be nil).
func newTestStore(t *testing.T, res memory.Resolver) (*memory.Store, memory.Locator) {
	t.Helper()
	db := openTestDB(t)
	ws := t.TempDir()
	svcDir := t.TempDir()
	loc := memory.Locator{
		WorkspaceDir: ws,
		ServiceDirs:  map[string]string{"rider-service": svcDir},
	}
	return memory.New(db, loc, res), loc
}

func TestStore_Create_Scopes(t *testing.T) {
	ctx := context.Background()

	t.Run("personal has no file", func(t *testing.T) {
		s, _ := newTestStore(t, nil)
		got, err := s.Create(ctx, domain.Memory{Scope: domain.ScopePersonal, Text: "a personal note"})
		require.NoError(t, err)
		assert.Equal(t, "", got.FilePath)
		assert.NotEmpty(t, got.ID)
		assert.True(t, strings.HasPrefix(got.ID, "mem_"))
		assert.Equal(t, domain.MemoryNote, got.Type)
		assert.Equal(t, domain.MemoryActive, got.Status)
		assert.Equal(t, "user", got.Source.Kind)
		assert.False(t, got.Created.IsZero())
		assert.Equal(t, got.Created, got.Updated)
	})

	t.Run("workspace writes a file under <ws>/memories", func(t *testing.T) {
		s, loc := newTestStore(t, nil)
		got, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "workspace note"})
		require.NoError(t, err)
		require.NotEmpty(t, got.FilePath)
		assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", memory.FileName(got.ID)), got.FilePath)
		data, err := os.ReadFile(got.FilePath)
		require.NoError(t, err)
		assert.Contains(t, string(data), "workspace note")
		assert.NotEmpty(t, got.Hash)
	})

	t.Run("service writes a file under the service's package dir", func(t *testing.T) {
		s, loc := newTestStore(t, nil)
		got, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeService,
			Subject: domain.Subject{Service: "rider-service"},
			Text:    "service note",
		})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(loc.ServiceDirs["rider-service"], "memories", memory.FileName(got.ID)), got.FilePath)
	})

	t.Run("flow (no owner configured) writes to the workspace", func(t *testing.T) {
		s, loc := newTestStore(t, nil)
		got, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeFlow,
			Subject: domain.Subject{Flow: "order-allocation"},
			Text:    "flow note",
		})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", memory.FileName(got.ID)), got.FilePath)
	})

	t.Run("no scope defaults to workspace", func(t *testing.T) {
		s, loc := newTestStore(t, nil)
		got, err := s.Create(ctx, domain.Memory{Text: "defaulted"})
		require.NoError(t, err)
		assert.Equal(t, domain.ScopeWorkspace, got.Scope)
		assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", memory.FileName(got.ID)), got.FilePath)
	})
}

func TestStore_Create_Validation(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	t.Run("empty text rejected", func(t *testing.T) {
		_, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "   "})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("field without operation or schema rejected", func(t *testing.T) {
		_, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeWorkspace,
			Subject: domain.Subject{Field: "response.200.body.qcomSkill"},
			Text:    "x",
		})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("field with operation is fine", func(t *testing.T) {
		_, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeWorkspace,
			Subject: domain.Subject{Operation: "rider-service.getRider", Field: "response.200.body.qcomSkill"},
			Text:    "x",
		})
		require.NoError(t, err)
	})

	t.Run("field with schema is fine", func(t *testing.T) {
		_, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeWorkspace,
			Subject: domain.Subject{Schema: "rider-service.Rider", Field: "qcomSkill"},
			Text:    "x",
		})
		require.NoError(t, err)
	})

	t.Run("unknown service errors", func(t *testing.T) {
		_, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeService,
			Subject: domain.Subject{Service: "no-such-service"},
			Text:    "x",
		})
		require.Error(t, err)
		assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
	})
}

func TestStore_Create_ResolvesSubject(t *testing.T) {
	ctx := context.Background()

	t.Run("resolver present, operation known", func(t *testing.T) {
		res := newFakeResolver().
			withOp("rider-service.getRider", memory.OperationInfo{Service: "rider-service", Method: "GET", Path: "/v1/riders/{riderId}", Hash: "h1"}).
			withField("rider-service.getRider", "response.200.body.qcomSkill")
		s, _ := newTestStore(t, res)

		got, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeWorkspace,
			Subject: domain.Subject{Operation: "rider-service.getRider", Field: "response.200.body.qcomSkill"},
			Text:    "qcomSkill note",
		})
		require.NoError(t, err)
		require.NotNil(t, got.Resolved)
		assert.Equal(t, "GET", got.Resolved.Method)
		assert.Equal(t, "/v1/riders/{riderId}", got.Resolved.Path)
		assert.Equal(t, "h1", got.Resolved.OperationHash)
		assert.False(t, got.Resolved.Unresolved)
	})

	t.Run("resolver present, operation unknown -> unresolved", func(t *testing.T) {
		s, _ := newTestStore(t, newFakeResolver())
		got, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeWorkspace,
			Subject: domain.Subject{Operation: "rider-service.getRider"},
			Text:    "x",
		})
		require.NoError(t, err)
		require.NotNil(t, got.Resolved)
		assert.True(t, got.Resolved.Unresolved)
	})

	t.Run("resolver present, operation known but field unknown -> unresolved", func(t *testing.T) {
		res := newFakeResolver().withOp("rider-service.getRider", memory.OperationInfo{Method: "GET", Path: "/v1/riders/{riderId}"})
		s, _ := newTestStore(t, res)
		got, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeWorkspace,
			Subject: domain.Subject{Operation: "rider-service.getRider", Field: "response.200.body.notAField"},
			Text:    "x",
		})
		require.NoError(t, err)
		require.NotNil(t, got.Resolved)
		assert.True(t, got.Resolved.Unresolved)
	})

	t.Run("no resolver -> nil Resolved", func(t *testing.T) {
		s, _ := newTestStore(t, nil)
		got, err := s.Create(ctx, domain.Memory{
			Scope:   domain.ScopeWorkspace,
			Subject: domain.Subject{Operation: "rider-service.getRider"},
			Text:    "x",
		})
		require.NoError(t, err)
		assert.Nil(t, got.Resolved)
	})

	t.Run("no operation subject -> nil Resolved even with a resolver", func(t *testing.T) {
		s, _ := newTestStore(t, newFakeResolver())
		got, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "x"})
		require.NoError(t, err)
		assert.Nil(t, got.Resolved)
	})
}

func TestStore_GetUpdateDelete(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t, nil)

	created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "original text", Tags: []string{"a"}})
	require.NoError(t, err)

	t.Run("Get", func(t *testing.T) {
		got, err := s.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, "original text", got.Text)
		assert.Equal(t, []string{"a"}, got.Tags)
	})

	t.Run("Get not found", func(t *testing.T) {
		_, err := s.Get(ctx, "mem_doesnotexist")
		require.Error(t, err)
		assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))
	})

	t.Run("Update in place (same scope)", func(t *testing.T) {
		updated := *created
		updated.Text = "updated text"
		updated.Tags = []string{"a", "b"}
		got, err := s.Update(ctx, updated)
		require.NoError(t, err)
		assert.Equal(t, "updated text", got.Text)
		assert.Equal(t, []string{"a", "b"}, got.Tags)
		// Created must be preserved, up to the second-level precision RFC3339
		// storage round-trips (PLAN §10: "RFC3339 UTC timestamps").
		assert.Equal(t, created.Created.UTC().Truncate(time.Second), got.Created.UTC().Truncate(time.Second))
		assert.True(t, got.Updated.After(created.Updated) || got.Updated.Equal(created.Updated))
		assert.Equal(t, created.FilePath, got.FilePath)

		reGet, err := s.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, "updated text", reGet.Text)
	})

	t.Run("Update moves the file when scope changes", func(t *testing.T) {
		oldPath := created.FilePath
		_, statErr := os.Stat(oldPath)
		require.NoError(t, statErr, "precondition: original file must exist")

		moved := *created
		moved.Text = "moved to a service"
		moved.Scope = domain.ScopeService
		moved.Subject = domain.Subject{Service: "rider-service"}
		got, err := s.Update(ctx, moved)
		require.NoError(t, err)

		assert.Equal(t, filepath.Join(loc.ServiceDirs["rider-service"], "memories", memory.FileName(created.ID)), got.FilePath)
		_, err = os.Stat(oldPath)
		assert.True(t, os.IsNotExist(err), "expected old file to be removed after the scope change")
		_, err = os.Stat(got.FilePath)
		assert.NoError(t, err, "expected new file to exist after the scope change")
	})

	t.Run("Update moving to personal scope removes the file", func(t *testing.T) {
		reGet, err := s.Get(ctx, created.ID)
		require.NoError(t, err)
		oldPath := reGet.FilePath
		require.NotEmpty(t, oldPath)

		personal := *reGet
		personal.Scope = domain.ScopePersonal
		personal.Subject = domain.Subject{}
		got, err := s.Update(ctx, personal)
		require.NoError(t, err)
		assert.Equal(t, "", got.FilePath)
		_, err = os.Stat(oldPath)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("Update unknown id errors", func(t *testing.T) {
		_, err := s.Update(ctx, domain.Memory{ID: "mem_doesnotexist", Text: "x"})
		require.Error(t, err)
		assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))
	})

	t.Run("Update without id errors", func(t *testing.T) {
		_, err := s.Update(ctx, domain.Memory{Text: "x"})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("Delete removes file and index rows", func(t *testing.T) {
		reGet, err := s.Get(ctx, created.ID)
		require.NoError(t, err)
		// Currently personal (no file) from the prior subtest; create a fresh
		// workspace one so there's a file to check for removal.
		wsMem, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "to be deleted"})
		require.NoError(t, err)
		_, statErr := os.Stat(wsMem.FilePath)
		require.NoError(t, statErr)

		require.NoError(t, s.Delete(ctx, wsMem.ID))

		_, err = os.Stat(wsMem.FilePath)
		assert.True(t, os.IsNotExist(err))
		_, err = s.Get(ctx, wsMem.ID)
		assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))

		// Also delete the personal-scoped one from the previous subtest to
		// keep this test self-contained (no assertion needed beyond no error).
		require.NoError(t, s.Delete(ctx, reGet.ID))
	})

	t.Run("Delete unknown id errors", func(t *testing.T) {
		err := s.Delete(ctx, "mem_doesnotexist")
		require.Error(t, err)
		assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))
	})
}

func TestStore_List(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	personal, err := s.Create(ctx, domain.Memory{Scope: domain.ScopePersonal, Type: domain.MemoryGotcha, Text: "personal gotcha"})
	require.NoError(t, err)
	ws, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Subject: domain.Subject{Operation: "rider-service.getRider"}, Text: "ws op note"})
	require.NoError(t, err)
	svc, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeService, Subject: domain.Subject{Service: "rider-service"}, Text: "svc note"})
	require.NoError(t, err)
	flowMem, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Subject: domain.Subject{Flow: "order-allocation"}, Text: "flow note"})
	require.NoError(t, err)

	// Deactivate one memory to prove List (unlike Search/Relevant) still
	// includes it.
	superseded := *ws
	superseded.Status = domain.MemorySuperseded
	_, err = s.Update(ctx, superseded)
	require.NoError(t, err)

	t.Run("no filter returns everything, newest updated first", func(t *testing.T) {
		got, err := s.List(ctx, domain.MemoryQuery{})
		require.NoError(t, err)
		require.Len(t, got, 4)
		for i := 1; i < len(got); i++ {
			assert.True(t, !got[i-1].Updated.Before(got[i].Updated))
		}
	})

	t.Run("filters by scope", func(t *testing.T) {
		got, err := s.List(ctx, domain.MemoryQuery{Scope: domain.ScopePersonal})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, personal.ID, got[0].ID)
	})

	t.Run("filters by type", func(t *testing.T) {
		got, err := s.List(ctx, domain.MemoryQuery{Type: domain.MemoryGotcha})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, personal.ID, got[0].ID)
	})

	t.Run("filters by service (subject.service or operation prefix)", func(t *testing.T) {
		got, err := s.List(ctx, domain.MemoryQuery{Service: "rider-service"})
		require.NoError(t, err)
		ids := idsOf(got)
		assert.ElementsMatch(t, []string{ws.ID, svc.ID}, ids)
	})

	t.Run("filters by operation", func(t *testing.T) {
		got, err := s.List(ctx, domain.MemoryQuery{Operation: "rider-service.getRider"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, ws.ID, got[0].ID)
	})

	t.Run("filters by flow", func(t *testing.T) {
		got, err := s.List(ctx, domain.MemoryQuery{Flow: "order-allocation"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, flowMem.ID, got[0].ID)
	})

	t.Run("includes inactive (unlike Search/Relevant)", func(t *testing.T) {
		got, err := s.List(ctx, domain.MemoryQuery{Operation: "rider-service.getRider"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, domain.MemorySuperseded, got[0].Status)
	})

	t.Run("limit defaults to 50 and is respected", func(t *testing.T) {
		got, err := s.List(ctx, domain.MemoryQuery{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})
}

func idsOf(ms []domain.Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}
